package database

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAcquireT1AllowsOnlyFirstCaller(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewEntrySessionRepository(&DB{DB: sqlDB})
	now := time.Unix(100, 0)
	expires := now.Add(time.Minute)
	columns := []string{"id", "t1_hash", "acquire_hash", "t2_hash", "node_id", "printer_id", "terminal_session_id", "qr_generation", "status", "mask_command_id", "mask_confirmed_at", "portal_attempt_version", "issued_at", "expires_at"}
	query := regexp.QuoteMeta(`UPDATE entry_sessions SET status='mask_pending',acquire_hash=$2,mask_command_id=$3::uuid
		WHERE t1_hash=$1 AND status='qr_issued' AND expires_at>$4
		RETURNING id,t1_hash,COALESCE(acquire_hash,''),COALESCE(t2_hash,''),node_id,printer_id::text,terminal_session_id,qr_generation,status,
		COALESCE(mask_command_id::text,''),mask_confirmed_at,portal_attempt_version,issued_at,expires_at`)
	mock.ExpectQuery(query).WithArgs("t1hash", "lease-one", "00000000-0000-0000-0000-000000000001", now).
		WillReturnRows(sqlmock.NewRows(columns).AddRow("entry-1", "t1hash", "lease-one", "", "node-1", "printer-1", "session-1", 1, "mask_pending", "00000000-0000-0000-0000-000000000001", nil, 0, now, expires))
	first, err := repo.Acquire("t1hash", "lease-one", "00000000-0000-0000-0000-000000000001", now)
	if err != nil || first == nil || first.ID != "entry-1" {
		t.Fatalf("first acquire = %#v, %v", first, err)
	}
	mock.ExpectQuery(query).WithArgs("t1hash", "lease-two", "00000000-0000-0000-0000-000000000002", now).WillReturnRows(sqlmock.NewRows(columns))
	if _, err = repo.Acquire("t1hash", "lease-two", "00000000-0000-0000-0000-000000000002", now); err != ErrEntrySessionInvalid {
		t.Fatalf("second acquire error = %v, want ErrEntrySessionInvalid", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEntryLookupsBindEntrySessionID(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewEntrySessionRepository(&DB{DB: sqlDB})
	now := time.Unix(100, 0)
	expires := now.Add(time.Minute)
	columns := []string{"id", "t1_hash", "acquire_hash", "t2_hash", "node_id", "printer_id", "terminal_session_id", "qr_generation", "status", "mask_command_id", "mask_confirmed_at", "portal_attempt_version", "issued_at", "expires_at"}
	row := func(status string) *sqlmock.Rows {
		return sqlmock.NewRows(columns).AddRow("entry-1", "t1hash", "lease-one", "t2hash", "node-1", "printer-1", "session-1", 1, status, "00000000-0000-0000-0000-000000000001", nil, 0, now, expires)
	}

	acquireQuery := regexp.QuoteMeta(`SELECT id,t1_hash,COALESCE(acquire_hash,''),COALESCE(t2_hash,''),node_id,printer_id::text,terminal_session_id,qr_generation,status,
		COALESCE(mask_command_id::text,''),mask_confirmed_at,portal_attempt_version,issued_at,expires_at
		FROM entry_sessions WHERE acquire_hash=$1 AND id=$2::uuid AND status='mask_pending' AND expires_at>$3`)
	mock.ExpectQuery(acquireQuery).WithArgs("lease-one", "entry-1", now).WillReturnRows(row("mask_pending"))
	entry, err := repo.GetByAcquire("lease-one", "entry-1", now)
	if err != nil || entry == nil || entry.ID != "entry-1" {
		t.Fatalf("acquire lookup = %#v, %v", entry, err)
	}
	mock.ExpectQuery(acquireQuery).WithArgs("lease-one", "entry-2", now).WillReturnRows(sqlmock.NewRows(columns))
	if _, err := repo.GetByAcquire("lease-one", "entry-2", now); err != ErrEntrySessionInvalid {
		t.Fatalf("mismatched acquire lookup error = %v", err)
	}

	t2Query := regexp.QuoteMeta(`SELECT id,t1_hash,COALESCE(acquire_hash,''),COALESCE(t2_hash,''),node_id,printer_id::text,terminal_session_id,qr_generation,status,
		COALESCE(mask_command_id::text,''),mask_confirmed_at,portal_attempt_version,issued_at,expires_at
		FROM entry_sessions WHERE t2_hash=$1 AND id=$2::uuid AND status='entry_active' AND expires_at>$3`)
	mock.ExpectQuery(t2Query).WithArgs("t2hash", "entry-1", now).WillReturnRows(row("entry_active"))
	entry, err = repo.GetActiveByT2("t2hash", "entry-1", now)
	if err != nil || entry == nil || entry.ID != "entry-1" {
		t.Fatalf("T2 lookup = %#v, %v", entry, err)
	}
	mock.ExpectQuery(t2Query).WithArgs("t2hash", "entry-2", now).WillReturnRows(sqlmock.NewRows(columns))
	if _, err := repo.GetActiveByT2("t2hash", "entry-2", now); err != ErrEntrySessionInvalid {
		t.Fatalf("mismatched T2 lookup error = %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
