package database

import (
	"database/sql"
	"time"
)

// TerminalSessionRepository is the Cloud-side source of truth for the active
// kiosk session. An Edge restart reports an empty session, so queued work is
// never dispatched based only on stale ticket data.
type TerminalSessionRepository struct{ db *DB }

func NewTerminalSessionRepository(db *DB) *TerminalSessionRepository {
	return &TerminalSessionRepository{db: db}
}

type TerminalSessionSnapshot struct {
	NodeID             string
	TerminalSessionID  string
	TerminalTicketHash string
	EntryType          string
	QRGeneration       int64
}

func (r *TerminalSessionRepository) Get(nodeID string) (*TerminalSessionSnapshot, error) {
	row := r.db.QueryRow(`SELECT node_id, COALESCE(terminal_session_id,''), COALESCE(terminal_ticket_hash,''),
		COALESCE(entry_type,''), COALESCE(qr_generation,0)
		FROM edge_terminal_sessions WHERE node_id=$1`, nodeID)
	snap := &TerminalSessionSnapshot{}
	if err := row.Scan(&snap.NodeID, &snap.TerminalSessionID, &snap.TerminalTicketHash, &snap.EntryType, &snap.QRGeneration); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return snap, nil
}

func (r *TerminalSessionRepository) Report(nodeID, sessionID, ticketHash, entryType string, now time.Time) error {
	return r.ReportWithGeneration(nodeID, sessionID, ticketHash, entryType, 0, now)
}

func (r *TerminalSessionRepository) ReportWithGeneration(nodeID, sessionID, ticketHash, entryType string, generation int64, now time.Time) error {
	_, err := r.db.Exec(`INSERT INTO edge_terminal_sessions(node_id,terminal_session_id,terminal_ticket_hash,entry_type,qr_generation,updated_at)
		VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6)
		ON CONFLICT(node_id) DO UPDATE SET terminal_session_id=EXCLUDED.terminal_session_id,terminal_ticket_hash=EXCLUDED.terminal_ticket_hash,
		entry_type=EXCLUDED.entry_type,qr_generation=EXCLUDED.qr_generation,updated_at=EXCLUDED.updated_at`,
		nodeID, sessionID, ticketHash, entryType, generation, now)
	return err
}

func (r *TerminalSessionRepository) Matches(nodeID, sessionID, ticketHash string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM edge_terminal_sessions WHERE node_id=$1 AND terminal_session_id=$2
		AND (terminal_ticket_hash=$3 OR terminal_ticket_hash IS NULL)`, nodeID, sessionID, ticketHash).Scan(&count)
	return count == 1, err
}
