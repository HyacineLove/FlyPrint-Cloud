package database

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"fly-print-cloud/api/internal/models"

	"github.com/DATA-DOG/go-sqlmock"
)

func newUserRepositoryTestDB(t *testing.T) (*DB, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	return &DB{DB: sqlDB}, mock, func() { _ = sqlDB.Close() }
}

func TestUserRepositoryListUsersIncludesInactiveAndAppliesFilters(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewUserRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM users")).
		WithArgs("%alice%", "viewer", "inactive").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, username, email, role, status, last_login, created_at, updated_at")).
		WithArgs("%alice%", "viewer", "inactive", 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email", "role", "status", "last_login", "created_at", "updated_at"}).
			AddRow("u-1", "alice", "alice@example.com", "viewer", "inactive", nil, time.Now(), time.Now()))

	users, total, err := repo.ListUsers(UserListFilter{
		Search: "alice", Role: "viewer", Status: "inactive", SortBy: "email", SortOrder: "asc", Limit: 20,
	})
	if err != nil || total != 1 || len(users) != 1 || users[0].Status != "inactive" {
		t.Fatalf("ListUsers() = users=%v total=%d err=%v", users, total, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserRepositoryUpdateUserLeavesEmailAndStatusUnchanged(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewUserRepository(db)
	user := &models.User{ID: "u-1", Username: "new-name", Email: "alice@example.com", Role: "operator", Status: "inactive"}

	mock.ExpectQuery(regexp.QuoteMeta(`
		UPDATE users
		SET username = $2, role = $3
		WHERE id = $1
		RETURNING updated_at`)).
		WithArgs("u-1", "new-name", "operator").
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))

	if err := repo.UpdateUser(user); err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserRepositoryUpdateEnabledReturnsInactiveUser(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewUserRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta(`
		UPDATE users
		SET status = CASE WHEN $2 THEN 'active' ELSE 'inactive' END
		WHERE id = $1
		RETURNING id, username, email, role, status, last_login, created_at, updated_at`)).
		WithArgs("u-1", false).
		WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email", "role", "status", "last_login", "created_at", "updated_at"}).
			AddRow("u-1", "alice", "alice@example.com", "viewer", "inactive", nil, time.Now(), time.Now()))

	user, err := repo.UpdateEnabled("u-1", false)
	if err != nil || user == nil || user.Status != "inactive" {
		t.Fatalf("UpdateEnabled() = user=%v err=%v", user, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteUserWithPrintJobsRejectsActiveJobsAndRollsBack(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewUserRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM users WHERE id = $1 FOR UPDATE`)).
		WithArgs("u-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("u-1"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM print_jobs WHERE user_id = $1 AND status IN`)).
		WithArgs("u-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()

	err := repo.DeleteUserWithPrintJobs("u-1")
	if !errors.Is(err, ErrUserHasActivePrintJobs) {
		t.Fatalf("DeleteUserWithPrintJobs() error = %v, want active-job error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
