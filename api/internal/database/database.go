package database

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"time"

	"fly-print-cloud/api/internal/config"
	schemaMigrations "fly-print-cloud/api/internal/database/migrations"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/security"

	_ "github.com/lib/pq"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// DB 数据库实例
type DB struct {
	*sql.DB
}

// New 创建数据库连接
func New(cfg *config.DatabaseConfig) (*DB, error) {
	db, err := sql.Open("postgres", cfg.GetDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 测试连接
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// 设置连接池参数
	db.SetMaxOpenConns(25)                 // 最大打开连接数
	db.SetMaxIdleConns(5)                  // 最大空闲连接数
	db.SetConnMaxLifetime(5 * time.Minute) // 连接最大生存时间

	return &DB{db}, nil
}

// Close 关闭数据库连接
func (db *DB) Close() error {
	return db.DB.Close()
}

// Ping 测试数据库连接
func (db *DB) Ping() error {
	return db.DB.Ping()
}

// Stats 获取数据库连接池统计信息
func (db *DB) Stats() sql.DBStats {
	return db.DB.Stats()
}

// Tx 数据库事务包装器
type Tx struct {
	*sql.Tx
}

// BeginTx 开始一个数据库事务
func (db *DB) BeginTx() (*Tx, error) {
	tx, err := db.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	return &Tx{tx}, nil
}

// Commit 提交事务
func (tx *Tx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// Rollback 回滚事务
func (tx *Tx) Rollback() error {
	if err := tx.Tx.Rollback(); err != nil {
		return fmt.Errorf("failed to rollback transaction: %w", err)
	}
	return nil
}

// InitTables 初始化数据库表
func (db *DB) InitTables() error {
	// 创建用户表
	userTableSQL := `
	CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		username VARCHAR(50) UNIQUE NOT NULL,
		email VARCHAR(100) UNIQUE NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		role VARCHAR(20) NOT NULL DEFAULT 'viewer',
		status VARCHAR(20) NOT NULL DEFAULT 'active',
		last_login TIMESTAMP,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(userTableSQL); err != nil {
		return fmt.Errorf("failed to create users table: %w", err)
	}

	// 创建更新时间触发器
	updateTriggerSQL := `
	CREATE OR REPLACE FUNCTION update_updated_at_column()
	RETURNS TRIGGER AS $$
	BEGIN
		NEW.updated_at = CURRENT_TIMESTAMP;
		RETURN NEW;
	END;
	$$ language 'plpgsql';

	DROP TRIGGER IF EXISTS update_users_updated_at ON users;
	CREATE TRIGGER update_users_updated_at
		BEFORE UPDATE ON users
		FOR EACH ROW
		EXECUTE FUNCTION update_updated_at_column();`

	if _, err := db.Exec(updateTriggerSQL); err != nil {
		return fmt.Errorf("failed to create update trigger: %w", err)
	}

	// 创建 Edge Node 表
	edgeNodeTableSQL := `
	CREATE TABLE IF NOT EXISTS edge_nodes (
		id VARCHAR(100) PRIMARY KEY,
		name VARCHAR(100) NOT NULL, -- User-friendly display name (can be modified)
		login_source VARCHAR(100) NOT NULL DEFAULT 'official',
		status VARCHAR(20) NOT NULL DEFAULT 'offline',
		health_status VARCHAR(20) NOT NULL DEFAULT 'unknown',
		health_reason_code VARCHAR(100),
		health_message TEXT,
		enabled BOOLEAN NOT NULL DEFAULT true, -- 云端启用/禁用状态
		version VARCHAR(50),
		last_heartbeat TIMESTAMP,
		printer_status_received_at TIMESTAMP,
		deleted_at TIMESTAMP,
		
		-- 位置信息
		location VARCHAR(255),
		latitude DECIMAL(10, 8),
		longitude DECIMAL(11, 8),
		
		-- 网络信息
		ip_address INET,
		mac_address VARCHAR(17),
		network_interface VARCHAR(50),
		
		-- 系统信息
		os_version VARCHAR(100),
		cpu_info TEXT,
		memory_info TEXT,
		disk_info TEXT,
		
		-- 连接信息
		connection_quality VARCHAR(20),
		latency INTEGER,
		cpu_usage DOUBLE PRECISION DEFAULT 0,
		memory_usage DOUBLE PRECISION DEFAULT 0,
		disk_usage DOUBLE PRECISION DEFAULT 0,
		components JSONB DEFAULT '{}'::jsonb,
		
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(edgeNodeTableSQL); err != nil {
		return fmt.Errorf("failed to create edge_nodes table: %w", err)
	}

	// 创建 Edge Node 更新时间触发器
	edgeNodeTriggerSQL := `
	DROP TRIGGER IF EXISTS update_edge_nodes_updated_at ON edge_nodes;
	CREATE TRIGGER update_edge_nodes_updated_at
		BEFORE UPDATE ON edge_nodes
		FOR EACH ROW
		EXECUTE FUNCTION update_updated_at_column();`

	if _, err := db.Exec(edgeNodeTriggerSQL); err != nil {
		return fmt.Errorf("failed to create edge_nodes update trigger: %w", err)
	}

	// 创建打印机表
	printerTableSQL := `
	CREATE TABLE IF NOT EXISTS printers (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name VARCHAR(100) NOT NULL,
		display_name VARCHAR(255),
		model VARCHAR(100),
		serial_number VARCHAR(100),
		status VARCHAR(64) NOT NULL DEFAULT 'printer_state_unknown',
		status_observed_since TIMESTAMP,
		source_observed_at TIMESTAMP,
		status_received_at TIMESTAMP,
		enabled BOOLEAN NOT NULL DEFAULT true, -- 云端启用/禁用状态
		
		-- 硬件信息
		firmware_version VARCHAR(50),
		port_info VARCHAR(100),
		
		-- 网络信息
		ip_address INET,
		mac_address VARCHAR(17),
		network_config TEXT,
		
		-- 能力信息 (JSON 格式)
		capabilities JSONB,
		
		-- 关联信息
		edge_node_id VARCHAR(100) REFERENCES edge_nodes(id) ON DELETE CASCADE,
		
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		
		-- 添加唯一约束以支持 ON CONFLICT
		UNIQUE (name, edge_node_id)
	);`

	if _, err := db.Exec(printerTableSQL); err != nil {
		return fmt.Errorf("failed to create printers table: %w", err)
	}

	// 创建打印机更新时间触发器
	printerTriggerSQL := `
	DROP TRIGGER IF EXISTS update_printers_updated_at ON printers;
	CREATE TRIGGER update_printers_updated_at
		BEFORE UPDATE ON printers
		FOR EACH ROW
		EXECUTE FUNCTION update_updated_at_column();`

	if _, err := db.Exec(printerTriggerSQL); err != nil {
		return fmt.Errorf("failed to create printers update trigger: %w", err)
	}

	// 创建打印任务表
	printJobTableSQL := `
	CREATE TABLE IF NOT EXISTS print_jobs (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name VARCHAR(200) NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		
		-- 关联信息
		printer_id UUID REFERENCES printers(id) ON DELETE CASCADE,
		user_id VARCHAR(255), -- 移除外键约束，支持OAuth2外部用户ID
		user_name VARCHAR(100),
		
		-- 任务信息
		file_path VARCHAR(500),
		file_url VARCHAR(1000),
		content_hash VARCHAR(64),
		file_size BIGINT,
		page_count INTEGER,
		copies INTEGER DEFAULT 1,
		
		-- 打印设置
		paper_size VARCHAR(20),
		color_mode VARCHAR(20),
		duplex_mode VARCHAR(20),
		
		-- 执行信息
		start_time TIMESTAMP,
		end_time TIMESTAMP,
		error_message TEXT,
		error_code VARCHAR(100),
		
		-- 重试信息
		retry_count INTEGER DEFAULT 0,
		max_retries INTEGER DEFAULT 3,
		
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(printJobTableSQL); err != nil {
		return fmt.Errorf("failed to create print_jobs table: %w", err)
	}

	if _, err := db.Exec("ALTER TABLE print_jobs ADD COLUMN IF NOT EXISTS content_hash VARCHAR(64);"); err != nil {
		return fmt.Errorf("failed to migrate print_jobs table: %w", err)
	}

	operationalAlertsSQL := `
	CREATE TABLE IF NOT EXISTS operational_alerts (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		resource_type VARCHAR(20) NOT NULL,
		resource_id VARCHAR(100) NOT NULL,
		node_id VARCHAR(100),
		printer_id UUID,
		job_id UUID,
		reason_code VARCHAR(100) NOT NULL,
		category VARCHAR(30) NOT NULL,
		title VARCHAR(200) NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'open',
		details JSONB DEFAULT '{}'::jsonb,
		occurrence_count INTEGER NOT NULL DEFAULT 1,
		first_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		resolved_at TIMESTAMP,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(operationalAlertsSQL); err != nil {
		return fmt.Errorf("failed to create operational_alerts table: %w", err)
	}

	// 创建打印任务更新时间触发器
	printJobTriggerSQL := `
	DROP TRIGGER IF EXISTS update_print_jobs_updated_at ON print_jobs;
	CREATE TRIGGER update_print_jobs_updated_at
		BEFORE UPDATE ON print_jobs
		FOR EACH ROW
		EXECUTE FUNCTION update_updated_at_column();`

	if _, err := db.Exec(printJobTriggerSQL); err != nil {
		return fmt.Errorf("failed to create print_jobs update trigger: %w", err)
	}

	// 创建Token使用记录表（用于一次性凭证验证）
	tokenUsageTableSQL := `
	CREATE TABLE IF NOT EXISTS token_usage_records (
		token_hash VARCHAR(64) PRIMARY KEY,
		token_type VARCHAR(20) NOT NULL,
		node_id VARCHAR(100) NOT NULL,
		resource_id VARCHAR(100),
		job_id VARCHAR(100),
		used_at TIMESTAMP,
		revoked BOOLEAN DEFAULT FALSE,
		revoked_at TIMESTAMP,
		expires_at TIMESTAMP NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(tokenUsageTableSQL); err != nil {
		return fmt.Errorf("failed to create token_usage_records table: %w", err)
	}

	// 数据库迁移：为 token_usage_records 表添加新字段（如果不存在）
	systemSettingsTableSQL := `
	CREATE TABLE IF NOT EXISTS system_settings (
		key VARCHAR(100) PRIMARY KEY,
		value TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(systemSettingsTableSQL); err != nil {
		return fmt.Errorf("failed to create system_settings table: %w", err)
	}

	systemSettingsTriggerSQL := `
	DROP TRIGGER IF EXISTS update_system_settings_updated_at ON system_settings;
	CREATE TRIGGER update_system_settings_updated_at
		BEFORE UPDATE ON system_settings
		FOR EACH ROW
		EXECUTE FUNCTION update_updated_at_column();`

	if _, err := db.Exec(systemSettingsTriggerSQL); err != nil {
		return fmt.Errorf("failed to create system_settings update trigger: %w", err)
	}

	migrations := []string{
		"ALTER TABLE token_usage_records ADD COLUMN IF NOT EXISTS revoked BOOLEAN DEFAULT FALSE;",
		"ALTER TABLE token_usage_records ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMP;",
		// 将 used_at 改为可空（兼容预注册token）
		"ALTER TABLE token_usage_records ALTER COLUMN used_at DROP NOT NULL;",
		"ALTER TABLE token_usage_records ALTER COLUMN used_at DROP DEFAULT;",
		"ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS health_status VARCHAR(20) NOT NULL DEFAULT 'unknown';",
		"ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS login_source VARCHAR(100) NOT NULL DEFAULT 'official';",
		"ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS health_reason_code VARCHAR(100);",
		"ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS health_message TEXT;",
		"ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS cpu_usage DOUBLE PRECISION DEFAULT 0;",
		"ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS memory_usage DOUBLE PRECISION DEFAULT 0;",
		"ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS disk_usage DOUBLE PRECISION DEFAULT 0;",
		"ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS components JSONB DEFAULT '{}'::jsonb;",
		"ALTER TABLE edge_nodes ADD COLUMN IF NOT EXISTS printer_status_received_at TIMESTAMP;",
		"ALTER TABLE printers ALTER COLUMN status TYPE VARCHAR(64);",
		"ALTER TABLE printers ADD COLUMN IF NOT EXISTS status_observed_since TIMESTAMP;",
		"ALTER TABLE printers ADD COLUMN IF NOT EXISTS source_observed_at TIMESTAMP;",
		"ALTER TABLE printers ADD COLUMN IF NOT EXISTS status_received_at TIMESTAMP;",
		"ALTER TABLE printers ALTER COLUMN status SET DEFAULT 'printer_state_unknown';",
		"ALTER TABLE printers DROP COLUMN IF EXISTS health_status;",
		"ALTER TABLE printers DROP COLUMN IF EXISTS reason_code;",
		"ALTER TABLE printers DROP COLUMN IF EXISTS reason_message;",
		"ALTER TABLE printers DROP COLUMN IF EXISTS reason_observed_since;",
		"ALTER TABLE printers DROP COLUMN IF EXISTS accepting_jobs;",
		"ALTER TABLE printers DROP COLUMN IF EXISTS diagnostics;",
		"ALTER TABLE printers DROP COLUMN IF EXISTS queue_length;",
		"ALTER TABLE print_jobs ADD COLUMN IF NOT EXISTS error_code VARCHAR(100);",
		"UPDATE printers SET status = CASE WHEN status = 'ready' THEN 'idle' WHEN status = 'processing' THEN 'printing' WHEN status IN ('error','stopped') THEN 'printer_stopped' WHEN status IN ('offline','unknown') THEN 'printer_state_unknown' ELSE status END WHERE status IN ('ready','processing','error','stopped','offline','unknown');",
		"UPDATE edge_nodes SET enabled = false, status = 'offline' WHERE status = 'maintenance';",
		"UPDATE print_jobs SET status = 'processing' WHERE status IN ('printing','downloading');",
		"UPDATE print_jobs SET status = 'canceled' WHERE status = 'cancelled';",
	}

	for _, migration := range migrations {
		if _, err := db.Exec(migration); err != nil {
			// 某些错误可以忽略（例如约束已存在）
			logger.Warn("Migration warning (可能已应用)", zap.Error(err))
		}
	}

	// 创建索引
	indexesSQL := []string{
		// Edge Nodes 索引
		"CREATE INDEX IF NOT EXISTS idx_edge_nodes_status ON edge_nodes(status);",
		"CREATE INDEX IF NOT EXISTS idx_edge_nodes_last_heartbeat ON edge_nodes(last_heartbeat);",
		"CREATE INDEX IF NOT EXISTS idx_edge_nodes_enabled ON edge_nodes(enabled);",
		"CREATE INDEX IF NOT EXISTS idx_edge_nodes_status_enabled ON edge_nodes(status, enabled);", // 复合索引

		// Printers 索引
		"CREATE INDEX IF NOT EXISTS idx_printers_edge_node_id ON printers(edge_node_id);",
		"CREATE INDEX IF NOT EXISTS idx_printers_status ON printers(status);",
		"CREATE INDEX IF NOT EXISTS idx_printers_enabled ON printers(enabled);",
		"CREATE INDEX IF NOT EXISTS idx_printers_edge_node_status ON printers(edge_node_id, status);", // 复合索引
		"CREATE INDEX IF NOT EXISTS idx_printers_status_enabled ON printers(status, enabled);",        // 复合索引

		// Print Jobs 索引
		"CREATE INDEX IF NOT EXISTS idx_print_jobs_status ON print_jobs(status);",
		"CREATE INDEX IF NOT EXISTS idx_operational_alerts_status ON operational_alerts(status);",
		"CREATE INDEX IF NOT EXISTS idx_operational_alerts_resource ON operational_alerts(resource_type, resource_id);",
		"CREATE INDEX IF NOT EXISTS idx_operational_alerts_last_seen ON operational_alerts(last_seen_at);",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_operational_alerts_open_unique ON operational_alerts(resource_type, resource_id, reason_code) WHERE status = 'open';",
		"CREATE INDEX IF NOT EXISTS idx_print_jobs_printer_id ON print_jobs(printer_id);",
		"CREATE INDEX IF NOT EXISTS idx_print_jobs_user_id ON print_jobs(user_id);",
		"CREATE INDEX IF NOT EXISTS idx_print_jobs_created_at ON print_jobs(created_at DESC);", // 降序，常用于最新任务查询
		"CREATE INDEX IF NOT EXISTS idx_print_jobs_updated_at ON print_jobs(updated_at DESC);",
		"CREATE INDEX IF NOT EXISTS idx_print_jobs_status_created ON print_jobs(status, created_at DESC);", // 复合索引
		"CREATE INDEX IF NOT EXISTS idx_print_jobs_printer_status ON print_jobs(printer_id, status);",      // 复合索引

		// Token Usage 索引
		"CREATE INDEX IF NOT EXISTS idx_token_usage_expires_at ON token_usage_records(expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_token_usage_node_id ON token_usage_records(node_id);",
		"CREATE INDEX IF NOT EXISTS idx_token_usage_created_at ON token_usage_records(created_at);",
		"CREATE INDEX IF NOT EXISTS idx_token_usage_node_resource ON token_usage_records(node_id, resource_id, token_type);",
		"CREATE INDEX IF NOT EXISTS idx_token_usage_revoked ON token_usage_records(revoked);",
		"CREATE INDEX IF NOT EXISTS idx_system_settings_updated_at ON system_settings(updated_at DESC);",

		// Files 索引
		"CREATE INDEX IF NOT EXISTS idx_files_uploader_id ON files(uploader_id);",
		"CREATE INDEX IF NOT EXISTS idx_files_created_at ON files(created_at DESC);",

		// Users 索引（如果需要经常按角色查询）
		"CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);",
		"CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);",
		"CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);", // username已有UNIQUE，此索引可选
		"CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);",       // email已有UNIQUE，此索引可选
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower ON users (LOWER(email));",
	}

	// 创建文件表
	filesTableSQL := `
	CREATE TABLE IF NOT EXISTS files (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		original_name VARCHAR(255) NOT NULL,
		file_name VARCHAR(255) NOT NULL,
		file_path VARCHAR(512) NOT NULL,
		storage_provider VARCHAR(20) NOT NULL DEFAULT 'local',
		storage_bucket VARCHAR(255),
		object_key VARCHAR(512),
		content_hash VARCHAR(64),
		mime_type VARCHAR(100) NOT NULL,
		size BIGINT NOT NULL,
		uploader_id VARCHAR(100) NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(filesTableSQL); err != nil {
		return fmt.Errorf("failed to create files table: %w", err)
	}

	fileMigrations := []string{
		"ALTER TABLE files ADD COLUMN IF NOT EXISTS storage_provider VARCHAR(20);",
		"ALTER TABLE files ADD COLUMN IF NOT EXISTS storage_bucket VARCHAR(255);",
		"ALTER TABLE files ADD COLUMN IF NOT EXISTS object_key VARCHAR(512);",
		"ALTER TABLE files ADD COLUMN IF NOT EXISTS content_hash VARCHAR(64);",
		"ALTER TABLE files ALTER COLUMN storage_provider SET DEFAULT 'local';",
		"UPDATE files SET storage_provider = 'local' WHERE storage_provider IS NULL OR storage_provider = '';",
		"UPDATE files SET object_key = file_path WHERE object_key IS NULL OR object_key = '';",
	}

	for _, migration := range fileMigrations {
		if _, err := db.Exec(migration); err != nil {
			return fmt.Errorf("failed to migrate files table: %w", err)
		}
	}

	// 创建 OAuth2 客户端表（内置认证模式使用）
	oauth2ClientsTableSQL := `
	CREATE TABLE IF NOT EXISTS oauth2_clients (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		client_id VARCHAR(100) UNIQUE NOT NULL,
		client_secret_hash VARCHAR(255) NOT NULL,
		client_secret_encrypted TEXT NOT NULL,
		client_type VARCHAR(20) NOT NULL DEFAULT 'edge_node' CHECK (client_type IN ('edge_node','site_portal')),
		site_portal_code VARCHAR(64),
		allowed_scopes TEXT NOT NULL,
		description TEXT,
		enabled BOOLEAN NOT NULL DEFAULT true,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(oauth2ClientsTableSQL); err != nil {
		return fmt.Errorf("failed to create oauth2_clients table: %w", err)
	}

	// 创建 OAuth2 客户端更新时间触发器
	oauth2ClientsTriggerSQL := `
	DROP TRIGGER IF EXISTS update_oauth2_clients_updated_at ON oauth2_clients;
	CREATE TRIGGER update_oauth2_clients_updated_at
		BEFORE UPDATE ON oauth2_clients
		FOR EACH ROW
		EXECUTE FUNCTION update_updated_at_column();`

	if _, err := db.Exec(oauth2ClientsTriggerSQL); err != nil {
		return fmt.Errorf("failed to create oauth2_clients update trigger: %w", err)
	}

	// 添加 OAuth2 客户端索引
	indexesSQL = append(indexesSQL, "CREATE INDEX IF NOT EXISTS idx_oauth2_clients_client_id ON oauth2_clients(client_id);")
	indexesSQL = append(indexesSQL, "CREATE INDEX IF NOT EXISTS idx_oauth2_clients_enabled ON oauth2_clients(enabled);")

	for _, indexSQL := range indexesSQL {
		if _, err := db.Exec(indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// 为 printers 表添加 deleted_at 字段（如果不存在）
	// 用于统一软删除策略，与 edge_nodes 保持一致
	alterPrintersSQL := `
	DO $$
	BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM information_schema.columns 
			WHERE table_name = 'printers' AND column_name = 'deleted_at'
		) THEN
			ALTER TABLE printers ADD COLUMN deleted_at TIMESTAMP;
			CREATE INDEX idx_printers_deleted_at ON printers(deleted_at);
		END IF;
	END $$;`

	if _, err := db.Exec(alterPrintersSQL); err != nil {
		return fmt.Errorf("failed to add deleted_at column to printers: %w", err)
	}

	// 基础表创建完成后先执行前向迁移，再执行依赖迁移表的兼容扩展。
	// 全新数据库中 edge_terminal_sessions 等表由迁移创建，顺序不可颠倒。
	if err := schemaMigrations.Run(db.DB); err != nil {
		return fmt.Errorf("apply schema migrations: %w", err)
	}
	if err := db.initSitePortalSchema(); err != nil {
		return err
	}
	if err := db.initPrintAuthorizationSchema(); err != nil {
		return err
	}

	logger.Info("Database tables initialized successfully")
	return nil
}

// CreateDefaultAdmin 创建默认管理员账户
func (db *DB) CreateDefaultAdmin() error {
	// 检查是否已存在管理员账户
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM users WHERE role = 'admin'").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check admin users: %w", err)
	}

	if count > 0 {
		logger.Info("Admin user already exists, skipping creation")
		return nil
	}

	// 只在环境变量允许时创建默认管理员
	createDefault := viper.GetString("create_default_admin")
	if createDefault != "true" {
		logger.Info("No admin users found, but CREATE_DEFAULT_ADMIN is not set to 'true'")
		logger.Info("To create a default admin, set CREATE_DEFAULT_ADMIN=true and restart")
		return nil
	}

	// 从环境变量获取管理员密码，如果没有则使用随机密码
	adminPassword := viper.GetString("default_admin_password")
	if adminPassword == "" {
		adminPassword = generateRandomPassword(16)
		logger.Info("Generated random admin password", zap.String("password", adminPassword))
		logger.Warn("IMPORTANT: Save this password immediately! It will not be shown again.")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash admin password: %w", err)
	}

	defaultAdminSQL := `
	INSERT INTO users (username, email, password_hash, role, status)
	VALUES ('admin', 'admin@flyprint.local', $1, 'admin', 'active')`

	if _, err := db.Exec(defaultAdminSQL, string(hashedPassword)); err != nil {
		return fmt.Errorf("failed to create default admin: %w", err)
	}

	logger.Info("Default admin user created successfully", zap.String("username", "admin"))
	if viper.GetString("default_admin_password") == "" {
		logger.Warn("=====================================")
		logger.Warn("IMPORTANT: ADMIN CREDENTIALS")
		logger.Warn("=====================================")
		logger.Info("Default admin created", zap.String("username", "admin"), zap.String("password", adminPassword))
		logger.Warn("=====================================")
		logger.Warn("SAVE THIS PASSWORD IMMEDIATELY!")
		logger.Warn("This password will NOT be shown again!")
		logger.Warn("Change it after first login for security!")
		logger.Warn("=====================================")
	} else {
		logger.Info("Using custom admin password from environment variable")
	}
	return nil
}

// CreateDefaultOAuth2Client 创建默认 Edge OAuth2 客户端（builtin 模式首次启动时）
func (db *DB) CreateDefaultOAuth2Client(secretCipher *security.ClientSecretCipher) error {
	// 检查是否已存在客户端
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM oauth2_clients").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check oauth2 clients: %w", err)
	}

	if count > 0 {
		logger.Info("OAuth2 clients already exist, skipping default client creation")
		return nil
	}

	// 生成随机 client_secret
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return fmt.Errorf("failed to generate client secret: %w", err)
	}
	rawSecret := fmt.Sprintf("%x", secretBytes)

	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(rawSecret), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash client secret: %w", err)
	}
	encryptedSecret, err := secretCipher.Encrypt(rawSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt client secret: %w", err)
	}

	insertSQL := `
	INSERT INTO oauth2_clients (client_id, client_secret_hash, client_secret_encrypted, client_type, allowed_scopes, description, enabled)
	VALUES ('edge-default', $1, $2, 'edge_node', 'edge:register edge:printer edge:heartbeat file:read', 'Default Edge client (auto-generated)', true)`

	if _, err := db.Exec(insertSQL, string(hashedSecret), encryptedSecret); err != nil {
		return fmt.Errorf("failed to create default oauth2 client: %w", err)
	}

	logger.Info("Default Edge OAuth2 client created", zap.String("client_id", "edge-default"))

	return nil
}

// generateRandomPassword 生成随机密码
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	password := make([]byte, length)
	for i := range password {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		password[i] = charset[num.Int64()]
	}
	return string(password)
}
