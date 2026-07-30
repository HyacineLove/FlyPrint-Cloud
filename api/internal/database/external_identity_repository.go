package database

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"fly-print-cloud/api/internal/models"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPortalLoginTicketInvalid  = errors.New("portal login ticket invalid")
	ErrPortalLoginPortalMismatch = errors.New("portal login site portal mismatch")
	ErrExternalIdentityDisabled  = errors.New("external identity disabled")
)

type CompletePortalLoginInput struct {
	SitePortalCode string
	TicketHash     string
	ExternalUserID string
	DisplayName    string
	Now            time.Time
}

type ExternalIdentityRepository struct {
	db *DB
}

func NewExternalIdentityRepository(db *DB) *ExternalIdentityRepository {
	return &ExternalIdentityRepository{db: db}
}

func silentUserLogin(sitePortalCode, externalUserID string) (string, string) {
	digest := sha256.Sum256([]byte(sitePortalCode + "\x00" + externalUserID))
	suffix := hex.EncodeToString(digest[:12])
	return "sp_" + suffix, suffix + "@identity.flyprint.invalid"
}

func externalOnlyPasswordHash() (string, error) {
	randomPassword := make([]byte, 32)
	if _, err := rand.Read(randomPassword); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(randomPassword)), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (r *ExternalIdentityRepository) CompleteLogin(input CompletePortalLoginInput) (*models.PortalLoginCompletion, error) {
	input.SitePortalCode = strings.TrimSpace(input.SitePortalCode)
	input.ExternalUserID = strings.TrimSpace(input.ExternalUserID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.SitePortalCode == "" || input.TicketHash == "" || input.ExternalUserID == "" ||
		input.DisplayName == "" || input.Now.IsZero() {
		return nil, ErrPortalLoginTicketInvalid
	}

	tx, err := r.db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var (
		nodeID, printerID, terminalSessionID string
		selectedEntry, ticketStatus          string
		expiresAt                            time.Time
		claimBaseURL                         string
		portalEnabled                        bool
	)
	err = tx.QueryRow(`SELECT ticket.node_id,ticket.printer_id,ticket.terminal_session_id,
		COALESCE(ticket.selected_entry,''),ticket.status,ticket.expires_at,
		portal.claim_base_url,portal.enabled
		FROM terminal_tickets ticket
		JOIN edge_terminal_sessions session ON session.node_id=ticket.node_id
			AND session.terminal_session_id=ticket.terminal_session_id
			AND session.terminal_ticket_hash=ticket.ticket_hash
		JOIN site_portals portal ON portal.code=$2
		WHERE ticket.ticket_hash=$1
		FOR UPDATE OF ticket`, input.TicketHash, input.SitePortalCode).Scan(
		&nodeID, &printerID, &terminalSessionID, &selectedEntry, &ticketStatus,
		&expiresAt, &claimBaseURL, &portalEnabled,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPortalLoginTicketInvalid
		}
		return nil, fmt.Errorf("lock portal terminal ticket: %w", err)
	}
	if !portalEnabled || ticketStatus != "selected" || !expiresAt.After(input.Now) {
		return nil, ErrPortalLoginTicketInvalid
	}
	if selectedEntry != input.SitePortalCode {
		return nil, ErrPortalLoginPortalMismatch
	}

	var cloudUserID, userStatus string
	err = tx.QueryRow(`SELECT identity.cloud_user_id,user_account.status
		FROM external_identities identity
		JOIN users user_account ON user_account.id=identity.cloud_user_id
		WHERE identity.site_portal_code=$1 AND identity.external_user_id=$2
		FOR UPDATE OF identity,user_account`, input.SitePortalCode, input.ExternalUserID).
		Scan(&cloudUserID, &userStatus)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		username, email := silentUserLogin(input.SitePortalCode, input.ExternalUserID)
		passwordHash, hashErr := externalOnlyPasswordHash()
		if hashErr != nil {
			return nil, fmt.Errorf("create external-only password hash: %w", hashErr)
		}
		if err = tx.QueryRow(`INSERT INTO users
			(username,email,password_hash,role,status,last_login)
			VALUES ($1,$2,$3,'viewer','active',$4)
			RETURNING id`, username, email, passwordHash, input.Now).Scan(&cloudUserID); err != nil {
			return nil, fmt.Errorf("create silently mapped user: %w", err)
		}
		if _, err = tx.Exec(`INSERT INTO external_identities
			(site_portal_code,external_user_id,cloud_user_id,display_name,last_login_at)
			VALUES ($1,$2,$3,$4,$5)`,
			input.SitePortalCode, input.ExternalUserID, cloudUserID, input.DisplayName, input.Now); err != nil {
			return nil, fmt.Errorf("create external identity mapping: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("get external identity mapping: %w", err)
	case userStatus != "active":
		return nil, ErrExternalIdentityDisabled
	default:
		if _, err = tx.Exec(`UPDATE external_identities SET display_name=$3,last_login_at=$4
			WHERE site_portal_code=$1 AND external_user_id=$2`,
			input.SitePortalCode, input.ExternalUserID, input.DisplayName, input.Now); err != nil {
			return nil, fmt.Errorf("update external identity mapping: %w", err)
		}
		if _, err = tx.Exec(`UPDATE users SET last_login=$2 WHERE id=$1`, cloudUserID, input.Now); err != nil {
			return nil, fmt.Errorf("update mapped user last login: %w", err)
		}
	}

	result, err := tx.Exec(`UPDATE terminal_tickets SET status='consumed',consumed_at=$2
		WHERE ticket_hash=$1 AND status='selected'`, input.TicketHash, input.Now)
	if err != nil {
		return nil, fmt.Errorf("consume portal terminal ticket: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		return nil, ErrPortalLoginTicketInvalid
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.PortalLoginCompletion{
		NodeID:            nodeID,
		TerminalSessionID: terminalSessionID,
		CloudUserID:       cloudUserID,
		SitePortalCode:    input.SitePortalCode,
		ClaimBaseURL:      claimBaseURL,
	}, nil
}
