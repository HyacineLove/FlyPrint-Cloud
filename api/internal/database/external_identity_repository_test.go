package database

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCompletePortalLoginCreatesMappingAndConsumesTicketInOneTransaction(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewExternalIdentityRepository(db)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(5 * time.Minute)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ticket.node_id,ticket.printer_id,ticket.terminal_session_id,
		COALESCE(ticket.selected_entry,''),ticket.status,ticket.expires_at,
		portal.claim_base_url,portal.enabled
		FROM terminal_tickets ticket
		JOIN edge_terminal_sessions session ON session.node_id=ticket.node_id
			AND session.terminal_session_id=ticket.terminal_session_id
			AND session.terminal_ticket_hash=ticket.ticket_hash
		JOIN edge_nodes node ON node.id=ticket.node_id
			AND node.deleted_at IS NULL AND node.enabled=true
		JOIN site_portals portal ON portal.code=$2
		WHERE ticket.ticket_hash=$1
		FOR UPDATE OF ticket`)).
		WithArgs("ticket-hash", "official").
		WillReturnRows(sqlmock.NewRows([]string{
			"node_id", "printer_id", "terminal_session_id", "selected_entry",
			"status", "expires_at", "claim_base_url", "enabled",
		}).AddRow("edge-1", "printer-1", "session-1", "official", "selected", expiresAt,
			"https://portal.example.test", true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT identity.cloud_user_id,user_account.status
		FROM external_identities identity
		JOIN users user_account ON user_account.id=identity.cloud_user_id
		WHERE identity.site_portal_code=$1 AND identity.external_user_id=$2
		FOR UPDATE OF identity,user_account`)).
		WithArgs("official", "external-user-1").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO users
		(username,email,password_hash,role,status,last_login,print_quota_balance)
		VALUES ($1,$2,$3,'viewer','active',$4,50)
		RETURNING id`)).
		WithArgs("sp_321612b9d446bc5d4c63b324", "321612b9d446bc5d4c63b324@identity.flyprint.invalid", sqlmock.AnyArg(), now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("cloud-user-1"))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO print_quota_transactions
		(user_id,transaction_type,delta,balance_after)
		VALUES ($1,'initial_grant',50,50)`)).
		WithArgs("cloud-user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO external_identities
		(site_portal_code,external_user_id,cloud_user_id,display_name,last_login_at)
		VALUES ($1,$2,$3,$4,$5)`)).
		WithArgs("official", "external-user-1", "cloud-user-1", "张老师", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE terminal_tickets SET status='consumed',consumed_at=$2
		WHERE ticket_hash=$1 AND status='selected'`)).
		WithArgs("ticket-hash", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	completion, err := repo.CompleteLogin(CompletePortalLoginInput{
		SitePortalCode: "official",
		TicketHash:     "ticket-hash",
		ExternalUserID: "external-user-1",
		DisplayName:    "张老师",
		Now:            now,
	})
	if err != nil {
		t.Fatalf("CompleteLogin() error = %v", err)
	}
	if completion.NodeID != "edge-1" || completion.TerminalSessionID != "session-1" ||
		completion.CloudUserID != "cloud-user-1" || completion.ClaimBaseURL != "https://portal.example.test" {
		t.Fatalf("unexpected completion: %#v", completion)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletePortalLoginReusesExistingMapping(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewExternalIdentityRepository(db)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT ticket.node_id").
		WithArgs("ticket-hash", "official").
		WillReturnRows(sqlmock.NewRows([]string{
			"node_id", "printer_id", "terminal_session_id", "selected_entry",
			"status", "expires_at", "claim_base_url", "enabled",
		}).AddRow("edge-1", "printer-1", "session-1", "official", "selected", now.Add(time.Minute),
			"https://portal.example.test", true))
	mock.ExpectQuery("SELECT identity.cloud_user_id").
		WithArgs("official", "external-user-1").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_user_id", "status"}).AddRow("cloud-user-existing", "active"))
	mock.ExpectExec("UPDATE external_identities SET display_name").
		WithArgs("official", "external-user-1", "张老师", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE users SET last_login").
		WithArgs("cloud-user-existing", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE terminal_tickets SET status='consumed'").
		WithArgs("ticket-hash", now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	completion, err := repo.CompleteLogin(CompletePortalLoginInput{
		SitePortalCode: "official",
		TicketHash:     "ticket-hash",
		ExternalUserID: "external-user-1",
		DisplayName:    "张老师",
		Now:            now,
	})
	if err != nil {
		t.Fatalf("CompleteLogin() error = %v", err)
	}
	if completion.CloudUserID != "cloud-user-existing" {
		t.Fatalf("CloudUserID = %q", completion.CloudUserID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletePortalLoginRejectsInactiveMappedUserWithoutConsumingTicket(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewExternalIdentityRepository(db)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT ticket.node_id").
		WithArgs("ticket-hash", "official").
		WillReturnRows(sqlmock.NewRows([]string{
			"node_id", "printer_id", "terminal_session_id", "selected_entry",
			"status", "expires_at", "claim_base_url", "enabled",
		}).AddRow("edge-1", "printer-1", "session-1", "official", "selected", now.Add(time.Minute),
			"https://portal.example.test", true))
	mock.ExpectQuery("SELECT identity.cloud_user_id").
		WithArgs("official", "external-user-1").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_user_id", "status"}).AddRow("cloud-user-1", "inactive"))
	mock.ExpectRollback()

	_, err := repo.CompleteLogin(CompletePortalLoginInput{
		SitePortalCode: "official",
		TicketHash:     "ticket-hash",
		ExternalUserID: "external-user-1",
		DisplayName:    "张老师",
		Now:            now,
	})
	if !errors.Is(err, ErrExternalIdentityDisabled) {
		t.Fatalf("CompleteLogin() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletePortalLoginRequiresEnabledEdgeAtConsumption(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewExternalIdentityRepository(db)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`JOIN edge_nodes node ON node.id=ticket.node_id
			AND node.deleted_at IS NULL AND node.enabled=true`)).
		WithArgs("ticket-hash", "official").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.CompleteLogin(CompletePortalLoginInput{
		SitePortalCode: "official",
		TicketHash:     "ticket-hash",
		ExternalUserID: "external-user-1",
		DisplayName:    "张老师",
		Now:            now,
	})
	if !errors.Is(err, ErrPortalLoginTicketInvalid) {
		t.Fatalf("CompleteLogin() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
