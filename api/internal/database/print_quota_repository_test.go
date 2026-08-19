package database

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPrintQuotaRepositoryGrantUpdatesBalanceAndWritesAdminLedger(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewPrintQuotaRepository(&DB{DB: sqlDB})
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id::text FROM users").
		WithArgs("22222222-2222-2222-2222-222222222222").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).
			AddRow("22222222-2222-2222-2222-222222222222"))
	mock.ExpectQuery(regexp.QuoteMeta(`UPDATE users
		SET print_quota_balance=print_quota_balance+$2
		WHERE id=$1::uuid
		RETURNING id::text,username,email,account_kind,role,status,print_quota_balance,
			last_login,created_at,updated_at`)).
		WithArgs("11111111-1111-1111-1111-111111111111", 20).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "username", "email", "account_kind", "role", "status", "print_quota_balance",
			"last_login", "created_at", "updated_at",
		}).AddRow(
			"11111111-1111-1111-1111-111111111111", "Alice", "alice@example.com",
			"external", "viewer", "active", 70, nil, now, now,
		))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO print_quota_transactions
		(user_id,transaction_type,delta,balance_after,admin_user_id,reason)
		VALUES ($1::uuid,'admin_grant',$2,$3,$4::uuid,$5)`)).
		WithArgs(
			"11111111-1111-1111-1111-111111111111", 20, 70,
			"22222222-2222-2222-2222-222222222222", "demo allowance",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	user, err := repo.Grant(
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		20,
		"demo allowance",
	)
	if err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	if user.PrintQuotaBalance != 70 {
		t.Fatalf("PrintQuotaBalance = %d, want 70", user.PrintQuotaBalance)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrintQuotaRepositoryGrantRejectsNonPositiveAmountBeforeDatabase(t *testing.T) {
	repo := NewPrintQuotaRepository(nil)
	if _, err := repo.Grant("user", "admin", 0, "invalid"); err != ErrPrintQuotaGrantInvalid {
		t.Fatalf("Grant() error = %v, want ErrPrintQuotaGrantInvalid", err)
	}
}
