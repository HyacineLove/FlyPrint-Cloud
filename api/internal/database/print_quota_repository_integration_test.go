package database

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestPrintQuotaRepositoryGrantOnFreshSchema 使用真实 PostgreSQL 覆盖额度授予，
// 防止 SQL Mock 忽略不存在的 users 字段。
func TestPrintQuotaRepositoryGrantOnFreshSchema(t *testing.T) {
	dsn := os.Getenv("FLYPRINT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FLYPRINT_TEST_POSTGRES_DSN is not configured")
	}

	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer sqlDB.Close()

	db := &DB{DB: sqlDB}
	if err := db.InitTables(); err != nil {
		t.Fatalf("initialize schema: %v", err)
	}

	suffix := time.Now().UnixNano()
	adminID := insertQuotaTestUser(t, sqlDB, fmt.Sprintf("quota-admin-%d", suffix), "admin")
	userID := insertQuotaTestUser(t, sqlDB, fmt.Sprintf("quota-user-%d", suffix), "viewer")

	user, err := NewPrintQuotaRepository(db).Grant(userID, adminID, 20, "现场测试补充额度")
	if err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	if user.PrintQuotaBalance != 20 {
		t.Fatalf("PrintQuotaBalance = %d, want 20", user.PrintQuotaBalance)
	}

	var transactionCount int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM print_quota_transactions
		 WHERE user_id=$1::uuid AND admin_user_id=$2::uuid
		   AND transaction_type='admin_grant' AND delta=20`,
		userID,
		adminID,
	).Scan(&transactionCount); err != nil {
		t.Fatalf("query quota ledger: %v", err)
	}
	if transactionCount != 1 {
		t.Fatalf("admin grant ledger count = %d, want 1", transactionCount)
	}
}

func insertQuotaTestUser(t *testing.T, db *sql.DB, username, role string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(
		`INSERT INTO users (username,email,password_hash,role,status)
		 VALUES ($1,$2,'test-hash',$3,'active') RETURNING id::text`,
		username,
		username+"@example.test",
		role,
	).Scan(&id); err != nil {
		t.Fatalf("insert %s user: %v", role, err)
	}
	return id
}
