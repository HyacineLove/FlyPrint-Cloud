package database

import (
	"database/sql"
	"regexp"
	"testing"
	"time"

	"fly-print-cloud/api/internal/models"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreateForCurrentSessionBindsTicketAtomically(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewTerminalTicketRepository(&DB{DB: sqlDB})
	notBefore := time.Unix(100, 0)
	expiresAt := time.Unix(400, 0)
	issuedAt := time.Unix(101, 0)
	ticket := &models.TerminalTicket{TicketHash: "ticket-hash", NodeID: "node-1", PrinterID: "printer-1", ExpiresAt: expiresAt}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT terminal_session_id FROM edge_terminal_sessions
		WHERE node_id=$1 AND terminal_session_id<>'' AND updated_at>=$2
		FOR UPDATE`)).WithArgs("node-1", notBefore).
		WillReturnRows(sqlmock.NewRows([]string{"terminal_session_id"}).AddRow("session-1"))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO terminal_tickets
		(ticket_hash, node_id, printer_id, terminal_session_id, status, expires_at)
		VALUES ($1,$2,$3,$4,'issued',$5) RETURNING id, issued_at`)).
		WithArgs("ticket-hash", "node-1", "printer-1", "session-1", expiresAt).
		WillReturnRows(sqlmock.NewRows([]string{"id", "issued_at"}).AddRow("ticket-1", issuedAt))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE edge_terminal_sessions
		SET terminal_ticket_hash=$3, entry_type='entry', updated_at=$4
		WHERE node_id=$1 AND terminal_session_id=$2`)).
		WithArgs("node-1", "session-1", "ticket-hash", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.CreateForCurrentSession(ticket, notBefore); err != nil {
		t.Fatalf("CreateForCurrentSession() error = %v", err)
	}
	if ticket.ID != "ticket-1" || ticket.TerminalSessionID != "session-1" || !ticket.IssuedAt.Equal(issuedAt) {
		t.Fatalf("unexpected ticket after create: %#v", ticket)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateForCurrentSessionRejectsMissingSession(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewTerminalTicketRepository(&DB{DB: sqlDB})
	notBefore := time.Unix(100, 0)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT terminal_session_id FROM edge_terminal_sessions
		WHERE node_id=$1 AND terminal_session_id<>'' AND updated_at>=$2
		FOR UPDATE`)).WithArgs("node-1", notBefore).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	err = repo.CreateForCurrentSession(&models.TerminalTicket{NodeID: "node-1"}, notBefore)
	if err == nil {
		t.Fatal("CreateForCurrentSession() should reject a missing active session")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCancelActiveForNodeTxUpdatesOnlyOpenTickets(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	repo := NewTerminalTicketRepository(&DB{DB: sqlDB})
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE terminal_tickets SET status='cancelled',consumed_at=NULL
		WHERE node_id=$1 AND status IN ('issued','selected')`)).
		WithArgs("node-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tx, err := (&DB{DB: sqlDB}).BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CancelActiveForNodeTx(tx, "node-1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSelectAllowsReselectWhileNotConsumed(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewTerminalTicketRepository(&DB{DB: sqlDB})
	now := time.Unix(200, 0)
	issuedAt := time.Unix(100, 0)
	selectedAt := now
	expiresAt := time.Unix(500, 0)

	mock.ExpectQuery(regexp.QuoteMeta(`UPDATE terminal_tickets SET selected_entry=$2,status='selected',selected_at=$3
		WHERE ticket_hash=$1 AND status IN ('issued','selected') AND expires_at>$3
		RETURNING id,ticket_hash,node_id,printer_id,terminal_session_id,selected_entry,status,issued_at,selected_at,consumed_at,expires_at`)).
		WithArgs("ticket-hash", "official", now).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "ticket_hash", "node_id", "printer_id", "terminal_session_id", "selected_entry",
			"status", "issued_at", "selected_at", "consumed_at", "expires_at",
		}).AddRow("ticket-1", "ticket-hash", "node-1", "printer-1", "session-1", "official",
			"selected", issuedAt, selectedAt, nil, expiresAt))

	ticket, err := repo.Select("ticket-hash", "official", now)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if ticket.SelectedEntry == nil || *ticket.SelectedEntry != "official" || ticket.Status != "selected" {
		t.Fatalf("unexpected ticket: %#v", ticket)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
