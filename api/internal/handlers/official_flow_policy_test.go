package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"fly-print-cloud/api/internal/auth"
	"fly-print-cloud/api/internal/config"
	"fly-print-cloud/api/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func TestOfficialRegistrationRoleIsFixedToViewer(t *testing.T) {
	if got := officialRegistrationRole(); got != "viewer" {
		t.Fatalf("official registration role = %q, want viewer", got)
	}
}

func TestTerminalLoginSourceValidation(t *testing.T) {
	tests := []struct {
		source string
		valid  bool
	}{
		{source: "official", valid: true},
		{source: "livacloud-demo", valid: true},
		{source: "", valid: false},
		{source: "not a provider", valid: false},
		{source: "../provider", valid: false},
	}

	for _, tt := range tests {
		if got := isValidTerminalLoginSource(tt.source); got != tt.valid {
			t.Errorf("isValidTerminalLoginSource(%q) = %v, want %v", tt.source, got, tt.valid)
		}
	}
}

func TestOfficialRegistrationCreatesViewerAndReturnsToken(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()
	db := &database.DB{DB: sqlDB}
	userRepo := database.NewUserRepository(db)
	builtin := auth.NewBuiltinAuthService(nil, userRepo, &config.OAuth2Config{JWTSigningSecret: "test-signing-secret", JWTTokenExpiry: 3600, JWTIssuer: "test-issuer"})
	handler := &OAuth2Handler{mode: "builtin", builtinAuth: builtin, userRepo: userRepo}

	emailExists := mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM users WHERE LOWER(email) = LOWER($1)"))
	emailExists.WithArgs("alice@example.com").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	createUser := mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO users (username, email, password_hash, role, status)"))
	createUser.WithArgs(sqlmock.AnyArg(), "alice@example.com", sqlmock.AnyArg(), "viewer", "active").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow("user-1", time.Now(), time.Now()))

	hash, err := bcrypt.GenerateFromPassword([]byte("StrongPass1"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword() error = %v", err)
	}
	getUser := mock.ExpectQuery(regexp.QuoteMeta("SELECT id, username, email, password_hash, role, status, last_login, created_at, updated_at"))
	getUser.WithArgs("alice@example.com").WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email", "password_hash", "role", "status", "last_login", "created_at", "updated_at"}).AddRow("user-1", "internal-user", "alice@example.com", string(hash), "viewer", "active", nil, time.Now(), time.Now()))
	lastLogin := mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET last_login = CURRENT_TIMESTAMP WHERE id = $1"))
	lastLogin.WithArgs("user-1").WillReturnResult(sqlmock.NewResult(0, 1))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"Alice@Example.com","password":"StrongPass1"}`))
	context.Request.Header.Set("Content-Type", "application/json")
	handler.Register(context)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("Register() status = %d, want %d, body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"scope":"file:read"`) {
		t.Fatalf("Register() response missing viewer scope: %s", recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}
