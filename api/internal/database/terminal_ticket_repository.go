package database

import (
	"database/sql"
	"fmt"
	"time"

	"fly-print-cloud/api/internal/models"
)

type TerminalTicketRepository struct{ db *DB }

func NewTerminalTicketRepository(db *DB) *TerminalTicketRepository {
	return &TerminalTicketRepository{db: db}
}

// CancelActiveForNodeTx invalidates tickets that could still open an entry
// after the owning node has been removed. Historical tickets remain intact.
func (r *TerminalTicketRepository) CancelActiveForNodeTx(tx *Tx, nodeID string) error {
	_, err := tx.Exec(`UPDATE terminal_tickets SET status='cancelled',consumed_at=NULL
		WHERE node_id=$1 AND status IN ('issued','selected')`, nodeID)
	return err
}

// CancelActiveForNode invalidates issued/selected tickets when Edge refreshes
// the kiosk session (new QR) so stale phone tickets fail immediately.
func (r *TerminalTicketRepository) CancelActiveForNode(nodeID string) error {
	_, err := r.db.Exec(`UPDATE terminal_tickets SET status='cancelled',consumed_at=NULL
		WHERE node_id=$1 AND status IN ('issued','selected')`, nodeID)
	return err
}

// GetActiveForSession returns the current issued/selected ticket for a kiosk
// session, if any. Used to replay terminal_occupied after WS reconnect.
func (r *TerminalTicketRepository) GetActiveForSession(nodeID, sessionID string, now time.Time) (*models.TerminalTicket, error) {
	if nodeID == "" || sessionID == "" {
		return nil, sql.ErrNoRows
	}
	row := r.db.QueryRow(`SELECT id, ticket_hash, node_id, printer_id, terminal_session_id, selected_entry,
		status, issued_at, selected_at, consumed_at, expires_at FROM terminal_tickets
		WHERE node_id=$1 AND terminal_session_id=$2 AND status IN ('issued','selected') AND expires_at>$3
		ORDER BY issued_at DESC LIMIT 1`, nodeID, sessionID, now)
	return scanTerminalTicket(row)
}

func (r *TerminalTicketRepository) Create(ticket *models.TerminalTicket) error {
	return r.db.QueryRow(`INSERT INTO terminal_tickets
		(ticket_hash, node_id, printer_id, terminal_session_id, status, expires_at)
		VALUES ($1,$2,$3,$4,'issued',$5) RETURNING id, issued_at`,
		ticket.TicketHash, ticket.NodeID, ticket.PrinterID, ticket.TerminalSessionID, ticket.ExpiresAt,
	).Scan(&ticket.ID, &ticket.IssuedAt)
}

func (r *TerminalTicketRepository) HasCurrentSession(nodeID string, sessionNotBefore time.Time) (bool, error) {
	var exists bool
	err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM edge_terminal_sessions
		WHERE node_id=$1 AND terminal_session_id<>'' AND updated_at>=$2)`, nodeID, sessionNotBefore).Scan(&exists)
	return exists, err
}

// CreateForCurrentSession issues a ticket for the kiosk session created by the
// same QR-code request and binds that session to the ticket atomically. The raw
// ticket never enters the database; only its hash is persisted.
func (r *TerminalTicketRepository) CreateForCurrentSession(ticket *models.TerminalTicket, sessionNotBefore time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := tx.QueryRow(`SELECT terminal_session_id FROM edge_terminal_sessions
		WHERE node_id=$1 AND terminal_session_id<>'' AND updated_at>=$2
		FOR UPDATE`, ticket.NodeID, sessionNotBefore).Scan(&ticket.TerminalSessionID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("active terminal session not found")
		}
		return err
	}

	if err := tx.QueryRow(`INSERT INTO terminal_tickets
		(ticket_hash, node_id, printer_id, terminal_session_id, status, expires_at)
		VALUES ($1,$2,$3,$4,'issued',$5) RETURNING id, issued_at`,
		ticket.TicketHash, ticket.NodeID, ticket.PrinterID, ticket.TerminalSessionID, ticket.ExpiresAt,
	).Scan(&ticket.ID, &ticket.IssuedAt); err != nil {
		return err
	}

	result, err := tx.Exec(`UPDATE edge_terminal_sessions
		SET terminal_ticket_hash=$3, entry_type='entry', updated_at=$4
		WHERE node_id=$1 AND terminal_session_id=$2`,
		ticket.NodeID, ticket.TerminalSessionID, ticket.TicketHash, time.Now())
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return fmt.Errorf("active terminal session changed")
	}
	return tx.Commit()
}

func (r *TerminalTicketRepository) GetValidByHash(hash string, now time.Time) (*models.TerminalTicket, error) {
	row := r.db.QueryRow(`SELECT id, ticket_hash, node_id, printer_id, terminal_session_id, selected_entry,
		status, issued_at, selected_at, consumed_at, expires_at FROM terminal_tickets
		WHERE ticket_hash=$1 AND status IN ('issued','selected') AND expires_at>$2`, hash, now)
	return scanTerminalTicket(row)
}

// Select locks a ticket to one entry. While the ticket is still selected and
// not consumed, the user may re-select (same or different entry) after backing
// out of upload or entry selection without completing.
func (r *TerminalTicketRepository) Select(hash, entry string, now time.Time) (*models.TerminalTicket, error) {
	row := r.db.QueryRow(`UPDATE terminal_tickets SET selected_entry=$2,status='selected',selected_at=$3
		WHERE ticket_hash=$1 AND status IN ('issued','selected') AND expires_at>$3
		RETURNING id,ticket_hash,node_id,printer_id,terminal_session_id,selected_entry,status,issued_at,selected_at,consumed_at,expires_at`, hash, entry, now)
	ticket, err := scanTerminalTicket(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("ticket cannot be selected")
	}
	return ticket, err
}

func scanTerminalTicket(scanner interface{ Scan(...any) error }) (*models.TerminalTicket, error) {
	ticket := &models.TerminalTicket{}
	if err := scanner.Scan(&ticket.ID, &ticket.TicketHash, &ticket.NodeID, &ticket.PrinterID,
		&ticket.TerminalSessionID, &ticket.SelectedEntry, &ticket.Status, &ticket.IssuedAt,
		&ticket.SelectedAt, &ticket.ConsumedAt, &ticket.ExpiresAt); err != nil {
		return nil, err
	}
	return ticket, nil
}
