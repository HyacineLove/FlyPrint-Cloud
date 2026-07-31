package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/security"

	"golang.org/x/crypto/bcrypt"
)

// UserRepository 用户数据访问层
type UserRepository struct {
	db *DB
}

// ErrUserHasActivePrintJobs prevents deleting an account while Edge may still
// be working on one of its print jobs.
var ErrUserHasActivePrintJobs = errors.New("user has active print jobs")

// UserListFilter contains only operator-controlled user list filters and sort
// fields. SortBy is translated through an allowlist before entering SQL.
type UserListFilter struct {
	Search    string
	Role      string
	Status    string
	SortBy    string
	SortOrder string
	Offset    int
	Limit     int
}

// NewUserRepository 创建用户仓库
func NewUserRepository(db *DB) *UserRepository {
	return &UserRepository{db: db}
}

// CreateUser 创建用户
func (r *UserRepository) CreateUser(user *models.User) error {
	user.Email = security.NormalizeEmail(user.Email)

	// 验证用户名格式
	if err := security.ValidateUsername(user.Username); err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	// 验证邮箱格式
	if err := security.ValidateEmail(user.Email); err != nil {
		return fmt.Errorf("invalid email: %w", err)
	}

	// 验证密码强度（user.PasswordHash 此时存储的是明文密码）
	if err := security.ValidatePasswordStrength(user.PasswordHash); err != nil {
		return fmt.Errorf("weak password: %w", err)
	}

	// 加密密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.PasswordHash), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	query := `
		INSERT INTO users (username, email, password_hash, role, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at`

	err = r.db.QueryRow(query, user.Username, user.Email, string(hashedPassword), user.Role, user.Status).
		Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	// 清空密码哈希值，不应该返回
	user.PasswordHash = ""
	return nil
}

func scanUser(scanner interface{ Scan(...any) error }) (*models.User, error) {
	user := &models.User{}
	var lastLogin sql.NullTime
	err := scanner.Scan(
		&user.ID, &user.Username, &user.Email, &user.Role, &user.Status,
		&user.PrintQuotaBalance, &lastLogin, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if lastLogin.Valid {
		user.LastLogin = lastLogin.Time
	}
	return user, nil
}

// GetUserByID 根据ID获取 active 用户，供现有业务调用。
func (r *UserRepository) GetUserByID(id string) (*models.User, error) {
	query := `
		SELECT id, username, email, role, status, print_quota_balance, last_login, created_at, updated_at
		FROM users WHERE id = $1 AND status = 'active'`
	user, err := scanUser(r.db.QueryRow(query, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}

// GetUserByIDAnyStatus 获取管理用用户详情，包含 inactive 用户。
func (r *UserRepository) GetUserByIDAnyStatus(id string) (*models.User, error) {
	query := `
		SELECT id, username, email, role, status, print_quota_balance, last_login, created_at, updated_at
		FROM users WHERE id = $1`
	user, err := scanUser(r.db.QueryRow(query, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}

// GetUserByUsername 根据用户名获取用户（包含密码哈希，用于登录验证）
func (r *UserRepository) GetUserByUsername(username string) (*models.User, error) {
	user := &models.User{}
	var lastLogin sql.NullTime
	query := `
		SELECT id, username, email, password_hash, role, status, print_quota_balance, last_login, created_at, updated_at
		FROM users WHERE username = $1 AND status = 'active'`

	err := r.db.QueryRow(query, username).Scan(
		&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Role,
		&user.Status, &user.PrintQuotaBalance, &lastLogin, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if lastLogin.Valid {
		user.LastLogin = lastLogin.Time
	}

	return user, nil
}

// GetUserByEmail gets an active builtin account by its canonical login
// identifier. Email matching is case-insensitive for existing records too.
func (r *UserRepository) GetUserByEmail(email string) (*models.User, error) {
	user := &models.User{}
	var lastLogin sql.NullTime
	query := `
		SELECT id, username, email, password_hash, role, status, print_quota_balance, last_login, created_at, updated_at
		FROM users WHERE LOWER(email) = LOWER($1) AND status = 'active'`

	err := r.db.QueryRow(query, security.NormalizeEmail(email)).Scan(
		&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Role,
		&user.Status, &user.PrintQuotaBalance, &lastLogin, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if lastLogin.Valid {
		user.LastLogin = lastLogin.Time
	}

	return user, nil
}

// UpdateUser 更新用户信息
func (r *UserRepository) UpdateUser(user *models.User) error {
	query := `
		UPDATE users
		SET username = $2, role = $3
		WHERE id = $1
		RETURNING updated_at`

	err := r.db.QueryRow(query, user.ID, user.Username, user.Role).
		Scan(&user.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	return nil
}

// UpdateEnabled 将管理页的 Toggle 映射到现有 users.status 字段。
func (r *UserRepository) UpdateEnabled(id string, enabled bool) (*models.User, error) {
	query := `
		UPDATE users
		SET status = CASE WHEN $2 THEN 'active' ELSE 'inactive' END
		WHERE id = $1
		RETURNING id, username, email, role, status, print_quota_balance, last_login, created_at, updated_at`
	user, err := scanUser(r.db.QueryRow(query, id, enabled))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to update user status: %w", err)
	}
	return user, nil
}

// UpdatePassword 更新用户密码
func (r *UserRepository) UpdatePassword(userID, newPassword string) error {
	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	query := `UPDATE users SET password_hash = $2 WHERE id = $1`
	_, err = r.db.Exec(query, userID, string(hashedPassword))
	if err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	return nil
}

// UpdateLastLogin 更新最后登录时间
func (r *UserRepository) UpdateLastLogin(userID string) error {
	query := `UPDATE users SET last_login = CURRENT_TIMESTAMP WHERE id = $1`
	_, err := r.db.Exec(query, userID)
	if err != nil {
		return fmt.Errorf("failed to update last login: %w", err)
	}

	return nil
}

// DeleteUser 删除用户（软删除，设置状态为inactive）
func (r *UserRepository) DeleteUser(userID string) error {
	query := `UPDATE users SET status = 'inactive' WHERE id = $1`
	result, err := r.db.Exec(query, userID)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("user not found")
	}

	return nil
}

// DeleteUserWithPrintJobs 在同一事务中删除用户及其非活动打印历史。
func (r *UserRepository) DeleteUserWithPrintJobs(userID string) error {
	tx, err := r.db.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var lockedID string
	if err := tx.QueryRow(`SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&lockedID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("user not found")
		}
		return fmt.Errorf("failed to lock user: %w", err)
	}

	var activeJobs int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM print_jobs
		WHERE user_id = $1 AND status IN ('pending', 'dispatched', 'processing')`, userID).Scan(&activeJobs); err != nil {
		return fmt.Errorf("failed to check active print jobs: %w", err)
	}
	if activeJobs > 0 {
		return ErrUserHasActivePrintJobs
	}

	// Site Portal 身份映射从属于 Cloud 用户，删除用户时在同一事务内解除映射。
	if _, err := tx.Exec(`DELETE FROM external_identities WHERE cloud_user_id = $1`, userID); err != nil {
		return fmt.Errorf("failed to delete user external identities: %w", err)
	}

	if _, err := tx.Exec(`
		DELETE FROM operational_alerts
		WHERE job_id IN (SELECT id FROM print_jobs WHERE user_id = $1)`, userID); err != nil {
		return fmt.Errorf("failed to delete user print job alerts: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM print_jobs WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("failed to delete user print jobs: %w", err)
	}
	result, err := tx.Exec(`DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("failed to verify user deletion: %w", err)
	} else if affected == 0 {
		return fmt.Errorf("user not found")
	}
	return tx.Commit()
}

// ListUsers 获取用户列表，默认包含 active 和 inactive 用户。
func (r *UserRepository) ListUsers(filter UserListFilter) ([]*models.User, int, error) {
	var users []*models.User
	var total int
	where, args, nextArg := buildUserListFilter(filter)
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	countQuery := `SELECT COUNT(*) FROM users` + whereSQL
	err := r.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count users: %w", err)
	}

	// 获取用户列表
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	query := `
		SELECT id, username, email, role, status, print_quota_balance, last_login, created_at, updated_at
		FROM users` + whereSQL + `
		ORDER BY ` + userSortColumn(filter.SortBy) + ` ` + userSortDirection(filter.SortOrder) +
		fmt.Sprintf(`
		LIMIT $%d OFFSET $%d`, nextArg, nextArg+1)
	args = append(args, limit, filter.Offset)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, user)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows error: %w", err)
	}

	return users, total, nil
}

func buildUserListFilter(filter UserListFilter) ([]string, []interface{}, int) {
	where := make([]string, 0, 3)
	args := make([]interface{}, 0, 3)
	nextArg := 1
	if search := strings.TrimSpace(filter.Search); search != "" {
		where = append(where, fmt.Sprintf("(id::text ILIKE $%d OR username ILIKE $%d OR LOWER(email) LIKE LOWER($%d))", nextArg, nextArg, nextArg))
		args = append(args, "%"+search+"%")
		nextArg++
	}
	if filter.Role != "" {
		where = append(where, fmt.Sprintf("role = $%d", nextArg))
		args = append(args, filter.Role)
		nextArg++
	}
	if filter.Status != "" {
		where = append(where, fmt.Sprintf("status = $%d", nextArg))
		args = append(args, filter.Status)
		nextArg++
	}
	return where, args, nextArg
}

func userSortColumn(value string) string {
	columns := map[string]string{
		"id":         "id",
		"username":   "username",
		"email":      "email",
		"role":       "role",
		"status":     "status",
		"last_login": "last_login",
		"created_at": "created_at",
	}
	if column, ok := columns[value]; ok {
		return column
	}
	return "created_at"
}

func userSortDirection(value string) string {
	if strings.EqualFold(value, "asc") {
		return "ASC"
	}
	return "DESC"
}

// VerifyPassword 验证密码
func (r *UserRepository) VerifyPassword(user *models.User, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	return err == nil
}

// EmailExists 检查邮箱是否已存在
func (r *UserRepository) EmailExists(email string, excludeUserID ...string) (bool, error) {
	query := `SELECT COUNT(*) FROM users WHERE LOWER(email) = LOWER($1)`
	args := []interface{}{security.NormalizeEmail(email)}

	if len(excludeUserID) > 0 && excludeUserID[0] != "" {
		query += ` AND id != $2`
		args = append(args, excludeUserID[0])
	}

	var count int
	err := r.db.QueryRow(query, args...).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check email existence: %w", err)
	}

	return count > 0, nil
}

// UsernameExists 检查用户名是否已存在
func (r *UserRepository) UsernameExists(username string, excludeUserID ...string) (bool, error) {
	query := `SELECT COUNT(*) FROM users WHERE username = $1`
	args := []interface{}{username}

	if len(excludeUserID) > 0 && excludeUserID[0] != "" {
		query += ` AND id != $2`
		args = append(args, excludeUserID[0])
	}

	var count int
	err := r.db.QueryRow(query, args...).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check username existence: %w", err)
	}

	return count > 0, nil
}

// GetUserByExternalID 通过外部ID获取用户
func (r *UserRepository) GetUserByExternalID(externalID string) (*models.User, error) {
	query := `
		SELECT id, username, email, password_hash, external_id, role, status,
			print_quota_balance,last_login,created_at,updated_at
		FROM users 
		WHERE external_id = $1`

	var user models.User
	var externalIDPtr sql.NullString

	err := r.db.QueryRow(query, externalID).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&externalIDPtr,
		&user.Role,
		&user.Status,
		&user.PrintQuotaBalance,
		&user.LastLogin,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	if externalIDPtr.Valid {
		user.ExternalID = &externalIDPtr.String
	}

	return &user, nil
}

// CreateUserFromOAuth2 从OAuth2信息创建用户
func (r *UserRepository) CreateUserFromOAuth2(externalID, username, email string) (*models.User, error) {
	email = security.NormalizeEmail(email)
	query := `
		INSERT INTO users (username, email, external_id, password_hash, role, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`

	user := &models.User{
		Username:     username,
		Email:        email,
		ExternalID:   &externalID,
		PasswordHash: "oauth2_user", // OAuth2 用户的占位符密码哈希
		Role:         "admin",
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	err := r.db.QueryRow(query,
		user.Username,
		user.Email,
		user.ExternalID,
		user.PasswordHash,
		user.Role,
		user.Status,
		user.CreatedAt,
		user.UpdatedAt,
	).Scan(&user.ID)

	if err != nil {
		return nil, err
	}

	return user, nil
}
