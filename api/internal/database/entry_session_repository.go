package database

import (
	"database/sql"
	"errors"
	"time"

	"fly-print-cloud/api/internal/models"

	"github.com/google/uuid"
)

var ErrEntrySessionInvalid = errors.New("entry session invalid")

type EntrySessionRepository struct{ db *DB }

func NewEntrySessionRepository(db *DB) *EntrySessionRepository {
	return &EntrySessionRepository{db: db}
}

// Issue invalidates every unfinished entry chain for this terminal before the
// new QR is made visible.  The Edge session/generation row is locked so a QR
// refresh and a later Claim validation cannot both succeed for the same chain.
func (r *EntrySessionRepository) Issue(nodeID, printerID, terminalSessionID string, generation int64, t1Hash string, expiresAt time.Time) (*models.EntrySession, error) {
	tx, err := r.db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var currentSession string
	var currentGeneration int64
	if err = tx.QueryRow(`SELECT terminal_session_id,qr_generation FROM edge_terminal_sessions WHERE node_id=$1 FOR UPDATE`, nodeID).Scan(&currentSession, &currentGeneration); err != nil {
		return nil, ErrEntrySessionInvalid
	}
	if currentSession != terminalSessionID || currentGeneration != generation {
		return nil, ErrEntrySessionInvalid
	}
	result, err := tx.Exec(`UPDATE entry_sessions SET status='invalidated',invalidated_at=CURRENT_TIMESTAMP
		WHERE node_id=$1 AND status IN ('qr_issued','mask_pending','entry_active','claim_pending')`, nodeID)
	if err != nil {
		return nil, err
	}
	_ = result
	entry := &models.EntrySession{NodeID: nodeID, PrinterID: printerID, TerminalSessionID: terminalSessionID, QRGeneration: generation, T1Hash: t1Hash, ExpiresAt: expiresAt}
	err = tx.QueryRow(`INSERT INTO entry_sessions(t1_hash,node_id,printer_id,terminal_session_id,qr_generation,status,expires_at)
		VALUES($1,$2,$3,$4,$5,'qr_issued',$6)
		RETURNING id,issued_at`, t1Hash, nodeID, printerID, terminalSessionID, generation, expiresAt).Scan(&entry.ID, &entry.IssuedAt)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	entry.Status = "qr_issued"
	return entry, nil
}

// InvalidateForNode is deliberately a single UPDATE: every T1/T2/T3/Claim
// verification joins the parent session and therefore observes it immediately.
func (r *EntrySessionRepository) InvalidateForNode(nodeID string) error {
	_, err := r.db.Exec(`UPDATE entry_sessions SET status='invalidated',invalidated_at=CURRENT_TIMESTAMP
		WHERE node_id=$1 AND status IN ('qr_issued','mask_pending','entry_active','claim_pending')`, nodeID)
	return err
}

// Acquire atomically reserves a T1 for the first arriving browser and creates
// a distinct temporary acquire lease.  It intentionally does not activate T2.
func (r *EntrySessionRepository) Acquire(t1Hash, acquireHash, commandID string, now time.Time) (*models.EntrySession, error) {
	row := r.db.QueryRow(`UPDATE entry_sessions SET status='mask_pending',acquire_hash=$2,mask_command_id=$3::uuid
		WHERE t1_hash=$1 AND status='qr_issued' AND expires_at>$4
		RETURNING id,t1_hash,COALESCE(acquire_hash,''),COALESCE(t2_hash,''),node_id,printer_id::text,terminal_session_id,qr_generation,status,
		COALESCE(mask_command_id::text,''),mask_confirmed_at,portal_attempt_version,issued_at,expires_at`, t1Hash, acquireHash, commandID, now)
	entry, err := scanEntrySession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEntrySessionInvalid
	}
	return entry, err
}

func (r *EntrySessionRepository) MarkMasked(nodeID, commandID string, now time.Time) error {
	result, err := r.db.Exec(`UPDATE entry_sessions SET mask_confirmed_at=$3
		WHERE node_id=$1 AND mask_command_id=$2::uuid AND status='mask_pending' AND expires_at>$3`, nodeID, commandID, now)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrEntrySessionInvalid
	}
	return nil
}

// Activate converts the temporary acquire lease to T2 only after Edge has
// confirmed the screen is masked.  The caller owns the raw T2 solely to place
// it in an HttpOnly cookie.
func (r *EntrySessionRepository) Activate(acquireHash, entryID, t2Hash string, now time.Time) (*models.EntrySession, error) {
	row := r.db.QueryRow(`UPDATE entry_sessions SET status='entry_active',t2_hash=$2
		WHERE acquire_hash=$1 AND id=$3::uuid AND status='mask_pending' AND mask_confirmed_at IS NOT NULL AND expires_at>$4
		RETURNING id,t1_hash,COALESCE(acquire_hash,''),COALESCE(t2_hash,''),node_id,printer_id::text,terminal_session_id,qr_generation,status,
		COALESCE(mask_command_id::text,''),mask_confirmed_at,portal_attempt_version,issued_at,expires_at`, acquireHash, t2Hash, entryID, now)
	entry, err := scanEntrySession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEntrySessionInvalid
	}
	return entry, err
}

func (r *EntrySessionRepository) GetByAcquire(acquireHash, entryID string, now time.Time) (*models.EntrySession, error) {
	row := r.db.QueryRow(`SELECT id,t1_hash,COALESCE(acquire_hash,''),COALESCE(t2_hash,''),node_id,printer_id::text,terminal_session_id,qr_generation,status,
		COALESCE(mask_command_id::text,''),mask_confirmed_at,portal_attempt_version,issued_at,expires_at
		FROM entry_sessions WHERE acquire_hash=$1 AND id=$2::uuid AND status='mask_pending' AND expires_at>$3`, acquireHash, entryID, now)
	entry, err := scanEntrySession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEntrySessionInvalid
	}
	return entry, err
}

func (r *EntrySessionRepository) GetActiveByT2(t2Hash, entryID string, now time.Time) (*models.EntrySession, error) {
	row := r.db.QueryRow(`SELECT id,t1_hash,COALESCE(acquire_hash,''),COALESCE(t2_hash,''),node_id,printer_id::text,terminal_session_id,qr_generation,status,
		COALESCE(mask_command_id::text,''),mask_confirmed_at,portal_attempt_version,issued_at,expires_at
		FROM entry_sessions WHERE t2_hash=$1 AND id=$2::uuid AND status='entry_active' AND expires_at>$3`, t2Hash, entryID, now)
	entry, err := scanEntrySession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEntrySessionInvalid
	}
	return entry, err
}

// CreateAttempt retires the previous handoff before issuing a new one.  This
// is how returning to the portal list invalidates a late SSO callback.
func (r *EntrySessionRepository) CreateAttempt(t2Hash, portalCode, t3Hash string, expiresAt, now time.Time) (*models.EntryPortalAttempt, error) {
	tx, err := r.db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var sessionID string
	var version int
	err = tx.QueryRow(`SELECT id,portal_attempt_version FROM entry_sessions WHERE t2_hash=$1 AND status='entry_active' AND expires_at>$2 FOR UPDATE`, t2Hash, now).Scan(&sessionID, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEntrySessionInvalid
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE entry_portal_attempts SET status='superseded' WHERE entry_session_id=$1 AND status IN ('issued','opened')`, sessionID); err != nil {
		return nil, err
	}
	version++
	if _, err = tx.Exec(`UPDATE entry_sessions SET portal_attempt_version=$2 WHERE id=$1`, sessionID, version); err != nil {
		return nil, err
	}
	attempt := &models.EntryPortalAttempt{ID: uuid.NewString(), EntrySessionID: sessionID, Version: version, SitePortalCode: portalCode, T3Hash: t3Hash, Status: "issued", ExpiresAt: expiresAt}
	if _, err = tx.Exec(`INSERT INTO entry_portal_attempts(id,entry_session_id,version,site_portal_code,t3_hash,status,expires_at)
		VALUES($1::uuid,$2::uuid,$3,$4,$5,'issued',$6)`, attempt.ID, sessionID, version, portalCode, t3Hash, expiresAt); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return attempt, nil
}

// ConsumeT3 opens a current Portal attempt once.  The Portal persists the
// returned attempt ID in OAuth state; it never carries a terminal credential.
func (r *EntrySessionRepository) ConsumeT3(t3Hash, portalCode string, now time.Time) (*models.EntryPortalAttempt, *models.EntrySession, error) {
	tx, err := r.db.BeginTx()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	var attempt models.EntryPortalAttempt
	var entry models.EntrySession
	err = tx.QueryRow(`SELECT a.id::text,a.entry_session_id::text,a.version,a.site_portal_code,a.t3_hash,a.status,a.expires_at,
		e.id::text,e.t1_hash,COALESCE(e.acquire_hash,''),COALESCE(e.t2_hash,''),e.node_id,e.printer_id::text,e.terminal_session_id,e.qr_generation,e.status,
		COALESCE(e.mask_command_id::text,''),e.mask_confirmed_at,e.portal_attempt_version,e.issued_at,e.expires_at
		FROM entry_portal_attempts a JOIN entry_sessions e ON e.id=a.entry_session_id
		WHERE a.t3_hash=$1 AND a.site_portal_code=$2 AND a.status='issued' AND a.expires_at>$3
		AND e.status='entry_active' AND e.expires_at>$3 AND a.version=e.portal_attempt_version FOR UPDATE OF a,e`, t3Hash, portalCode, now).Scan(
		&attempt.ID, &attempt.EntrySessionID, &attempt.Version, &attempt.SitePortalCode, &attempt.T3Hash, &attempt.Status, &attempt.ExpiresAt,
		&entry.ID, &entry.T1Hash, &entry.AcquireHash, &entry.T2Hash, &entry.NodeID, &entry.PrinterID, &entry.TerminalSessionID, &entry.QRGeneration, &entry.Status, &entry.MaskCommandID, &entry.MaskConfirmedAt, &entry.PortalAttemptVersion, &entry.IssuedAt, &entry.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrEntrySessionInvalid
	}
	if err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(`UPDATE entry_portal_attempts SET status='opened' WHERE id=$1::uuid`, attempt.ID); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	attempt.Status = "opened"
	return &attempt, &entry, nil
}

func (r *EntrySessionRepository) GetAttemptForCompletion(attemptID, portalCode string, now time.Time) (*models.EntryPortalAttempt, *models.EntrySession, error) {
	row := r.db.QueryRow(`SELECT a.id::text,a.entry_session_id::text,a.version,a.site_portal_code,a.t3_hash,a.status,a.expires_at,
		e.id::text,e.t1_hash,COALESCE(e.acquire_hash,''),COALESCE(e.t2_hash,''),e.node_id,e.printer_id::text,e.terminal_session_id,e.qr_generation,e.status,
		COALESCE(e.mask_command_id::text,''),e.mask_confirmed_at,e.portal_attempt_version,e.issued_at,e.expires_at
		FROM entry_portal_attempts a JOIN entry_sessions e ON e.id=a.entry_session_id
		WHERE a.id=$1::uuid AND a.site_portal_code=$2 AND a.status='opened' AND a.expires_at>$3
		AND e.status='entry_active' AND e.expires_at>$3 AND a.version=e.portal_attempt_version`, attemptID, portalCode, now)
	var attempt models.EntryPortalAttempt
	var entry models.EntrySession
	err := row.Scan(&attempt.ID, &attempt.EntrySessionID, &attempt.Version, &attempt.SitePortalCode, &attempt.T3Hash, &attempt.Status, &attempt.ExpiresAt,
		&entry.ID, &entry.T1Hash, &entry.AcquireHash, &entry.T2Hash, &entry.NodeID, &entry.PrinterID, &entry.TerminalSessionID, &entry.QRGeneration, &entry.Status, &entry.MaskCommandID, &entry.MaskConfirmedAt, &entry.PortalAttemptVersion, &entry.IssuedAt, &entry.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrEntrySessionInvalid
	}
	return &attempt, &entry, err
}

// ValidateClaim serializes a Portal claim exchange with QR refresh.  A caller
// gets only terminal bindings, never an identity access token.
func (r *EntrySessionRepository) ValidateClaim(claimHash, nodeID, terminalSessionID string, generation int64, now time.Time) error {
	result, err := r.db.Exec(`UPDATE entry_sessions SET status='redeemed'
		WHERE id=(SELECT c.entry_session_id FROM entry_claims c WHERE c.claim_hash=$1 AND c.expires_at>$5 AND c.redeemed_at IS NULL)
		AND node_id=$2 AND terminal_session_id=$3 AND qr_generation=$4 AND status='claim_pending'`, claimHash, nodeID, terminalSessionID, generation, now)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrEntrySessionInvalid
	}
	_, err = r.db.Exec(`UPDATE entry_claims SET redeemed_at=$2 WHERE claim_hash=$1`, claimHash, now)
	return err
}

func scanEntrySession(scanner interface{ Scan(...any) error }) (*models.EntrySession, error) {
	e := &models.EntrySession{}
	err := scanner.Scan(&e.ID, &e.T1Hash, &e.AcquireHash, &e.T2Hash, &e.NodeID, &e.PrinterID, &e.TerminalSessionID, &e.QRGeneration, &e.Status, &e.MaskCommandID, &e.MaskConfirmedAt, &e.PortalAttemptVersion, &e.IssuedAt, &e.ExpiresAt)
	return e, err
}
