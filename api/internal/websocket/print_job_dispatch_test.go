package websocket

import (
	"strings"
	"testing"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/operations"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDispatchFailureInvokesIntegrationFailureHook(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	databaseHandle := &database.DB{DB: db}
	statusService := operations.NewStatusService(databaseHandle, database.NewOperationalAlertRepository(databaseHandle))
	job := &models.PrintJob{ID: "job-1", PrinterID: "printer-1"}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT j.status").WithArgs(job.ID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "error_code", "user_id", "quota_reserved", "quota_consumed", "printer_id", "edge_node_id"}).
			AddRow("pending", "", "", 0, nil, job.PrinterID, "node-1"))
	mock.ExpectExec("UPDATE print_jobs SET").
		WithArgs(job.ID, "failed", "dispatch_rejected", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	called := false
	err = dispatchPrintJobAndRecordWithDispatch(nil, nil, statusService, job, "node-1",
		func() error { return ErrAckRejected },
		DispatchHooks{AfterFailure: func(code, message string) error {
			called = code == "dispatch_rejected" && message != ""
			return nil
		}},
	)
	if err != nil {
		t.Fatalf("dispatch helper error = %v", err)
	}
	if !called {
		t.Fatal("expected integration failure hook")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchFailureRequiresStatusService(t *testing.T) {
	job := &models.PrintJob{ID: "job-1", PrinterID: "printer-1"}
	err := dispatchPrintJobAndRecordWithDispatch(nil, nil, nil, job, "node-1",
		func() error { return ErrAckRejected },
		DispatchHooks{},
	)
	if err == nil || !strings.Contains(err.Error(), "status service unavailable") {
		t.Fatalf("error = %v, want status service error", err)
	}
}

func TestDispatchTimeoutInvokesIntegrationFailureHook(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	databaseHandle := &database.DB{DB: db}
	statusService := operations.NewStatusService(databaseHandle, database.NewOperationalAlertRepository(databaseHandle))
	job := &models.PrintJob{ID: "job-1", PrinterID: "printer-1"}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE print_jobs SET status='unconfirmed'").
		WithArgs(job.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO operational_alerts").
		WithArgs("printer", job.PrinterID, "node-1", job.PrinterID, job.ID, "printer_unconfirmed_lock", "job", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	var gotCode, gotMessage string
	err = dispatchPrintJobAndRecordWithDispatch(nil, nil, statusService, job, "node-1",
		func() error { return ErrAckTimeout },
		DispatchHooks{AfterFailure: func(code, message string) error {
			gotCode, gotMessage = code, message
			return nil
		}},
	)
	if err != nil {
		t.Fatalf("dispatch helper error = %v", err)
	}
	if gotCode != "dispatch_ack_timeout" || gotMessage == "" {
		t.Fatalf("failure hook = (%q, %q), want timeout transition", gotCode, gotMessage)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchTimeoutDoesNotFailIntegrationWhenJobAlreadyAdvanced(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	databaseHandle := &database.DB{DB: db}
	statusService := operations.NewStatusService(databaseHandle, database.NewOperationalAlertRepository(databaseHandle))
	job := &models.PrintJob{ID: "job-1", PrinterID: "printer-1"}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE print_jobs SET status='unconfirmed'").
		WithArgs(job.ID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	called := false
	err = dispatchPrintJobAndRecordWithDispatch(nil, nil, statusService, job, "node-1",
		func() error { return ErrAckTimeout },
		DispatchHooks{AfterFailure: func(string, string) error {
			called = true
			return nil
		}},
	)
	if err != nil {
		t.Fatalf("dispatch helper error = %v", err)
	}
	if called {
		t.Fatal("timeout after a non-pending job must not fail integration request")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
