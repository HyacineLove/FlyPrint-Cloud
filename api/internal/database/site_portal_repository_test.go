package database

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetDefaultSitePortalForNodeReturnsEnabledPortal(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewSitePortalRepository(db)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT portal.code,portal.display_name,portal.entry_url,portal.claim_base_url,
		portal.api_token_hash,portal.enabled,portal.created_at,portal.updated_at
		FROM edge_nodes node
		JOIN site_portals portal ON portal.code=node.default_site_portal_code
		WHERE node.id=$1 AND node.deleted_at IS NULL AND node.enabled=true AND portal.enabled=true`)).
		WithArgs("edge-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"code", "display_name", "entry_url", "claim_base_url",
			"api_token_hash", "enabled", "created_at", "updated_at",
		}).AddRow("official", "FlyPrint", "https://portal.example.test/entry",
			"https://portal.example.test", hashSitePortalToken("portal-token"), true, now, now))

	portal, err := repo.GetDefaultForNode("edge-1")
	if err != nil {
		t.Fatalf("GetDefaultForNode() error = %v", err)
	}
	if portal.Code != "official" || portal.EntryURL != "https://portal.example.test/entry" {
		t.Fatalf("unexpected portal: %#v", portal)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSitePortalAuthenticationComparesHashedToken(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewSitePortalRepository(db)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT code,display_name,entry_url,claim_base_url,api_token_hash,enabled,created_at,updated_at
		FROM site_portals WHERE code=$1 AND enabled=true`)).
		WithArgs("official").
		WillReturnRows(sqlmock.NewRows([]string{
			"code", "display_name", "entry_url", "claim_base_url",
			"api_token_hash", "enabled", "created_at", "updated_at",
		}).AddRow("official", "FlyPrint", "https://portal.example.test/entry",
			"https://portal.example.test", hashSitePortalToken("portal-token"), true, now, now))

	if _, err := repo.Authenticate("official", "wrong-token"); err == nil {
		t.Fatal("Authenticate() should reject a wrong raw token")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
