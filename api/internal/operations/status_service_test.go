package operations

import (
	"testing"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestValidatePrinterDispatchUsesOrderedFacts(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-10 * time.Second)
	node := &models.EdgeNode{Enabled: true, ConnectionStatus: "online"}
	printer := &models.Printer{Enabled: true, PrinterStatus: "idle", StatusReceivedAt: &fresh}

	if got := ValidatePrinterDispatch(printer, node, now); got != "" {
		t.Fatalf("idle printer should accept a task: got %q", got)
	}
	printer.PrinterStatus = "printing"
	if got := ValidatePrinterDispatch(printer, node, now); got != "printer_busy" {
		t.Fatalf("processing printer: got %q", got)
	}
	printer.PrinterStatus = "printer_out_of_paper"
	if got := ValidatePrinterDispatch(printer, node, now); got != "printer_out_of_paper" {
		t.Fatalf("fault must be returned directly: got %q", got)
	}
	printer.PrinterStatus = "idle"
	old := now.Add(-91 * time.Second)
	printer.StatusReceivedAt = &old
	if got := ValidatePrinterDispatch(printer, node, now); got != "printer_status_stale" || !PrinterStatusStale(printer, now) {
		t.Fatalf("stale printer: got %q", got)
	}
	printer.StatusReceivedAt = &fresh
	node.ConnectionStatus = "unstable"
	if got := ValidatePrinterDispatch(printer, node, now); got != "node_offline" {
		t.Fatalf("unstable node: got %q", got)
	}
	node.ConnectionStatus = "offline"
	if got := ValidatePrinterDispatch(printer, node, now); got != "node_offline" {
		t.Fatalf("offline node: got %q", got)
	}
}

func TestAlertPolicyActivationDelays(t *testing.T) {
	now := time.Now()
	tests := []struct {
		reason string
		age    time.Duration
		ready  bool
	}{
		{"printer_out_of_paper", 0, true},
		{"printer_not_accepting_jobs", 59 * time.Second, false},
		{"printer_not_accepting_jobs", 60 * time.Second, true},
	}
	for _, test := range tests {
		policy, ok := alertPolicy(test.reason)
		if !ok {
			t.Fatalf("missing policy for %s", test.reason)
		}
		if got := policyReady(policy, now.Add(-test.age), now); got != test.ready {
			t.Fatalf("%s age=%s: got ready=%v", test.reason, test.age, got)
		}
	}
}

func TestAttentionReasonsDoNotCreatePolicies(t *testing.T) {
	for _, reason := range []string{"printer_warning", "printer_toner_low", "node_connection_unstable", "libreoffice_unavailable"} {
		if _, ok := alertPolicy(reason); ok {
			t.Fatalf("attention reason %q must not create an operational alert", reason)
		}
	}
}

func TestNormalPrinterStatusesDoNotCreateAlerts(t *testing.T) {
	for _, status := range []string{"idle", "printing"} {
		if _, ok := alertPolicy(status); ok {
			t.Fatalf("normal printer status %q must not be an alert reason", status)
		}
	}
}

func TestConnectionScopedReasonsOnlyContainConnectivityFailures(t *testing.T) {
	reasons := map[string]bool{}
	for _, reason := range connectionScopedPrinterReasons() {
		reasons[reason] = true
	}
	if !reasons["ipp_unreachable"] {
		t.Fatal("IPP connectivity failures must be suppressible under node offline")
	}
	if _, ok := alertPolicy("printer_offline"); ok {
		t.Fatal("offline state must not be treated as a printer alert")
	}
	if _, ok := alertPolicy("printer_state_unknown"); ok {
		t.Fatal("unknown state must not be treated as a printer alert")
	}
	if reasons["printer_out_of_paper"] || reasons["printer_jammed"] {
		t.Fatal("physical faults must remain visible under node offline")
	}
}

func TestOfflineStatesDoNotCreateAlertPolicies(t *testing.T) {
	for _, reason := range []string{"node_offline", "printer_offline", "printer_state_unknown"} {
		if _, ok := alertPolicy(reason); ok {
			t.Fatalf("offline state %q must not create an operational alert", reason)
		}
	}
}

func TestApplyJobResultRejectsUnknownStatusBeforeDatabaseWrite(t *testing.T) {
	service := &StatusService{}
	if err := service.ApplyJobResult("job", "node", "printer", "printing", "", nil); err == nil {
		t.Fatal("legacy printing status must not enter the Cloud state model")
	}
}

func TestApplyJobResultRefundsReservedQuotaWhenDispatchFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	databaseHandle := &database.DB{DB: db}
	service := NewStatusService(databaseHandle, database.NewOperationalAlertRepository(databaseHandle))

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT j.status").WithArgs("job-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "error_code", "user_id", "quota_reserved", "quota_consumed", "printer_id", "edge_node_id"}).
			AddRow("pending", "", "user-1", 8, nil, "printer-1", "node-1"))
	mock.ExpectExec("UPDATE print_jobs SET").
		WithArgs("job-1", "failed", "dispatch_failed", "dispatch failed").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("UPDATE users").WithArgs("user-1", 8).
		WillReturnRows(sqlmock.NewRows([]string{"print_quota_balance"}).AddRow(50))
	mock.ExpectExec("INSERT INTO print_quota_transactions").
		WithArgs("user-1", "job-1", 8, 50).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := service.ApplyJobResult("job-1", "node-1", "printer-1", "failed", "dispatch_failed", map[string]interface{}{"message": "dispatch failed"}); err != nil {
		t.Fatalf("ApplyJobResult() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupStaleJobsUsesStatusServiceSettlement(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	databaseHandle := &database.DB{DB: db}
	service := NewStatusService(databaseHandle, database.NewOperationalAlertRepository(databaseHandle))
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT pj.id::text").
		WithArgs(now.Add(-30 * time.Minute)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "edge_node_id", "printer_id"}).AddRow("job-1", "node-1", "printer-1"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT j.status").WithArgs("job-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "error_code", "user_id", "quota_reserved", "quota_consumed", "printer_id", "edge_node_id"}).
			AddRow("dispatched", "", "user-1", 8, nil, "printer-1", "node-1"))
	mock.ExpectExec("UPDATE print_jobs SET").
		WithArgs("job-1", "failed", "print_timeout_failed", "Edge node did not report status").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("UPDATE users").WithArgs("user-1", 8).
		WillReturnRows(sqlmock.NewRows([]string{"print_quota_balance"}).AddRow(50))
	mock.ExpectExec("INSERT INTO print_quota_transactions").
		WithArgs("user-1", "job-1", 8, 50).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if _, err := service.CleanupStaleJobs(now, 30*time.Minute); err != nil {
		t.Fatalf("CleanupStaleJobs() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyDispatchUnconfirmedReportsNoChangeWhenJobWasAlreadyAccepted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	service := NewStatusService(&database.DB{DB: db}, database.NewOperationalAlertRepository(&database.DB{DB: db}))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE print_jobs SET status='unconfirmed'").
		WithArgs("job-1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	changed, err := service.ApplyDispatchUnconfirmed("job-1", "node-1", "printer-1")
	if err != nil {
		t.Fatalf("ApplyDispatchUnconfirmed() error = %v", err)
	}
	if changed {
		t.Fatal("expected no transition when job was already accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyTerminalJobUpdatePersistsResultAndReceiptTogether(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	service := &StatusService{db: &database.DB{DB: db}}
	receipts := database.NewEdgeJobUpdateReceiptRepository(&database.DB{DB: db})
	update := TerminalJobUpdate{EventID: "event-1", PayloadHash: "hash-1", NodeID: "node-1", JobID: "job-1", Status: "completed"}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_id").WithArgs(update.EventID).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "node_id", "job_id", "status", "payload_hash"}))
	mock.ExpectQuery("SELECT j.status").WithArgs(update.JobID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "error_code", "printer_id", "edge_node_id", "user_id",
			"quota_reserved", "quota_consumed", "impressions_completed", "sheets_completed", "color_mode",
			"page_count", "copies", "duplex_mode",
		}).AddRow("processing", "", "printer-1", update.NodeID, "", 0, nil, nil, nil, "mono", 1, 1, "simplex"))
	mock.ExpectExec("UPDATE print_jobs").WithArgs(update.JobID, update.Status, update.ErrorCode, update.ErrorMessage).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO edge_job_update_receipts").
		WithArgs(update.EventID, update.NodeID, update.JobID, update.Status, update.PayloadHash).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := service.ApplyTerminalJobUpdate(receipts, update)
	if err != nil {
		t.Fatalf("ApplyTerminalJobUpdate() error = %v", err)
	}
	if !result.Accepted || !result.Changed {
		t.Fatalf("unexpected terminal result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyTerminalJobUpdateSettlesFailedPortalJobAndRefundsUnusedQuota(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	service := &StatusService{db: &database.DB{DB: db}}
	receipts := database.NewEdgeJobUpdateReceiptRepository(&database.DB{DB: db})
	update := TerminalJobUpdate{
		EventID: "event-1", PayloadHash: "hash-1", NodeID: "node-1", JobID: "job-1",
		Status: "failed", ImpressionsCompleted: 4, SheetsCompleted: 3, QuotaConsumed: 6,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_id").WithArgs(update.EventID).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "node_id", "job_id", "status", "payload_hash"}))
	mock.ExpectQuery("SELECT j.status").WithArgs(update.JobID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "error_code", "printer_id", "edge_node_id", "user_id",
			"quota_reserved", "quota_consumed", "impressions_completed", "sheets_completed", "color_mode",
			"page_count", "copies", "duplex_mode",
		}).AddRow("processing", "", "printer-1", update.NodeID, "user-1", 8, nil, nil, nil, "color", 3, 2, "longedge"))
	mock.ExpectExec("UPDATE print_jobs").
		WithArgs(update.JobID, update.Status, update.ErrorCode, update.ErrorMessage).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE print_jobs SET").
		WithArgs(update.JobID, update.ImpressionsCompleted, update.SheetsCompleted, 6).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("UPDATE users").WithArgs("user-1", 2).
		WillReturnRows(sqlmock.NewRows([]string{"print_quota_balance"}).AddRow(44))
	mock.ExpectExec("INSERT INTO print_quota_transactions").
		WithArgs("user-1", update.JobID, 2, 44).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO edge_job_update_receipts").
		WithArgs(update.EventID, update.NodeID, update.JobID, update.Status, update.PayloadHash).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := service.ApplyTerminalJobUpdate(receipts, update)
	if err != nil {
		t.Fatalf("ApplyTerminalJobUpdate() error = %v", err)
	}
	if !result.Accepted || !result.Changed {
		t.Fatalf("unexpected terminal result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyTerminalJobUpdateKeepsReservationWhenResultIsUnconfirmed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	databaseHandle := &database.DB{DB: db}
	service := NewStatusService(databaseHandle, database.NewOperationalAlertRepository(databaseHandle))
	receipts := database.NewEdgeJobUpdateReceiptRepository(databaseHandle)
	update := TerminalJobUpdate{
		EventID: "event-1", PayloadHash: "hash-1", NodeID: "node-1", JobID: "job-1",
		Status: "unconfirmed",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_id").WithArgs(update.EventID).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "node_id", "job_id", "status", "payload_hash"}))
	mock.ExpectQuery("SELECT j.status").WithArgs(update.JobID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "error_code", "printer_id", "edge_node_id", "user_id",
			"quota_reserved", "quota_consumed", "impressions_completed", "sheets_completed", "color_mode",
			"page_count", "copies", "duplex_mode",
		}).AddRow("processing", "", "printer-1", update.NodeID, "user-1", 8, nil, nil, nil, "color", 3, 2, "longedge"))
	mock.ExpectExec("UPDATE print_jobs").
		WithArgs(update.JobID, update.Status, update.ErrorCode, update.ErrorMessage).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO operational_alerts").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO edge_job_update_receipts").
		WithArgs(update.EventID, update.NodeID, update.JobID, update.Status, update.PayloadHash).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := service.ApplyTerminalJobUpdate(receipts, update)
	if err != nil {
		t.Fatalf("ApplyTerminalJobUpdate() error = %v", err)
	}
	if !result.Accepted || !result.Changed {
		t.Fatalf("unexpected terminal result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyTerminalJobUpdateRefundsFailedPortalJobFromReportedUsage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	service := &StatusService{db: &database.DB{DB: db}}
	receipts := database.NewEdgeJobUpdateReceiptRepository(&database.DB{DB: db})
	update := TerminalJobUpdate{
		EventID: "event-1", PayloadHash: "hash-1", NodeID: "node-1", JobID: "job-1",
		Status: "failed", ErrorCode: "ipp_submit_failed",
		ImpressionsCompleted: 0, SheetsCompleted: 0,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_id").WithArgs(update.EventID).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "node_id", "job_id", "status", "payload_hash"}))
	mock.ExpectQuery("SELECT j.status").WithArgs(update.JobID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "error_code", "printer_id", "edge_node_id", "user_id",
			"quota_reserved", "quota_consumed", "impressions_completed", "sheets_completed", "color_mode",
			"page_count", "copies", "duplex_mode",
		}).AddRow("processing", "", "printer-1", update.NodeID, "user-1", 8, nil, nil, nil, "color", 3, 2, "longedge"))
	mock.ExpectExec("UPDATE print_jobs").
		WithArgs(update.JobID, update.Status, update.ErrorCode, update.ErrorMessage).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE print_jobs SET").
		WithArgs(update.JobID, 0, 0, 0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("UPDATE users").WithArgs("user-1", 8).
		WillReturnRows(sqlmock.NewRows([]string{"print_quota_balance"}).AddRow(50))
	mock.ExpectExec("INSERT INTO print_quota_transactions").
		WithArgs("user-1", update.JobID, 8, 50).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO edge_job_update_receipts").
		WithArgs(update.EventID, update.NodeID, update.JobID, update.Status, update.PayloadHash).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := service.ApplyTerminalJobUpdate(receipts, update)
	if err != nil {
		t.Fatalf("ApplyTerminalJobUpdate() error = %v", err)
	}
	if !result.Accepted || !result.Changed {
		t.Fatalf("unexpected terminal result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyTerminalJobUpdateRejectsOtherNodeBeforeChangingTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	service := &StatusService{db: &database.DB{DB: db}}
	receipts := database.NewEdgeJobUpdateReceiptRepository(&database.DB{DB: db})
	update := TerminalJobUpdate{EventID: "event-1", PayloadHash: "hash-1", NodeID: "node-1", JobID: "job-1", Status: "failed"}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_id").WithArgs(update.EventID).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "node_id", "job_id", "status", "payload_hash"}))
	mock.ExpectQuery("SELECT j.status").WithArgs(update.JobID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "error_code", "printer_id", "edge_node_id", "user_id",
			"quota_reserved", "quota_consumed", "impressions_completed", "sheets_completed", "color_mode",
			"page_count", "copies", "duplex_mode",
		}).AddRow("processing", "", "printer-1", "node-2", "", 0, nil, nil, nil, "mono", 1, 1, "simplex"))
	mock.ExpectRollback()

	result, err := service.ApplyTerminalJobUpdate(receipts, update)
	if err != nil {
		t.Fatalf("ApplyTerminalJobUpdate() error = %v", err)
	}
	if result.Accepted || result.Reason != "job_node_mismatch" {
		t.Fatalf("unexpected terminal result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyTerminalJobUpdateAcceptsAnIdenticalRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	service := &StatusService{db: &database.DB{DB: db}}
	receipts := database.NewEdgeJobUpdateReceiptRepository(&database.DB{DB: db})
	update := TerminalJobUpdate{EventID: "event-1", PayloadHash: "hash-1", NodeID: "node-1", JobID: "job-1", Status: "completed"}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_id").WithArgs(update.EventID).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "node_id", "job_id", "status", "payload_hash"}).
			AddRow(update.EventID, update.NodeID, update.JobID, update.Status, update.PayloadHash))
	mock.ExpectRollback()

	result, err := service.ApplyTerminalJobUpdate(receipts, update)
	if err != nil {
		t.Fatalf("ApplyTerminalJobUpdate() error = %v", err)
	}
	if !result.Accepted || result.Changed {
		t.Fatalf("unexpected terminal result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyTerminalJobUpdateRejectsReusedEventWithDifferentPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	service := &StatusService{db: &database.DB{DB: db}}
	receipts := database.NewEdgeJobUpdateReceiptRepository(&database.DB{DB: db})
	update := TerminalJobUpdate{EventID: "event-1", PayloadHash: "new-hash", NodeID: "node-1", JobID: "job-1", Status: "completed"}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT event_id").WithArgs(update.EventID).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "node_id", "job_id", "status", "payload_hash"}).
			AddRow(update.EventID, update.NodeID, update.JobID, update.Status, "old-hash"))
	mock.ExpectRollback()

	result, err := service.ApplyTerminalJobUpdate(receipts, update)
	if err != nil {
		t.Fatalf("ApplyTerminalJobUpdate() error = %v", err)
	}
	if result.Accepted || result.Reason != "event_id_conflict" {
		t.Fatalf("unexpected terminal result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
