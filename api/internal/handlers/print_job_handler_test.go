package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"fly-print-cloud/api/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

func TestPrintJobHandlerListPassesUserEmailFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := database.NewPrintJobRepository(&database.DB{DB: sqlDB})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM print_jobs pj")).
		WithArgs("alice@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pj.id")).
		WithArgs("alice@example.com", 20).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "status", "printer_id", "user_id", "user_name", "user_email",
			"file_path", "file_url", "content_hash", "file_size", "page_count", "copies",
			"paper_size", "color_mode", "duplex_mode", "start_time", "end_time", "error_message",
			"error_code", "retry_count", "max_retries", "created_at", "updated_at", "printer_name",
			"node_name", "edge_node_id", "initiator_name", "initiator_code",
		}).AddRow(
			"job-1", "document.pdf", "completed", "printer-1", "user-1", "Alice", "alice@example.com",
			"/data/document.pdf", "", "hash", int64(100), 2, 1, "A4", "color", "single",
			nil, time.Now(), "", nil, 0, 3, time.Now(), time.Now(), "Printer 1", "Node 1", "node-1", "主系统", "",
		))

	handler := NewPrintJobHandler(repo, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/admin/print-jobs", handler.ListPrintJobs)
	req := httptest.NewRequest(http.MethodGet, "/admin/print-jobs?user_email=alice%40example.com", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
