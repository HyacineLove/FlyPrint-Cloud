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
