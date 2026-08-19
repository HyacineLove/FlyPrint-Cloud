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
		"site_portal_providers",
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
	for _, tableName := range []string{"integration_providers", "integration_print_requests", "integration_callback_events"} {
		var exists bool
		if err := sqlDB.QueryRow(`SELECT to_regclass('public.' || $1) IS NOT NULL`, tableName).Scan(&exists); err != nil {
			t.Fatalf("check removed table %s: %v", tableName, err)
		}
		if exists {
			t.Fatalf("legacy table %s still exists", tableName)
		}
	}
	var legacyColumn bool
	if err := sqlDB.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='public' AND table_name='edge_terminal_sessions' AND column_name='integration_request_id'
	)`).Scan(&legacyColumn); err != nil {
		t.Fatalf("check removed terminal session column: %v", err)
	}
	if legacyColumn {
		t.Fatal("legacy integration_request_id column still exists")
	}

	for _, column := range []struct {
		table string
		name  string
	}{
		{table: "users", name: "account_kind"},
		{table: "external_identities", name: "identity_connector_id"},
		{table: "external_identities", name: "issuer"},
		{table: "external_identities", name: "subject"},
	} {
		var exists bool
		if err := sqlDB.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1 AND column_name=$2
		)`, column.table, column.name).Scan(&exists); err != nil {
			t.Fatalf("check column %s.%s: %v", column.table, column.name, err)
		}
		if !exists {
			t.Fatalf("column %s.%s was not created", column.table, column.name)
		}
	}
}
