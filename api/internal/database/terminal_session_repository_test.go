package database

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestTerminalSessionMatchesAllowsFirstPreviewBeforeTicketBinding(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	repo := NewTerminalSessionRepository(&DB{DB: sqlDB})
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM edge_terminal_sessions WHERE node_id=$1 AND terminal_session_id=$2
		AND (terminal_ticket_hash=$3 OR terminal_ticket_hash IS NULL)`)).
		WithArgs("node-1", "session-1", "ticket-hash").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	matched, err := repo.Matches("node-1", "session-1", "ticket-hash")
	if err != nil || !matched {
		t.Fatalf("Matches() = (%v, %v), want (true, nil)", matched, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalSessionMatchesRejectsDifferentSession(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	repo := NewTerminalSessionRepository(&DB{DB: sqlDB})
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM edge_terminal_sessions WHERE node_id=$1 AND terminal_session_id=$2
		AND (terminal_ticket_hash=$3 OR terminal_ticket_hash IS NULL)`)).
		WithArgs("node-1", "other-session", "ticket-hash").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	matched, err := repo.Matches("node-1", "other-session", "ticket-hash")
	if err != nil || matched {
		t.Fatalf("Matches() = (%v, %v), want (false, nil)", matched, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
