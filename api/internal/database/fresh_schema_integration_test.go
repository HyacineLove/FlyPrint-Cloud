//go:build integration

package database

import (
	"database/sql"
	"os"
	"testing"
)

// TestInitTablesOnFreshDatabase 覆盖全新交付环境，防止迁移表尚未创建时提前执行扩展 ALTER。
func TestInitTablesOnFreshDatabase(t *testing.T) {
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
		t.Fatalf("InitTables() on fresh database: %v", err)
	}

	for _, tableName := range []string{
		"edge_terminal_sessions",
		"site_portals",
		"print_quota_transactions",
	} {
		var exists bool
		if err := sqlDB.QueryRow(
			`SELECT to_regclass('public.' || $1) IS NOT NULL`,
			tableName,
		).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", tableName, err)
		}
		if !exists {
			t.Fatalf("table %s was not created", tableName)
		}
	}
}
