package database

import (
	"database/sql"
	"errors"
	"strings"

	"fly-print-cloud/api/internal/models"
)

var (
	ErrPrintQuotaGrantInvalid = errors.New("invalid print quota grant")
	ErrPrintQuotaUserNotFound = errors.New("print quota user not found")
)

type PrintQuotaRepository struct {
	db *DB
}

func NewPrintQuotaRepository(db *DB) *PrintQuotaRepository {
	return &PrintQuotaRepository{db: db}
}

func (r *PrintQuotaRepository) Grant(
	userID, adminUserID string,
	amount int,
	reason string,
) (*models.User, error) {
	reason = strings.TrimSpace(reason)
	if amount <= 0 || userID == "" || adminUserID == "" || reason == "" || len(reason) > 500 {
		return nil, ErrPrintQuotaGrantInvalid
	}

	tx, err := r.db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var localAdminUserID string
	// Cloud Admin 的内置登录 Token subject 就是 users.id。
	// 外部身份映射独立存放在 external_identities，不存在 users.external_id 列。
	err = tx.QueryRow(`SELECT id::text FROM users WHERE id::text=$1 AND role='admin'`, adminUserID).Scan(&localAdminUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPrintQuotaGrantInvalid
	}
	if err != nil {
		return nil, err
	}

	user := &models.User{}
	var lastLogin sql.NullTime
	err = tx.QueryRow(`UPDATE users
		SET print_quota_balance=print_quota_balance+$2
		WHERE id=$1::uuid
		RETURNING id::text,username,email,role,status,print_quota_balance,
			last_login,created_at,updated_at`,
		userID, amount,
	).Scan(
		&user.ID, &user.Username, &user.Email, &user.Role, &user.Status,
		&user.PrintQuotaBalance, &lastLogin, &user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPrintQuotaUserNotFound
	}
	if err != nil {
		return nil, err
	}
	if lastLogin.Valid {
		user.LastLogin = lastLogin.Time
	}

	if _, err = tx.Exec(`INSERT INTO print_quota_transactions
		(user_id,transaction_type,delta,balance_after,admin_user_id,reason)
		VALUES ($1::uuid,'admin_grant',$2,$3,$4::uuid,$5)`,
		userID, amount, user.PrintQuotaBalance, localAdminUserID, reason,
	); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return user, nil
}
