package database

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"fly-print-cloud/api/internal/models"

	"github.com/DATA-DOG/go-sqlmock"
)

func authorizationInput() models.PrintAuthorizationInput {
	return models.PrintAuthorizationInput{
		NodeID:            "edge-1",
		ConfirmationID:    "confirm-1",
		TerminalSessionID: "session-1",
		SitePortalCode:    "official",
		LocalFileID:       "prp-file-1",
		FileDisplayName:   "课件.pdf",
		PageCount:         3,
		Copies:            2,
		PaperSize:         "A4",
		ColorMode:         "color",
		DuplexMode:        "longedge",
		PrinterID:         "11111111-1111-1111-1111-111111111111",
		Now:               time.Date(2026, 7, 31, 5, 0, 0, 0, time.UTC),
	}
}

func expectAuthorizationIdentity(
	mock sqlmock.Sqlmock,
	input models.PrintAuthorizationInput,
	balance int,
	status string,
) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT session.site_portal_code,session.cloud_user_id::text,
		user_account.status,user_account.print_quota_balance,
		portal.enabled,
		COALESCE(NULLIF(identity.display_name,''),NULLIF(user_account.username,''),'')
		FROM edge_terminal_sessions session
		JOIN users user_account ON user_account.id=session.cloud_user_id
		JOIN site_portals portal ON portal.code=session.site_portal_code
		LEFT JOIN external_identities identity
			ON identity.site_portal_code=session.site_portal_code
			AND identity.cloud_user_id=session.cloud_user_id
		WHERE session.node_id=$1 AND session.terminal_session_id=$2
		FOR UPDATE OF session,user_account`)).
		WithArgs(input.NodeID, input.TerminalSessionID).
		WillReturnRows(sqlmock.NewRows([]string{
			"site_portal_code", "cloud_user_id", "status", "print_quota_balance", "enabled", "display_name",
		}).AddRow("official", "22222222-2222-2222-2222-222222222222", status, balance, true, "演示用户"))
}

func expectNoExistingAuthorization(mock sqlmock.Sqlmock, input models.PrintAuthorizationInput) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id::text,authorization_request_hash,quota_reserved
		FROM print_jobs
		WHERE edge_node_id=$1 AND confirmation_id=$2
		FOR UPDATE`)).
		WithArgs(input.NodeID, input.ConfirmationID).
		WillReturnError(sql.ErrNoRows)
}

func expectAvailableAuthorizationPrinter(mock sqlmock.Sqlmock, input models.PrintAuthorizationInput) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT printer.edge_node_id,printer.enabled,printer.status,
		printer.status_received_at,node.enabled,node.status,printer.capabilities
		FROM printers printer
		JOIN edge_nodes node ON node.id=printer.edge_node_id
		WHERE printer.id=$1::uuid
			AND printer.deleted_at IS NULL
			AND node.deleted_at IS NULL
		FOR UPDATE OF printer,node`)).
		WithArgs(input.PrinterID).
		WillReturnRows(sqlmock.NewRows([]string{
			"edge_node_id", "printer_enabled", "printer_status",
			"status_received_at", "node_enabled", "node_status", "capabilities",
		}).AddRow(
			input.NodeID, true, "idle", input.Now.Add(-time.Second), true, "online",
			[]byte(`{"paper_sizes":["A4"],"color_support":true,"duplex_support":true}`),
		))
}

func TestPrintAuthorizationRepositoryCreatesFilelessAuditAndReservesQuota(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewPrintAuthorizationRepository(db)
	input := authorizationInput()

	mock.ExpectBegin()
	expectAuthorizationIdentity(mock, input, 50, "active")
	expectNoExistingAuthorization(mock, input)
	expectAvailableAuthorizationPrinter(mock, input)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE users
		SET print_quota_balance=print_quota_balance-$2
		WHERE id=$1::uuid AND print_quota_balance>=$2`)).
		WithArgs("22222222-2222-2222-2222-222222222222", 8).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO print_jobs (
		name,status,printer_id,user_id,user_name,file_path,file_url,content_hash,
		page_count,copies,paper_size,color_mode,duplex_mode,retry_count,max_retries,
		edge_node_id,site_portal_code,terminal_session_id,confirmation_id,
		authorization_request_hash,local_file_id,quota_reserved,created_at,updated_at
	) VALUES (
		$1,'pending',$2::uuid,$3,$4,'','','',
		$5,$6,$7,$8,$9,0,0,
		$10,$11,$12,$13,$14,$15,$16,$17,$17
	) RETURNING id::text`)).
		WithArgs(
			"课件.pdf",
			input.PrinterID,
			"22222222-2222-2222-2222-222222222222",
			"演示用户",
			3,
			2,
			"A4",
			"color",
			"longedge",
			input.NodeID,
			"official",
			input.TerminalSessionID,
			input.ConfirmationID,
			sqlmock.AnyArg(),
			input.LocalFileID,
			8,
			input.Now,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).
			AddRow("33333333-3333-3333-3333-333333333333"))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO print_quota_transactions
		(user_id,print_job_id,transaction_type,delta,balance_after)
		VALUES ($1::uuid,$2::uuid,'authorization_reserve',$3,$4)`)).
		WithArgs(
			"22222222-2222-2222-2222-222222222222",
			"33333333-3333-3333-3333-333333333333",
			-8,
			42,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := repo.Authorize(input)
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if result.JobID != "33333333-3333-3333-3333-333333333333" ||
		result.ReservedQuota != 8 || result.QuotaBalance != 42 {
		t.Fatalf("Authorize() = %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintAuthorizationRepositoryRejectsInsufficientQuotaWithoutJob(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewPrintAuthorizationRepository(db)
	input := authorizationInput()

	mock.ExpectBegin()
	expectAuthorizationIdentity(mock, input, 7, "active")
	expectNoExistingAuthorization(mock, input)
	expectAvailableAuthorizationPrinter(mock, input)
	mock.ExpectRollback()

	_, err := repo.Authorize(input)
	if !errors.Is(err, ErrPrintQuotaInsufficient) {
		t.Fatalf("Authorize() error = %v, want ErrPrintQuotaInsufficient", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintAuthorizationRepositoryReturnsExistingMatchingConfirmation(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewPrintAuthorizationRepository(db)
	input := authorizationInput()
	requestHash := PrintAuthorizationRequestHash(input)

	mock.ExpectBegin()
	expectAuthorizationIdentity(mock, input, 42, "active")
	mock.ExpectQuery("SELECT id::text,authorization_request_hash,quota_reserved").
		WithArgs(input.NodeID, input.ConfirmationID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "authorization_request_hash", "quota_reserved",
		}).AddRow("33333333-3333-3333-3333-333333333333", requestHash, 8))
	mock.ExpectCommit()

	result, err := repo.Authorize(input)
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if result.JobID != "33333333-3333-3333-3333-333333333333" ||
		result.ReservedQuota != 8 || result.QuotaBalance != 42 {
		t.Fatalf("Authorize() = %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintAuthorizationRepositoryRejectsConflictingConfirmation(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewPrintAuthorizationRepository(db)
	input := authorizationInput()

	mock.ExpectBegin()
	expectAuthorizationIdentity(mock, input, 42, "active")
	mock.ExpectQuery("SELECT id::text,authorization_request_hash,quota_reserved").
		WithArgs(input.NodeID, input.ConfirmationID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "authorization_request_hash", "quota_reserved",
		}).AddRow("33333333-3333-3333-3333-333333333333", "different-request-hash", 8))
	mock.ExpectRollback()

	_, err := repo.Authorize(input)
	if !errors.Is(err, ErrPrintAuthorizationConflict) {
		t.Fatalf("Authorize() error = %v, want ErrPrintAuthorizationConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
