package database

import (
	"regexp"
	"strings"
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
		portal.enabled,portal.created_at,portal.updated_at
		FROM edge_nodes node
		JOIN edge_site_portals assignment ON assignment.edge_node_id=node.id AND assignment.is_default=true
		JOIN site_portals portal ON portal.code=assignment.site_portal_code
		WHERE node.id=$1 AND node.deleted_at IS NULL AND node.enabled=true AND portal.enabled=true`)).
		WithArgs("edge-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"code", "display_name", "entry_url", "claim_base_url",
			"enabled", "created_at", "updated_at",
		}).AddRow("official", "FlyPrint", "https://portal.example.test/entry",
			"https://portal.example.test", true, now, now))

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

func TestGetSitePortalByCodeReturnsEnabledPortal(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewSitePortalRepository(db)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT code,display_name,entry_url,claim_base_url,enabled,created_at,updated_at
		FROM site_portals WHERE code=$1 AND enabled=true`)).
		WithArgs("official").
		WillReturnRows(sqlmock.NewRows([]string{
			"code", "display_name", "entry_url", "claim_base_url",
			"enabled", "created_at", "updated_at",
		}).AddRow("official", "FlyPrint", "https://portal.example.test/entry",
			"https://portal.example.test", true, now, now))

	if _, err := repo.GetByCode("official"); err != nil {
		t.Fatalf("GetByCode() returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSitePortalRemovesOAuthClientWhenUnreferenced(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewSitePortalRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM external_identities WHERE site_portal_code=$1`)).
		WithArgs("portal-a").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM edge_site_portals WHERE site_portal_code=$1`)).
		WithArgs("portal-a").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM oauth2_clients WHERE client_type='site_portal' AND site_portal_code=$1`)).
		WithArgs("portal-a").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM site_portals WHERE code=$1`)).
		WithArgs("portal-a").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.Delete("portal-a"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizePortalCodesDeduplicatesAndSorts(t *testing.T) {
	codes, err := normalizePortalCodes([]string{" portal-b ", "portal-a", "portal-b"})
	if err != nil {
		t.Fatalf("normalizePortalCodes() error = %v", err)
	}
	if got, want := strings.Join(codes, ","), "portal-a,portal-b"; got != want {
		t.Fatalf("normalizePortalCodes() = %q, want %q", got, want)
	}
}

func TestReplaceEdgeSitePortalsUpdatesDefaultAndAssignmentsAtomically(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewSitePortalRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM edge_nodes WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`)).
		WithArgs("edge-1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("edge-1"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT code,enabled FROM site_portals WHERE code=ANY($1) FOR SHARE`)).
		WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"code", "enabled"}).
		AddRow("portal-a", true).AddRow("portal-b", true))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM edge_site_portals WHERE edge_node_id=$1`)).
		WithArgs("edge-1").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO edge_site_portals(edge_node_id,site_portal_code,is_default) VALUES($1,$2,$3)`)).
		WithArgs("edge-1", "portal-a", true).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO edge_site_portals(edge_node_id,site_portal_code,is_default) VALUES($1,$2,$3)`)).
		WithArgs("edge-1", "portal-b", false).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.ReplaceEdgeSitePortals("edge-1", []string{"portal-b", "portal-a", "portal-a"}, "portal-a"); err != nil {
		t.Fatalf("ReplaceEdgeSitePortals() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceEdgeSitePortalsRejectsDefaultOutsideAssignment(t *testing.T) {
	repo := &SitePortalRepository{}
	if err := repo.ReplaceEdgeSitePortals("edge-1", []string{"portal-a"}, "portal-b"); err != ErrDefaultPortalNotAssigned {
		t.Fatalf("ReplaceEdgeSitePortals() error = %v, want ErrDefaultPortalNotAssigned", err)
	}
}

func TestDeleteSitePortalRejectsIdentityMappings(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewSitePortalRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM external_identities WHERE site_portal_code=$1`)).
		WithArgs("portal-a").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()

	if err := repo.Delete("portal-a"); err != ErrSitePortalHasMappings {
		t.Fatalf("Delete() error = %v, want ErrSitePortalHasMappings", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListEnabledSitePortalProvidersReturnsRevisionAndStableOrder(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewSitePortalRepository(db)
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT provider_config_revision FROM site_portals WHERE code=$1 AND enabled=true`)).
		WithArgs("official").WillReturnRows(sqlmock.NewRows([]string{"provider_config_revision"}).AddRow(4))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT site_portal_code,provider_id,display_name,enabled,sort_order,file_base_url,sign_secret_ref,
		portal_api_base_url,upload_enabled,updated_at
		FROM site_portal_providers WHERE site_portal_code=$1 AND enabled=true ORDER BY sort_order,provider_id`)).
		WithArgs("official").WillReturnRows(sqlmock.NewRows([]string{"site_portal_code", "provider_id", "display_name", "enabled", "sort_order", "file_base_url", "sign_secret_ref", "portal_api_base_url", "upload_enabled", "updated_at"}).
		AddRow("official", "invoice", "发票", true, 1, "https://files.example.test", "INVOICE", nil, false, now))
	providers, revision, err := repo.ListEnabledProviders("official")
	if err != nil || revision != 4 || len(providers) != 1 || providers[0].ProviderID != "invoice" {
		t.Fatalf("providers=%#v revision=%d err=%v", providers, revision, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSetSitePortalProviderEnabledBumpsPortalRevisionAtomically(t *testing.T) {
	db, mock, closeDB := newUserRepositoryTestDB(t)
	defer closeDB()
	repo := NewSitePortalRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT code FROM site_portals WHERE code=$1 FOR UPDATE`)).
		WithArgs("official").WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow("official"))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE site_portal_providers SET enabled=$3,updated_at=CURRENT_TIMESTAMP WHERE site_portal_code=$1 AND provider_id=$2`)).
		WithArgs("official", "invoice", false).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE site_portals SET provider_config_revision=provider_config_revision+1,updated_at=CURRENT_TIMESTAMP WHERE code=$1`)).
		WithArgs("official").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repo.SetProviderEnabled("official", "invoice", false); err != nil {
		t.Fatalf("SetProviderEnabled() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
