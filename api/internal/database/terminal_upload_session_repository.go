package database

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"
)

type TerminalUploadSessionRepository struct{ db *DB }

func NewTerminalUploadSessionRepository(db *DB) *TerminalUploadSessionRepository {
	return &TerminalUploadSessionRepository{db: db}
}

// DeleteForNodeTx removes only ephemeral upload mappings when a node is
// deleted. Terminal tickets and historical requests remain intact.
func (r *TerminalUploadSessionRepository) DeleteForNodeTx(tx *Tx, nodeID string) error {
	_, err := tx.Exec(`DELETE FROM terminal_upload_sessions WHERE node_id=$1`, nodeID)
	return err
}

// DeleteForNode revokes all ephemeral upload credentials when an Edge session
// is replaced or the node is disabled.
func (r *TerminalUploadSessionRepository) DeleteForNode(nodeID string) error {
	_, err := r.db.Exec(`DELETE FROM terminal_upload_sessions WHERE node_id=$1`, nodeID)
	return err
}

func (r *TerminalUploadSessionRepository) Create(rawToken, ticketHash, nodeID, printerID, sessionID string, expiresAt time.Time) error {
	_, err := r.db.Exec(`INSERT INTO terminal_upload_sessions(upload_token_hash,terminal_ticket_hash,node_id,printer_id,terminal_session_id,expires_at)
		VALUES($1,$2,$3,$4,$5,$6)`, uploadTokenHash(rawToken), ticketHash, nodeID, printerID, sessionID, expiresAt)
	return err
}

// DeleteOpenForTicket drops unused official upload mappings for a ticket so
// re-selecting official after backing out issues a fresh upload token binding.
func (r *TerminalUploadSessionRepository) DeleteOpenForTicket(ticketHash string) error {
	_, err := r.db.Exec(`DELETE FROM terminal_upload_sessions WHERE terminal_ticket_hash=$1 AND file_id IS NULL`, ticketHash)
	return err
}

// TerminalUploadSessionInfo is the Cloud-side binding for an official upload
// token issued after entry select.
type TerminalUploadSessionInfo struct {
	TicketHash string
	NodeID     string
	PrinterID  string
	SessionID  string
	FileID     sql.NullString
	ExpiresAt  time.Time
}

func (r *TerminalUploadSessionRepository) GetByToken(rawToken string, now time.Time) (*TerminalUploadSessionInfo, error) {
	info := &TerminalUploadSessionInfo{}
	err := r.db.QueryRow(`SELECT terminal_ticket_hash,node_id,printer_id,terminal_session_id,file_id,expires_at
		FROM terminal_upload_sessions WHERE upload_token_hash=$1 AND expires_at>$2`, uploadTokenHash(rawToken), now).Scan(
		&info.TicketHash, &info.NodeID, &info.PrinterID, &info.SessionID, &info.FileID, &info.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return info, nil
}

// GetByFileID returns the terminal binding created by an official upload.
func (r *TerminalUploadSessionRepository) GetByFileID(fileID string) (*TerminalUploadSessionInfo, error) {
	info := &TerminalUploadSessionInfo{}
	err := r.db.QueryRow(`SELECT terminal_ticket_hash,node_id,printer_id,terminal_session_id,file_id,expires_at
		FROM terminal_upload_sessions WHERE file_id=$1::uuid ORDER BY expires_at DESC LIMIT 1`, fileID).Scan(
		&info.TicketHash, &info.NodeID, &info.PrinterID, &info.SessionID, &info.FileID, &info.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return info, nil
}

// BindFile atomically consumes the official ticket only when upload succeeds.
func (r *TerminalUploadSessionRepository) BindFile(rawToken, fileID string, now time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ticketHash string
	err = tx.QueryRow(`SELECT terminal_ticket_hash FROM terminal_upload_sessions WHERE upload_token_hash=$1 AND file_id IS NULL AND expires_at>$2 FOR UPDATE`, uploadTokenHash(rawToken), now).Scan(&ticketHash)
	// Existing Edge QR uploads predate the entry bridge and deliberately have no
	// mapping. Preserve that protocol; a mapping, once present, is strict.
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("terminal upload session unavailable: %w", err)
	}
	result, err := tx.Exec(`UPDATE terminal_tickets SET status='consumed',consumed_at=$2 WHERE ticket_hash=$1 AND status='selected' AND selected_entry='official' AND expires_at>$2`, ticketHash, now)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fmt.Errorf("terminal ticket unavailable")
	}
	if _, err := tx.Exec(`UPDATE terminal_upload_sessions SET file_id=$2::uuid WHERE upload_token_hash=$1`, uploadTokenHash(rawToken), fileID); err != nil {
		return err
	}
	return tx.Commit()
}

func uploadTokenHash(raw string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) }
