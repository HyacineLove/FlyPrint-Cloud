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
		"paper_size", "orientation", "scale_percent", "color_mode", "duplex_mode", "start_time", "end_time", "error_message",
		"error_code", "retry_count", "max_retries", "created_at", "updated_at", "printer_name",
		"node_name", "edge_node_id", "initiator_name",
		"site_portal_code", "quota_reserved", "quota_consumed",
		"impressions_completed", "sheets_completed",
	}).AddRow(
		"job-1", "document.pdf", "completed", "printer-1", "user-1", "Alice", "alice@example.com",
		"/data/document.pdf", "", "hash", int64(100), 2, 1, "A4", "portrait", 100, "color", "single",
		nil, now, "", nil, 0, 3, now, now, "Printer 1", "Node 1", "node-1", "Official Site Portal", "official",
		8, 8, 6, 4,
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

func TestPrintJobRepositoryGetByIDQualifiesJoinedTimestamps(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintJobRepository(&DB{DB: sqlDB})

	mock.ExpectQuery(`SELECT pj\.id[\s\S]*pj\.max_retries, pj\.created_at, pj\.updated_at[\s\S]*FROM print_jobs pj LEFT JOIN users`).
		WithArgs("job-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "status", "printer_id", "user_id", "user_name", "user_email",
			"file_path", "file_url", "content_hash", "file_size", "page_count", "copies",
			"paper_size", "orientation", "scale_percent", "color_mode", "duplex_mode", "start_time", "end_time", "error_message",
			"retry_count", "max_retries", "created_at", "updated_at",
		}).AddRow(
			"job-1", "document.pdf", "processing", "printer-1", nil, "", "",
			"/data/document.pdf", "", "hash", int64(100), 1, 1, "A4", "portrait", 100, "color", "single",
			nil, nil, "", 0, 3, time.Now(), time.Now(),
		))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT error_code FROM print_jobs WHERE id=$1")).
		WithArgs("job-1").
		WillReturnRows(sqlmock.NewRows([]string{"error_code"}).AddRow(nil))

	job, err := repo.GetPrintJobByID("job-1")
	if err != nil {
		t.Fatalf("GetPrintJobByID() error = %v", err)
	}
	if job == nil || job.ID != "job-1" {
		t.Fatalf("unexpected job: %#v", job)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintJobRepositoryGetByIDTreatsNullFileSizeAsZero(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintJobRepository(&DB{DB: sqlDB})

	mock.ExpectQuery(`SELECT pj\.id[\s\S]*COALESCE\(pj\.file_size, 0\)[\s\S]*FROM print_jobs pj LEFT JOIN users`).
		WithArgs("job-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "status", "printer_id", "user_id", "user_name", "user_email",
			"file_path", "file_url", "content_hash", "file_size", "page_count", "copies",
			"paper_size", "orientation", "scale_percent", "color_mode", "duplex_mode", "start_time", "end_time", "error_message",
			"retry_count", "max_retries", "created_at", "updated_at",
		}).AddRow(
			"job-1", "local.pdf", "processing", "printer-1", nil, "", "",
			"", "", "", int64(0), 1, 1, "A4", "portrait", 100, "color", "single",
			nil, nil, "", 0, 3, time.Now(), time.Now(),
		))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT error_code FROM print_jobs WHERE id=$1")).
		WithArgs("job-1").
		WillReturnRows(sqlmock.NewRows([]string{"error_code"}).AddRow(nil))

	job, err := repo.GetPrintJobByID("job-1")
	if err != nil {
		t.Fatalf("GetPrintJobByID() error = %v", err)
	}
	if job == nil || job.FileSize != 0 {
		t.Fatalf("unexpected job file size: %#v", job)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintJobRepositoryGetPendingOrDispatchedTreatsNullFileSizeAsZero(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintJobRepository(&DB{DB: sqlDB})

	mock.ExpectQuery(`SELECT pj\.id[\s\S]*COALESCE\(pj\.file_size, 0\)[\s\S]*FROM print_jobs pj`).
		WithArgs("node-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "status", "printer_id", "printer_name", "user_id", "user_name",
			"file_path", "file_url", "content_hash", "file_size", "page_count", "copies",
			"paper_size", "orientation", "scale_percent", "color_mode", "duplex_mode", "start_time", "end_time", "error_message",
			"retry_count", "max_retries", "created_at", "updated_at",
		}).AddRow(
			"job-1", "local.pdf", "dispatched", "printer-1", "Printer 1", nil, "",
			"", "", "", int64(0), 1, 1, "A4", "portrait", 100, "color", "single",
			nil, nil, "", 0, 3, time.Now(), time.Now(),
		))

	jobs, err := repo.GetPendingOrDispatchedJobsByEdgeNodeID("node-1")
	if err != nil {
		t.Fatalf("GetPendingOrDispatchedJobsByEdgeNodeID() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].FileSize != 0 {
		t.Fatalf("unexpected jobs: %#v", jobs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintJobRepositoryGetPendingJobsForRetryTreatsNullFileSizeAsZero(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintJobRepository(&DB{DB: sqlDB})

	mock.ExpectQuery(`SELECT pj\.id[\s\S]*COALESCE\(pj\.file_size, 0\)[\s\S]*FROM print_jobs pj`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "status", "printer_id", "printer_name", "edge_node_id", "user_id", "user_name",
			"file_path", "file_url", "content_hash", "file_size", "page_count", "copies",
			"paper_size", "orientation", "scale_percent", "color_mode", "duplex_mode", "start_time", "end_time", "error_message",
			"retry_count", "max_retries", "created_at", "updated_at",
		}).AddRow(
			"job-1", "local.pdf", "pending", "printer-1", "Printer 1", "node-1", nil, "",
			"", "", "", int64(0), 1, 1, "A4", "portrait", 100, "color", "single",
			nil, nil, "", 0, 3, time.Now(), time.Now(),
		))

	jobs, err := repo.GetPendingJobsForRetry(time.Minute)
	if err != nil {
		t.Fatalf("GetPendingJobsForRetry() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].FileSize != 0 {
		t.Fatalf("unexpected jobs: %#v", jobs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintJobRepositoryCountActiveJobsByEdgeNodeID(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintJobRepository(&DB{DB: sqlDB})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs("node-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	count, err := repo.CountActiveJobsByEdgeNodeID("node-1")
	if err != nil {
		t.Fatalf("CountActiveJobsByEdgeNodeID() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("count=%d, want 2", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintJobRepositoryListActiveJobRefsByPrinterID(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintJobRepository(&DB{DB: sqlDB})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pj.id::text,pj.printer_id::text,p.edge_node_id")).
		WithArgs("printer-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "printer_id", "edge_node_id"}).AddRow("job-1", "printer-1", "node-1"))

	refs, err := repo.ListActiveJobRefsByPrinterID("printer-1")
	if err != nil {
		t.Fatalf("ListActiveJobRefsByPrinterID() error = %v", err)
	}
	if len(refs) != 1 || refs[0].ID != "job-1" || refs[0].EdgeNodeID != "node-1" {
		t.Fatalf("unexpected refs: %#v", refs)
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
		"paper_size", "orientation", "scale_percent", "color_mode", "duplex_mode", "start_time", "end_time", "error_message",
		"error_code", "retry_count", "max_retries", "created_at", "updated_at", "printer_name",
		"node_name", "edge_node_id", "initiator_name",
		"site_portal_code", "quota_reserved", "quota_consumed",
		"impressions_completed", "sheets_completed",
	}).AddRow(
		"job-2", "legacy.pdf", "completed", "printer-1", "external-user", "Legacy User", "",
		"/data/legacy.pdf", "", "hash", int64(100), 1, 1, "A4", "portrait", 100, "grayscale", "single",
		nil, time.Now(), "", nil, 0, 3, time.Now(), time.Now(), "Printer 1", "Node 1", "node-1", "主系统", "",
		0, nil, nil, nil,
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
