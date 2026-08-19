//go:build integration

package database

import (
	"database/sql"
	"os"
	"testing"
)

func TestRemoveLegacyThirdPartyMigrationHandlesReferencedPrintJobs(t *testing.T) {
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
		t.Fatalf("initialize current schema: %v", err)
	}

	const fileID = "00000000-0000-0000-0000-000000000151"
	const jobID = "00000000-0000-0000-0000-000000000152"
	const requestID = "00000000-0000-0000-0000-000000000153"
	const eventID = "00000000-0000-0000-0000-000000000154"
	const nodeID = "legacy-migration-node"
	setup := `
		DELETE FROM schema_migrations WHERE version = '015_remove_legacy_third_party.sql';
		DROP TABLE IF EXISTS integration_print_requests CASCADE;
		CREATE TABLE integration_print_requests (
			id UUID PRIMARY KEY,
			file_id UUID REFERENCES files(id),
			print_job_id UUID REFERENCES print_jobs(id)
		);
		INSERT INTO files (id, original_name, file_name, file_path, mime_type, size, uploader_id)
		VALUES ('` + fileID + `', 'legacy.pdf', 'legacy.pdf', 'legacy.pdf', 'application/pdf', 1, 'legacy-user');
		INSERT INTO print_jobs (id, name, local_file_id)
		VALUES ('` + jobID + `', 'legacy third-party job', '` + fileID + `');
		INSERT INTO edge_nodes (id, name)
		VALUES ('` + nodeID + `', 'Legacy migration node')
		ON CONFLICT (id) DO NOTHING;
		INSERT INTO edge_job_update_receipts (event_id, node_id, job_id, status, payload_hash)
		VALUES ('` + eventID + `', '` + nodeID + `', '` + jobID + `', 'completed', repeat('0', 64));
		INSERT INTO integration_print_requests (id, file_id, print_job_id)
		VALUES ('` + requestID + `', '` + fileID + `', '` + jobID + `');
	`
	if _, err := sqlDB.Exec(setup); err != nil {
		t.Fatalf("create referenced legacy data: %v", err)
	}

	if err := db.InitTables(); err != nil {
		t.Fatalf("InitTables() upgrading referenced legacy data: %v", err)
	}

	var legacyTableExists bool
	if err := sqlDB.QueryRow(`SELECT to_regclass('public.integration_print_requests') IS NOT NULL`).Scan(&legacyTableExists); err != nil {
		t.Fatalf("check legacy table: %v", err)
	}
	if legacyTableExists {
		t.Fatal("integration_print_requests still exists after migration")
	}
	var receiptCount int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM edge_job_update_receipts WHERE event_id = $1`, eventID).Scan(&receiptCount); err != nil {
		t.Fatalf("check legacy job receipt cleanup: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("legacy job receipt count = %d, want 0", receiptCount)
	}

	for tableName, id := range map[string]string{"print_jobs": jobID, "files": fileID} {
		var count int
		if err := sqlDB.QueryRow(`SELECT count(*) FROM `+tableName+` WHERE id = $1`, id).Scan(&count); err != nil {
			t.Fatalf("check %s cleanup: %v", tableName, err)
		}
		if count != 0 {
			t.Fatalf("%s legacy row count = %d, want 0", tableName, count)
		}
	}
}
