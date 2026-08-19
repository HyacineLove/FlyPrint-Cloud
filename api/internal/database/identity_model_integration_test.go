//go:build integration

package database

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func openIdentityModelTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("FLYPRINT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FLYPRINT_TEST_POSTGRES_DSN is not configured")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestExternalIdentityModelConstraints(t *testing.T) {
	db := openIdentityModelTestDB(t)
	if err := (&DB{DB: db}).InitTables(); err != nil {
		t.Fatalf("InitTables(): %v", err)
	}

	var incomplete int
	if err := db.QueryRow(`SELECT COUNT(*) FROM external_identities
		WHERE identity_connector_id='' OR issuer='' OR subject=''`).Scan(&incomplete); err != nil {
		t.Fatalf("check external identity principals: %v", err)
	}
	if incomplete != 0 {
		t.Fatalf("external identities without a complete principal = %d", incomplete)
	}

	username := "invalid-external-admin-" + uuid.NewString()
	_, err := db.Exec(`INSERT INTO users(username,email,password_hash,account_kind,role,status)
		VALUES($1,$2,'unusable','external','admin','active')`, username, username+"@example.test")
	if err == nil {
		t.Fatal("external account with an admin role was accepted")
	}
}

func TestExternalIdentityMigrationBackfillsLegacyRows(t *testing.T) {
	db := openIdentityModelTestDB(t)
	current := &DB{DB: db}
	if err := current.InitTables(); err != nil {
		t.Fatalf("initial InitTables(): %v", err)
	}
	// If an assertion interrupts the test after the legacy schema is staged,
	// make a best effort to restore the current schema for the remaining tests.
	t.Cleanup(func() { _ = current.InitTables() })

	suffix := uuid.NewString()
	portalCode := "migration-" + suffix
	username := "migration-" + suffix
	externalID := "subject-" + suffix
	var userID string
	if _, err := db.Exec(`INSERT INTO site_portals(code,display_name,entry_url,claim_base_url,enabled)
		VALUES($1,'Migration Portal','https://portal.example.test/entry','https://portal.example.test',true)`, portalCode); err != nil {
		t.Fatalf("insert portal: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO users(username,email,password_hash,account_kind,role,status)
		VALUES($1,$2,'unusable','operator','viewer','active') RETURNING id::text`, username, username+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert legacy user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO external_identities
		(site_portal_code,external_user_id,identity_connector_id,issuer,subject,cloud_user_id,display_name,last_login_at)
		VALUES($1,$2,$3,$4,$2,$5,'Migration User',$6)`, portalCode, externalID,
		"site-portal:"+portalCode, "urn:flyprint:site-portal:"+portalCode, userID, time.Now().UTC()); err != nil {
		t.Fatalf("insert legacy identity: %v", err)
	}

	if _, err := db.Exec(`
		DELETE FROM schema_migrations WHERE version='020_external_identity_principals.sql';
		DROP INDEX IF EXISTS idx_external_identities_principal;
		ALTER TABLE users DROP CONSTRAINT IF EXISTS users_external_role_check;
		ALTER TABLE users DROP CONSTRAINT IF EXISTS users_account_kind_check;
		ALTER TABLE external_identities DROP COLUMN identity_connector_id, DROP COLUMN issuer, DROP COLUMN subject;
		ALTER TABLE users DROP COLUMN account_kind;
	`); err != nil {
		t.Fatalf("stage legacy schema: %v", err)
	}
	if err := current.InitTables(); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}

	var accountKind, connectorID, issuer, subject string
	if err := db.QueryRow(`SELECT user_account.account_kind,identity.identity_connector_id,identity.issuer,identity.subject
		FROM users user_account JOIN external_identities identity ON identity.cloud_user_id=user_account.id
		WHERE user_account.id=$1::uuid`, userID).Scan(&accountKind, &connectorID, &issuer, &subject); err != nil {
		t.Fatalf("read migrated identity: %v", err)
	}
	if accountKind != "external" || connectorID != "site-portal:"+portalCode ||
		issuer != "urn:flyprint:site-portal:"+portalCode || subject != externalID {
		t.Fatalf("unexpected migrated identity: kind=%q connector=%q issuer=%q subject=%q", accountKind, connectorID, issuer, subject)
	}
}
