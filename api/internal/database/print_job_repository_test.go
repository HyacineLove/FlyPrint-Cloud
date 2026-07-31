package database

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func printJobRows() *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"id", "name", "status", "printer_id", "user_id", "user_name", "user_email",
		"file_path", "file_url", "content_hash", "file_size", "page_count", "copies",
		"paper_size", "color_mode", "duplex_mode", "start_time", "end_time", "error_message",
		"error_code", "retry_count", "max_retries", "created_at", "updated_at", "printer_name",
		"node_name", "edge_node_id", "initiator_name", "initiator_code",
		"site_portal_code", "quota_reserved", "quota_consumed",
		"impressions_completed", "sheets_completed",
	}).AddRow(
		"job-1", "document.pdf", "completed", "printer-1", "user-1", "Alice", "alice@example.com",
		"/data/document.pdf", "", "hash", int64(100), 2, 1, "A4", "color", "single",
		nil, now, "", nil, 0, 3, now, now, "Printer 1", "Node 1", "node-1", "Official Site Portal", "official",
		"official", 8, 8, 6, 4,
	)
}

func TestPrintJobRepositoryListIncludesUserEmailAndFiltersByEmail(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintJobRepository(&DB{DB: sqlDB})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT pj.id")).
		WithArgs("alice@example.com", 20).
		WillReturnRows(printJobRows())

	jobs, err := repo.ListPrintJobs(20, 0, "", "", "", "alice@example.com", "", "", nil, nil)
	if err != nil {
		t.Fatalf("ListPrintJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].UserEmail != "alice@example.com" || jobs[0].UserName != "Alice" {
		t.Fatalf("unexpected user fields: %#v", jobs)
	}
	if jobs[0].SitePortalCode != "official" || jobs[0].QuotaReserved != 8 ||
		jobs[0].QuotaConsumed == nil || *jobs[0].QuotaConsumed != 8 {
		t.Fatalf("unexpected unified audit fields: %#v", jobs[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintJobRepositoryListKeepsSnapshotNameWithoutMatchedUser(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintJobRepository(&DB{DB: sqlDB})

	rows := printJobRows()
	rows = sqlmock.NewRows([]string{
		"id", "name", "status", "printer_id", "user_id", "user_name", "user_email",
		"file_path", "file_url", "content_hash", "file_size", "page_count", "copies",
		"paper_size", "color_mode", "duplex_mode", "start_time", "end_time", "error_message",
		"error_code", "retry_count", "max_retries", "created_at", "updated_at", "printer_name",
		"node_name", "edge_node_id", "initiator_name", "initiator_code",
		"site_portal_code", "quota_reserved", "quota_consumed",
		"impressions_completed", "sheets_completed",
	}).AddRow(
		"job-2", "legacy.pdf", "completed", "printer-1", "external-user", "Legacy User", "",
		"/data/legacy.pdf", "", "hash", int64(100), 1, 1, "A4", "grayscale", "single",
		nil, time.Now(), "", nil, 0, 3, time.Now(), time.Now(), "Printer 1", "Node 1", "node-1", "主系统", "",
		"", 0, nil, nil, nil,
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pj.id")).
		WithArgs(20).
		WillReturnRows(rows)

	jobs, err := repo.ListPrintJobs(20, 0, "", "", "", "", "", "", nil, nil)
	if err != nil {
		t.Fatalf("ListPrintJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].UserEmail != "" || jobs[0].UserName != "Legacy User" {
		t.Fatalf("unexpected legacy user fields: %#v", jobs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintJobRepositoryListTreatsFilelessPortalJobSizeAsZero(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintJobRepository(&DB{DB: sqlDB})

	rows := printJobRows()
	mock.ExpectQuery(`SELECT pj\.id[\s\S]*COALESCE\(pj\.file_size, 0\)`).
		WithArgs(20).
		WillReturnRows(rows)

	jobs, err := repo.ListPrintJobs(20, 0, "", "", "", "", "", "", nil, nil)
	if err != nil {
		t.Fatalf("ListPrintJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].FileSize != 100 {
		t.Fatalf("unexpected print jobs: %#v", jobs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
