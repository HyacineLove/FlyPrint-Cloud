package models

import (
	"time"
)

// EdgeNode Edge节点
type EdgeNode struct {
	ID                      string     `json:"id"`
	LoginSource             string     `json:"login_source"`
	Name                    string     `json:"name"`            // Edge 上报的原始名称
	Alias                   string     `json:"alias,omitempty"` // Cloud 运维别名
	RegistrationState       string     `json:"registration_state"`
	ConnectionStatus        string     `json:"connection_status"` // online/unstable/offline
	HealthStatus            string     `json:"health_status"`     // healthy/degraded/critical/unknown
	HealthReasonCode        string     `json:"health_reason_code,omitempty"`
	HealthMessage           string     `json:"health_message,omitempty"`
	Enabled                 bool       `json:"enabled"` // 云端启用/禁用状态
	Version                 string     `json:"version"`
	LastHeartbeat           time.Time  `json:"last_heartbeat"`
	PrinterStatusReceivedAt *time.Time `json:"printer_status_received_at,omitempty"`
	DeletedAt               *time.Time `json:"deleted_at,omitempty"` // 软删除时间

	// 位置信息
	Location  string   `json:"location"`            // 地理位置描述
	Latitude  *float64 `json:"latitude,omitempty"`  // 纬度
	Longitude *float64 `json:"longitude,omitempty"` // 经度

	// 网络信息
	IPAddress        *string `json:"ip_address,omitempty"` // IP地址
	MACAddress       string  `json:"mac_address"`          // MAC地址
	NetworkInterface string  `json:"network_interface"`    // 网络接口

	// 系统信息
	OSVersion  string `json:"os_version"`  // 操作系统版本
	CPUInfo    string `json:"cpu_info"`    // CPU信息
	MemoryInfo string `json:"memory_info"` // 内存信息
	DiskInfo   string `json:"disk_info"`   // 磁盘信息

	// 连接信息
	ConnectionQuality string                 `json:"connection_quality"` // 连接质量
	Latency           int                    `json:"latency"`            // 延迟(ms)
	CPUUsage          float64                `json:"cpu_usage"`
	MemoryUsage       float64                `json:"memory_usage"`
	DiskUsage         float64                `json:"disk_usage"`
	Components        map[string]interface{} `json:"components,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Printer 打印机
type Printer struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`         // Edge node范围内唯一的技术名称
	DisplayName         string     `json:"display_name"` // 用户友好的显示名称
	Model               string     `json:"model"`
	SerialNumber        string     `json:"serial_number"`
	PrinterStatus       string     `json:"printer_status"`
	StatusObservedSince *time.Time `json:"status_observed_since,omitempty"`
	SourceObservedAt    *time.Time `json:"source_observed_at,omitempty"`
	StatusReceivedAt    *time.Time `json:"status_received_at,omitempty"`
	Enabled             bool       `json:"enabled"` // 云端启用/禁用状态

	// 硬件信息
	FirmwareVersion string `json:"firmware_version"` // 固件版本
	PortInfo        string `json:"port_info"`        // 端口信息

	// 网络信息
	IPAddress     *string `json:"ip_address"`     // IP地址 (修复：改为指针类型以处理NULL)
	MACAddress    string  `json:"mac_address"`    // MAC地址
	NetworkConfig string  `json:"network_config"` // 网络配置

	// 能力信息
	Capabilities PrinterCapabilities `json:"capabilities"`

	// 关联信息
	EdgeNodeID string `json:"edge_node_id"` // 关联Edge Node

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PrinterCapabilities 打印机能力
type PrinterCapabilities struct {
	PaperSizes    []string `json:"paper_sizes"`    // 支持的纸张尺寸
	ColorSupport  bool     `json:"color_support"`  // 是否支持彩色
	DuplexSupport bool     `json:"duplex_support"` // 是否支持双面
	Resolution    string   `json:"resolution"`     // 分辨率
	PrintSpeed    string   `json:"print_speed"`    // 打印速度
	MediaTypes    []string `json:"media_types"`    // 支持的介质类型
}

// PrintJob 打印任务
type PrintJob struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // pending/dispatched/processing/completed/failed/canceled/unconfirmed

	// 关联信息
	PrinterID   string `json:"printer_id"`
	PrinterName string `json:"printer_name,omitempty"` // 打印机名称 (非DB字段，仅用于API返回或内部逻辑)
	EdgeNodeID  string `json:"edge_node_id,omitempty"` // 所属节点ID（查询时填充）
	NodeName    string `json:"node_name,omitempty"`    // 节点显示名称（别名优先）
	UserID      string `json:"user_id"`                // 提交用户（内部兼容主键或第三方标识）
	UserName    string `json:"user_name"`              // 提交用户名
	UserEmail   string `json:"user_email,omitempty"`   // 提交用户邮箱（官方用户的稳定业务标识）

	// 任务信息
	FilePath    string `json:"file_path"`    // 文件路径（本地文件）
	FileURL     string `json:"file_url"`     // 文件URL（第三方API使用）
	ContentHash string `json:"content_hash"` // file sha256 content hash
	FileSize    int64  `json:"file_size"`    // 文件大小
	PageCount   int    `json:"page_count"`   // 页数
	Copies      int    `json:"copies"`       // 份数

	// 打印设置
	PaperSize  string `json:"paper_size"`
	ColorMode  string `json:"color_mode"`  // color/grayscale
	DuplexMode string `json:"duplex_mode"` // single/duplex

	// 执行信息
	StartTime    *time.Time `json:"start_time"`
	EndTime      *time.Time `json:"end_time"`
	ErrorMessage string     `json:"error_message"`
	ErrorCode    string     `json:"error_code,omitempty"`

	// 重试信息
	RetryCount int `json:"retry_count"`
	MaxRetries int `json:"max_retries"`

	// Terminal context is populated only for integration jobs and is carried to
	// Edge without adding third-party fields to the print_jobs table.
	TerminalSessionID    string `json:"terminal_session_id,omitempty"`
	TerminalTicketHash   string `json:"terminal_ticket_hash,omitempty"`
	IntegrationRequestID string `json:"integration_request_id,omitempty"`
	InitiatorName        string `json:"initiator_name,omitempty"`
	InitiatorCode        string `json:"initiator_code,omitempty"` // integration provider code; empty = official/main

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OperationalAlert struct {
	ID              string                 `json:"id"`
	ResourceType    string                 `json:"resource_type"`
	ResourceID      string                 `json:"resource_id"`
	NodeID          string                 `json:"node_id,omitempty"`
	PrinterID       string                 `json:"printer_id,omitempty"`
	JobID           string                 `json:"job_id,omitempty"`
	ReasonCode      string                 `json:"reason_code"`
	Category        string                 `json:"category"`
	Title           string                 `json:"title"`
	Status          string                 `json:"status"`
	Details         map[string]interface{} `json:"details,omitempty"`
	OccurrenceCount int                    `json:"occurrence_count"`
	FirstSeenAt     time.Time              `json:"first_seen_at"`
	LastSeenAt      time.Time              `json:"last_seen_at"`
	ResolvedAt      *time.Time             `json:"resolved_at,omitempty"`
	DurationSeconds int64                  `json:"duration_seconds,omitempty"`
	NodeName        string                 `json:"node_name,omitempty"`
	PrinterName     string                 `json:"printer_name,omitempty"`
	JobName         string                 `json:"job_name,omitempty"`
}

// User 用户
type User struct {
	ID                string    `json:"id"`
	Username          string    `json:"username"`              // 用户名
	Email             string    `json:"email"`                 // 邮箱
	PasswordHash      string    `json:"-"`                     // 密码哈希 (不返回)
	ExternalID        *string   `json:"external_id,omitempty"` // OAuth2 外部ID
	Role              string    `json:"role"`                  // 角色: admin/operator/viewer
	Status            string    `json:"status"`                // 状态: active/inactive
	PrintQuotaBalance int       `json:"print_quota_balance"`   // Cloud 管理的剩余打印额度点数
	LastLogin         time.Time `json:"last_login"`            // 最后登录时间
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}
