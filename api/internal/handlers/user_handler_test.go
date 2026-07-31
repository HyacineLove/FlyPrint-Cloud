package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"fly-print-cloud/api/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

func newUserHandlerTestContext(method, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "user-2"}}
	return c, recorder
}

func TestUserHandlerDeleteUsesExternalIDForCurrentAdmin(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM users WHERE id = $1 FOR UPDATE`)).
		WithArgs("user-2").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("user-2"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM print_jobs WHERE user_id = $1 AND status IN`)).
		WithArgs("user-2").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM external_identities WHERE cloud_user_id = $1`)).
		WithArgs("user-2").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM operational_alerts
		WHERE job_id IN (SELECT id FROM print_jobs WHERE user_id = $1)`)).
		WithArgs("user-2").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM print_jobs WHERE user_id = $1`)).
		WithArgs("user-2").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM users WHERE id = $1`)).
		WithArgs("user-2").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	c, recorder := newUserHandlerTestContext(http.MethodDelete, "/admin/users/user-2", "")
	c.Set("external_id", "admin-1")
	NewUserHandler(database.NewUserRepository(&database.DB{DB: sqlDB})).DeleteUser(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserHandlerDeleteSelfUsesExternalID(t *testing.T) {
	c, recorder := newUserHandlerTestContext(http.MethodDelete, "/admin/users/admin-1", "")
	c.Params = gin.Params{{Key: "id", Value: "admin-1"}}
	c.Set("external_id", "admin-1")
	NewUserHandler(nil).DeleteUser(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestUserHandlerUpdateRejectsEmailChange(t *testing.T) {
	c, recorder := newUserHandlerTestContext(http.MethodPut, "/admin/users/user-2", `{"email":"new@example.com","username":"Alice","role":"viewer"}`)
	NewUserHandler(nil).UpdateUser(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestUserHandlerUpdateEnabledDisablesUser(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`
		UPDATE users
		SET status = CASE WHEN $2 THEN 'active' ELSE 'inactive' END
		WHERE id = $1
		RETURNING id, username, email, role, status, print_quota_balance, last_login, created_at, updated_at`)).
		WithArgs("user-2", false).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email", "role", "status", "print_quota_balance", "last_login", "created_at", "updated_at"}).
			AddRow("user-2", "Alice", "alice@example.com", "viewer", "inactive", 50, nil, now, now))

	c, recorder := newUserHandlerTestContext(http.MethodPatch, "/admin/users/user-2/enabled", `{"enabled":false}`)
	NewUserHandler(database.NewUserRepository(&database.DB{DB: sqlDB})).UpdateEnabled(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserHandlerDeleteRejectsActivePrintJobs(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM users WHERE id = $1 FOR UPDATE`)).
		WithArgs("user-2").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("user-2"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM print_jobs WHERE user_id = $1 AND status IN`)).
		WithArgs("user-2").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()

	c, recorder := newUserHandlerTestContext(http.MethodDelete, "/admin/users/user-2", "")
	c.Set("external_id", "admin-1")
	NewUserHandler(database.NewUserRepository(&database.DB{DB: sqlDB})).DeleteUser(c)

	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "用户存在打印中的任务，无法删除") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserHandlerGrantPrintQuotaRejectsNonPositiveAmount(t *testing.T) {
	c, recorder := newUserHandlerTestContext(
		http.MethodPost,
		"/admin/users/user-2/print-quota-grants",
		`{"amount":0,"reason":"demo allowance"}`,
	)
	c.Set("external_id", "admin-1")
	NewUserHandler(nil).GrantPrintQuota(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestUserHandlerGrantPrintQuotaReturnsUpdatedBalance(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	now := time.Now()
	userID := "11111111-1111-1111-1111-111111111111"
	adminID := "22222222-2222-2222-2222-222222222222"

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id::text FROM users").
		WithArgs(adminID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(adminID))
	mock.ExpectQuery("UPDATE users").
		WithArgs(userID, 20).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "username", "email", "role", "status", "print_quota_balance",
			"last_login", "created_at", "updated_at",
		}).AddRow(userID, "Alice", "alice@example.com", "viewer", "active", 70, nil, now, now))
	mock.ExpectExec("INSERT INTO print_quota_transactions").
		WithArgs(userID, 20, 70, adminID, "demo allowance").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	c, recorder := newUserHandlerTestContext(
		http.MethodPost,
		"/admin/users/"+userID+"/print-quota-grants",
		`{"amount":20,"reason":"demo allowance"}`,
	)
	c.Params = gin.Params{{Key: "id", Value: userID}}
	c.Set("external_id", adminID)
	NewUserHandler(
		nil,
		database.NewPrintQuotaRepository(&database.DB{DB: sqlDB}),
	).GrantPrintQuota(c)

	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"print_quota_balance":70`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
