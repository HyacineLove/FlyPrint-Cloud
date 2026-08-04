package database

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPortalSessionReadyOutboxClaimsAndCompletesEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()
	repo := NewPortalSessionReadyOutboxRepository(&DB{DB: db})
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery("WITH candidate AS").WithArgs(now, now.Add(5*time.Minute)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "node_id", "payload", "attempt_count", "next_attempt_at"}).
			AddRow("event-1", "node-1", `{"claim_code":"claim"}`, 1, now.Add(5*time.Minute)))
	event, err := repo.ClaimDue(now)
	if err != nil {
		t.Fatalf("ClaimDue() error = %v", err)
	}
	if event == nil || event.ID != "event-1" || event.NodeID != "node-1" {
		t.Fatalf("unexpected event: %#v", event)
	}

	mock.ExpectExec("UPDATE portal_session_ready_outbox SET status='delivered'").WithArgs("event-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.MarkDelivered("event-1"); err != nil {
		t.Fatalf("MarkDelivered() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
