package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type PortalSessionReadyEvent struct {
	ID            string
	NodeID        string
	Payload       []byte
	AttemptCount  int
	NextAttemptAt time.Time
}

type PortalSessionReadyOutboxRepository struct {
	db *DB
}

func NewPortalSessionReadyOutboxRepository(db *DB) *PortalSessionReadyOutboxRepository {
	return &PortalSessionReadyOutboxRepository{db: db}
}

// EnqueueTx persists the one-time Portal notification in the login transaction.
func (r *PortalSessionReadyOutboxRepository) EnqueueTx(tx *sql.Tx, nodeID string, payload interface{}) (string, error) {
	if tx == nil || nodeID == "" {
		return "", errors.New("portal ready outbox transaction and node are required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal portal ready payload: %w", err)
	}
	eventID := uuid.NewString()
	_, err = tx.Exec(`INSERT INTO portal_session_ready_outbox
		(id,node_id,payload,status,attempt_count,next_attempt_at)
		VALUES ($1::uuid,$2,$3::jsonb,'pending',0,CURRENT_TIMESTAMP)`, eventID, nodeID, encoded)
	if err != nil {
		return "", fmt.Errorf("enqueue portal ready event: %w", err)
	}
	return eventID, nil
}

func (r *PortalSessionReadyOutboxRepository) ClaimDue(now time.Time) (*PortalSessionReadyEvent, error) {
	event := &PortalSessionReadyEvent{}
	err := r.db.QueryRow(`WITH candidate AS (
		SELECT id FROM portal_session_ready_outbox
		WHERE status='pending' AND next_attempt_at <= $1
		ORDER BY next_attempt_at,created_at
		FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE portal_session_ready_outbox outbox
	SET attempt_count=outbox.attempt_count+1,next_attempt_at=$2
	FROM candidate
	WHERE outbox.id=candidate.id
	RETURNING outbox.id::text,outbox.node_id,outbox.payload::text,outbox.attempt_count,outbox.next_attempt_at`,
		now, now.Add(5*time.Minute),
	).Scan(&event.ID, &event.NodeID, &event.Payload, &event.AttemptCount, &event.NextAttemptAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return event, nil
}

func (r *PortalSessionReadyOutboxRepository) MarkDelivered(id string) error {
	_, err := r.db.Exec(`UPDATE portal_session_ready_outbox
		SET status='delivered',delivered_at=CURRENT_TIMESTAMP
		WHERE id=$1::uuid AND status='pending'`, id)
	return err
}

func (r *PortalSessionReadyOutboxRepository) Retry(id string, attempt int, message string, now time.Time) error {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second * time.Duration(1<<minInt(attempt-1, 8))
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	_, err := r.db.Exec(`UPDATE portal_session_ready_outbox
		SET next_attempt_at=$2,last_error=$3
		WHERE id=$1::uuid AND status='pending'`, id, now.Add(delay), message)
	return err
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
