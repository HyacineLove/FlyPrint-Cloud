package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"fly-print-cloud/api/internal/models"
)

var (
	ErrOpsContactNotFound         = errors.New("ops contact not found")
	ErrNodeContactLimitExceeded   = errors.New("node ops contact limit exceeded")
	ErrOpsContactNodeNotFound     = errors.New("edge node not found")
)

// OpsContactRepository persists display-only ops contact profiles and node bindings.
type OpsContactRepository struct {
	db *DB
}

func NewOpsContactRepository(db *DB) *OpsContactRepository {
	return &OpsContactRepository{db: db}
}

type OpsContactListFilter struct {
	Search  string
	NodeID  string
	Enabled *bool
	Offset  int
	Limit   int
}

func (r *OpsContactRepository) List(filter OpsContactListFilter) ([]*models.OpsContact, int, error) {
	where := []string{"c.deleted_at IS NULL"}
	args := []interface{}{}
	argIndex := 1

	if filter.Search != "" {
		where = append(where, fmt.Sprintf("(c.name ILIKE $%d OR c.phone ILIKE $%d)", argIndex, argIndex))
		args = append(args, "%"+filter.Search+"%")
		argIndex++
	}
	if filter.Enabled != nil {
		where = append(where, fmt.Sprintf("c.enabled = $%d", argIndex))
		args = append(args, *filter.Enabled)
		argIndex++
	}
	if filter.NodeID != "" {
		where = append(where, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM node_ops_contacts n
			WHERE n.contact_id = c.id AND n.edge_node_id = $%d
		)`, argIndex))
		args = append(args, filter.NodeID)
		argIndex++
	}

	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM ops_contacts c WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count ops contacts: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 10
	}
	query := fmt.Sprintf(`
		SELECT c.id, c.name, c.phone, c.enabled, c.created_at, c.updated_at
		FROM ops_contacts c
		WHERE %s
		ORDER BY c.updated_at DESC, c.created_at DESC
		LIMIT $%d OFFSET $%d`, whereSQL, argIndex, argIndex+1)
	args = append(args, limit, filter.Offset)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list ops contacts: %w", err)
	}
	defer rows.Close()

	contacts := make([]*models.OpsContact, 0)
	for rows.Next() {
		contact := &models.OpsContact{}
		if err := rows.Scan(&contact.ID, &contact.Name, &contact.Phone, &contact.Enabled, &contact.CreatedAt, &contact.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan ops contact: %w", err)
		}
		contacts = append(contacts, contact)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	for _, contact := range contacts {
		nodeIDs, err := r.ListNodeIDs(contact.ID)
		if err != nil {
			return nil, 0, err
		}
		contact.NodeIDs = nodeIDs
	}
	return contacts, total, nil
}

func (r *OpsContactRepository) Get(id string) (*models.OpsContact, error) {
	contact := &models.OpsContact{}
	err := r.db.QueryRow(`
		SELECT id, name, phone, enabled, created_at, updated_at
		FROM ops_contacts
		WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&contact.ID, &contact.Name, &contact.Phone, &contact.Enabled, &contact.CreatedAt, &contact.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrOpsContactNotFound
		}
		return nil, fmt.Errorf("get ops contact: %w", err)
	}
	nodeIDs, err := r.ListNodeIDs(contact.ID)
	if err != nil {
		return nil, err
	}
	contact.NodeIDs = nodeIDs
	return contact, nil
}

func (r *OpsContactRepository) Create(contact *models.OpsContact) error {
	return r.db.QueryRow(`
		INSERT INTO ops_contacts(name, phone, enabled)
		VALUES($1, $2, $3)
		RETURNING id, created_at, updated_at`,
		contact.Name, contact.Phone, contact.Enabled,
	).Scan(&contact.ID, &contact.CreatedAt, &contact.UpdatedAt)
}

func (r *OpsContactRepository) Update(contact *models.OpsContact) error {
	err := r.db.QueryRow(`
		UPDATE ops_contacts
		SET name = $2, phone = $3, enabled = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING updated_at`,
		contact.ID, contact.Name, contact.Phone, contact.Enabled,
	).Scan(&contact.UpdatedAt)
	if err == sql.ErrNoRows {
		return ErrOpsContactNotFound
	}
	return err
}

func (r *OpsContactRepository) SoftDelete(id string) error {
	result, err := r.db.Exec(`
		UPDATE ops_contacts
		SET deleted_at = CURRENT_TIMESTAMP, enabled = FALSE, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("soft delete ops contact: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrOpsContactNotFound
	}
	_, err = r.db.Exec(`DELETE FROM node_ops_contacts WHERE contact_id = $1`, id)
	return err
}

// ClearBindingsForNodeTx removes all ops-contact bindings for a node.
// Soft-deleting edge_nodes does not fire ON DELETE CASCADE, so callers must
// clear bindings explicitly before (or with) node soft-delete.
func (r *OpsContactRepository) ClearBindingsForNodeTx(tx *Tx, nodeID string) error {
	_, err := tx.Exec(`DELETE FROM node_ops_contacts WHERE edge_node_id = $1`, nodeID)
	if err != nil {
		return fmt.Errorf("clear ops contact bindings for node: %w", err)
	}
	return nil
}

func (r *OpsContactRepository) UpdateEnabled(id string, enabled bool) (*models.OpsContact, error) {
	contact := &models.OpsContact{}
	err := r.db.QueryRow(`
		UPDATE ops_contacts
		SET enabled = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, name, phone, enabled, created_at, updated_at`, id, enabled).
		Scan(&contact.ID, &contact.Name, &contact.Phone, &contact.Enabled, &contact.CreatedAt, &contact.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrOpsContactNotFound
	}
	if err != nil {
		return nil, err
	}
	nodeIDs, err := r.ListNodeIDs(contact.ID)
	if err != nil {
		return nil, err
	}
	contact.NodeIDs = nodeIDs
	return contact, nil
}

func (r *OpsContactRepository) ListNodeIDs(contactID string) ([]string, error) {
	rows, err := r.db.Query(`
		SELECT edge_node_id
		FROM node_ops_contacts
		WHERE contact_id = $1
		ORDER BY created_at`, contactID)
	if err != nil {
		return nil, fmt.Errorf("list contact nodes: %w", err)
	}
	defer rows.Close()

	nodeIDs := make([]string, 0)
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, err
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	return nodeIDs, rows.Err()
}

// CountActiveByNode counts enabled, non-deleted contacts bound to a node.
func (r *OpsContactRepository) CountActiveByNode(nodeID string) (int, error) {
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*)
		FROM node_ops_contacts n
		INNER JOIN ops_contacts c ON c.id = n.contact_id
		WHERE n.edge_node_id = $1
		  AND c.deleted_at IS NULL
		  AND c.enabled = TRUE`, nodeID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count active contacts for node: %w", err)
	}
	return count, nil
}

// ListPublicForNode returns name+phone for Edge display.
func (r *OpsContactRepository) ListPublicForNode(nodeID string) ([]models.OpsContactPublic, error) {
	rows, err := r.db.Query(`
		SELECT c.name, c.phone
		FROM node_ops_contacts n
		INNER JOIN ops_contacts c ON c.id = n.contact_id
		WHERE n.edge_node_id = $1
		  AND c.deleted_at IS NULL
		  AND c.enabled = TRUE
		ORDER BY c.name, c.phone`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list public contacts for node: %w", err)
	}
	defer rows.Close()

	contacts := make([]models.OpsContactPublic, 0)
	for rows.Next() {
		var item models.OpsContactPublic
		if err := rows.Scan(&item.Name, &item.Phone); err != nil {
			return nil, err
		}
		contacts = append(contacts, item)
	}
	return contacts, rows.Err()
}

// ReplaceNodeBindings replaces all node bindings for a contact, enforcing per-node caps.
func (r *OpsContactRepository) ReplaceNodeBindings(contactID string, nodeIDs []string, maxPerNode int) error {
	if maxPerNode <= 0 {
		maxPerNode = 5
	}

	tx, err := r.db.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM ops_contacts WHERE id = $1 AND deleted_at IS NULL)`, contactID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrOpsContactNotFound
	}

	unique := make([]string, 0, len(nodeIDs))
	seen := map[string]struct{}{}
	for _, nodeID := range nodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			continue
		}
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		unique = append(unique, nodeID)
	}

	if _, err := tx.Exec(`DELETE FROM node_ops_contacts WHERE contact_id = $1`, contactID); err != nil {
		return fmt.Errorf("clear contact bindings: %w", err)
	}

	for _, nodeID := range unique {
		var nodeOK bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM edge_nodes WHERE id = $1 AND deleted_at IS NULL)`, nodeID).Scan(&nodeOK); err != nil {
			return err
		}
		if !nodeOK {
			return ErrOpsContactNodeNotFound
		}

		var count int
		if err := tx.QueryRow(`
			SELECT COUNT(*)
			FROM node_ops_contacts n
			INNER JOIN ops_contacts c ON c.id = n.contact_id
			WHERE n.edge_node_id = $1
			  AND c.deleted_at IS NULL`, nodeID).Scan(&count); err != nil {
			return err
		}
		if count >= maxPerNode {
			return fmt.Errorf("%w: node %s already has %d contacts (max %d)", ErrNodeContactLimitExceeded, nodeID, count, maxPerNode)
		}

		if _, err := tx.Exec(`
			INSERT INTO node_ops_contacts(edge_node_id, contact_id)
			VALUES($1, $2)`, nodeID, contactID); err != nil {
			return fmt.Errorf("bind contact to node: %w", err)
		}
	}

	if _, err := tx.Exec(`UPDATE ops_contacts SET updated_at = CURRENT_TIMESTAMP WHERE id = $1`, contactID); err != nil {
		return err
	}
	return tx.Commit()
}
