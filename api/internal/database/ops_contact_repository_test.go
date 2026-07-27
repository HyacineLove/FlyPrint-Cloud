package database

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"fly-print-cloud/api/internal/models"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestOpsContactRepositoryListPublicForNode(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewOpsContactRepository(&DB{DB: sqlDB})

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT c.name, c.phone
		FROM node_ops_contacts n
		INNER JOIN ops_contacts c ON c.id = n.contact_id
		WHERE n.edge_node_id = $1
		  AND c.deleted_at IS NULL
		  AND c.enabled = TRUE
		ORDER BY c.name, c.phone`)).
		WithArgs("node-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "phone"}).
			AddRow("张三", "13800000000").
			AddRow("李四", "13900000000"))

	contacts, err := repo.ListPublicForNode("node-1")
	if err != nil {
		t.Fatalf("ListPublicForNode() error = %v", err)
	}
	if len(contacts) != 2 || contacts[0].Name != "张三" || contacts[1].Phone != "13900000000" {
		t.Fatalf("unexpected contacts: %#v", contacts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOpsContactRepositoryReplaceNodeBindingsRejectsLimit(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewOpsContactRepository(&DB{DB: sqlDB})

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM ops_contacts WHERE id = $1 AND deleted_at IS NULL)`)).
		WithArgs("contact-1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM node_ops_contacts WHERE contact_id = $1`)).
		WithArgs("contact-1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM edge_nodes WHERE id = $1 AND deleted_at IS NULL)`)).
		WithArgs("node-1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`
			SELECT COUNT(*)
			FROM node_ops_contacts n
			INNER JOIN ops_contacts c ON c.id = n.contact_id
			WHERE n.edge_node_id = $1
			  AND c.deleted_at IS NULL`)).
		WithArgs("node-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	mock.ExpectRollback()

	err = repo.ReplaceNodeBindings("contact-1", []string{"node-1"}, 5)
	if !errors.Is(err, ErrNodeContactLimitExceeded) {
		t.Fatalf("ReplaceNodeBindings() error = %v, want limit exceeded", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOpsContactRepositorySoftDeleteClearsBindings(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewOpsContactRepository(&DB{DB: sqlDB})

	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE ops_contacts
		SET deleted_at = CURRENT_TIMESTAMP, enabled = FALSE, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs("contact-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM node_ops_contacts WHERE contact_id = $1`)).
		WithArgs("contact-1").
		WillReturnResult(sqlmock.NewResult(0, 2))

	if err := repo.SoftDelete("contact-1"); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOpsContactRepositoryClearBindingsForNodeTx(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewOpsContactRepository(&DB{DB: sqlDB})

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM node_ops_contacts WHERE edge_node_id = $1`)).
		WithArgs("node-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	tx, err := (&DB{DB: sqlDB}).BeginTx()
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if err := repo.ClearBindingsForNodeTx(tx, "node-1"); err != nil {
		t.Fatalf("ClearBindingsForNodeTx() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOpsContactRepositoryCreate(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	repo := NewOpsContactRepository(&DB{DB: sqlDB})
	contact := &models.OpsContact{Name: "张三", Phone: "13800000000", Enabled: true}
	now := time.Unix(100, 0).UTC()

	mock.ExpectQuery(regexp.QuoteMeta(`
		INSERT INTO ops_contacts(name, phone, enabled)
		VALUES($1, $2, $3)
		RETURNING id, created_at, updated_at`)).
		WithArgs("张三", "13800000000", true).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("contact-1", now, now))

	if err := repo.Create(contact); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if contact.ID != "contact-1" {
		t.Fatalf("contact.ID = %q", contact.ID)
	}
}
