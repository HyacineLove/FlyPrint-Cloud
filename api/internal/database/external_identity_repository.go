package database

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"fly-print-cloud/api/internal/models"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPortalLoginTicketInvalid  = errors.New("portal login ticket invalid")
	ErrPortalLoginPortalMismatch = errors.New("portal login site portal mismatch")
	ErrExternalIdentityDisabled  = errors.New("external identity disabled")
)

type CompletePortalLoginInput struct {
	SitePortalCode  string
	TicketHash      string
	PortalAttemptID string
	ExternalUserID  string
	DisplayName     string
	ClaimCode       string
	ClaimExpiresAt  time.Time
	Now             time.Time
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
	if input.SitePortalCode == "" || (input.TicketHash == "" && input.PortalAttemptID == "") || input.ExternalUserID == "" ||
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
		claimBaseURL, portalDisplayName      string
		portalEnabled                        bool
	)
	if input.PortalAttemptID != "" {
		err = tx.QueryRow(`SELECT entry.node_id,entry.printer_id::text,entry.terminal_session_id,
			attempt.site_portal_code,attempt.status,entry.expires_at,portal.claim_base_url,portal.display_name,portal.enabled
			FROM entry_portal_attempts attempt
			JOIN entry_sessions entry ON entry.id=attempt.entry_session_id
			JOIN edge_terminal_sessions session ON session.node_id=entry.node_id
				AND session.terminal_session_id=entry.terminal_session_id AND session.qr_generation=entry.qr_generation
			JOIN edge_nodes node ON node.id=entry.node_id AND node.deleted_at IS NULL AND node.enabled=true
			JOIN site_portals portal ON portal.code=$2
			WHERE attempt.id=$1::uuid AND attempt.site_portal_code=$2 AND attempt.status='opened'
				AND attempt.version=entry.portal_attempt_version AND entry.status='entry_active'
			FOR UPDATE OF attempt,entry`, input.PortalAttemptID, input.SitePortalCode).Scan(
			&nodeID, &printerID, &terminalSessionID, &selectedEntry, &ticketStatus, &expiresAt, &claimBaseURL, &portalDisplayName, &portalEnabled,
		)
	} else {
		err = tx.QueryRow(`SELECT ticket.node_id,ticket.printer_id,ticket.terminal_session_id,
			COALESCE(ticket.selected_entry,''),ticket.status,ticket.expires_at,
			portal.claim_base_url,portal.display_name,portal.enabled
			FROM terminal_tickets ticket
			JOIN edge_terminal_sessions session ON session.node_id=ticket.node_id
				AND session.terminal_session_id=ticket.terminal_session_id
				AND session.terminal_ticket_hash=ticket.ticket_hash
			JOIN edge_nodes node ON node.id=ticket.node_id
				AND node.deleted_at IS NULL AND node.enabled=true
			JOIN site_portals portal ON portal.code=$2
			WHERE ticket.ticket_hash=$1
			FOR UPDATE OF ticket`, input.TicketHash, input.SitePortalCode).Scan(
			&nodeID, &printerID, &terminalSessionID, &selectedEntry, &ticketStatus,
			&expiresAt, &claimBaseURL, &portalDisplayName, &portalEnabled,
		)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPortalLoginTicketInvalid
		}
		return nil, fmt.Errorf("lock portal terminal ticket: %w", err)
	}
	validStatus := "selected"
	if input.PortalAttemptID != "" {
		validStatus = "opened"
	}
	if !portalEnabled || ticketStatus != validStatus || !expiresAt.After(input.Now) {
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
			(username,email,password_hash,role,status,last_login,print_quota_balance)
			VALUES ($1,$2,$3,'viewer','active',$4,50)
			RETURNING id`, username, email, passwordHash, input.Now).Scan(&cloudUserID); err != nil {
			return nil, fmt.Errorf("create silently mapped user: %w", err)
		}
		if _, err = tx.Exec(`INSERT INTO print_quota_transactions
			(user_id,transaction_type,delta,balance_after)
			VALUES ($1,'initial_grant',50,50)`, cloudUserID); err != nil {
			return nil, fmt.Errorf("grant initial print quota: %w", err)
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

	sessionResult, err := tx.Exec(`UPDATE edge_terminal_sessions
		SET site_portal_code=$3,cloud_user_id=$4
		WHERE node_id=$1 AND terminal_session_id=$2`,
		nodeID, terminalSessionID, input.SitePortalCode, cloudUserID)
	if err != nil {
		return nil, fmt.Errorf("bind portal identity to terminal session: %w", err)
	}
	if affected, rowsErr := sessionResult.RowsAffected(); rowsErr != nil || affected != 1 {
		return nil, ErrPortalLoginTicketInvalid
	}

	if input.PortalAttemptID != "" {
		claimHash := sha256.Sum256([]byte(input.ClaimCode))
		result, updateErr := tx.Exec(`UPDATE entry_sessions SET status='claim_pending'
			WHERE id=(SELECT entry_session_id FROM entry_portal_attempts WHERE id=$1::uuid)
			AND status='entry_active'`, input.PortalAttemptID)
		if updateErr != nil {
			return nil, fmt.Errorf("complete entry session: %w", updateErr)
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return nil, ErrPortalLoginTicketInvalid
		}
		if _, err = tx.Exec(`UPDATE entry_portal_attempts SET status='completed' WHERE id=$1::uuid AND status='opened'`, input.PortalAttemptID); err != nil {
			return nil, fmt.Errorf("complete portal attempt: %w", err)
		}
		if _, err = tx.Exec(`UPDATE entry_portal_attempts SET status='superseded'
			WHERE entry_session_id=(SELECT entry_session_id FROM entry_portal_attempts WHERE id=$1::uuid) AND status IN ('issued','opened')`, input.PortalAttemptID); err != nil {
			return nil, fmt.Errorf("retire portal attempts: %w", err)
		}
		if _, err = tx.Exec(`INSERT INTO entry_claims(entry_session_id,claim_hash,expires_at)
			VALUES((SELECT entry_session_id FROM entry_portal_attempts WHERE id=$1::uuid),$2,$3)`, input.PortalAttemptID, hex.EncodeToString(claimHash[:]), input.ClaimExpiresAt); err != nil {
			return nil, fmt.Errorf("persist entry claim: %w", err)
		}
	} else {
		result, updateErr := tx.Exec(`UPDATE terminal_tickets SET status='consumed',consumed_at=$2
			WHERE ticket_hash=$1 AND status='selected'`, input.TicketHash, input.Now)
		if updateErr != nil {
			return nil, fmt.Errorf("consume portal terminal ticket: %w", updateErr)
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return nil, ErrPortalLoginTicketInvalid
		}
	}
	readyEventID := ""
	if input.ClaimCode != "" && !input.ClaimExpiresAt.IsZero() {
		payload, marshalErr := json.Marshal(map[string]interface{}{
			"site_portal_code":         input.SitePortalCode,
			"site_portal_display_name": portalDisplayName,
			"claim_base_url":           claimBaseURL,
			"claim_code":               input.ClaimCode,
			"terminal_session_id":      terminalSessionID,
			"cloud_user_id":            cloudUserID,
			"expires_at":               input.ClaimExpiresAt,
		})
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal portal ready payload: %w", marshalErr)
		}
		readyEventID = uuid.NewString()
		if _, err = tx.Exec(`INSERT INTO portal_session_ready_outbox
			(id,node_id,payload,status,attempt_count,next_attempt_at)
			VALUES ($1::uuid,$2,$3::jsonb,'pending',0,CURRENT_TIMESTAMP)`,
			readyEventID, nodeID, payload); err != nil {
			return nil, fmt.Errorf("enqueue portal ready event: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.PortalLoginCompletion{
		NodeID:                nodeID,
		TerminalSessionID:     terminalSessionID,
		CloudUserID:           cloudUserID,
		SitePortalCode:        input.SitePortalCode,
		SitePortalDisplayName: portalDisplayName,
		ClaimBaseURL:          claimBaseURL,
		ReadyEventID:          readyEventID,
	}, nil
}
