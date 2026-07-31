package websocket

import "time"

// 基础消息格式
type Message struct {
	Type      string      `json:"type"`
	NodeID    string      `json:"node_id"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// 上行消息类型
const (
	MsgTypeHeartbeat            = "edge_heartbeat"
	MsgTypeJobUpdate            = "job_update"
	MsgTypeSubmitPrintParams    = "submit_print_params"
	MsgTypeRequestUploadToken   = "request_upload_token" // 请求上传凭证
	MsgTypeTerminalSessionState = "terminal_session_state"
	MsgTypeAck                  = "ack" // 确认消息
)

// 下行指令类型
const (
	CmdTypePrintJob           = "print_job"
	CmdTypeConfigUpdate       = "config_update"
	CmdTypeReportStatus       = "report_status"
	CmdTypePreviewFile        = "preview_file"
	CmdTypeNodeState          = "node_state"
	CmdTypeError              = "error"        // 错误消息，用于通知 Edge 端操作失败
	CmdTypeUploadToken        = "upload_token" // 下发上传凭证
	CmdTypeJobUpdateAck       = "job_update_ack"
	CmdTypeTerminalOccupied   = "terminal_occupied" // 进门票已签发，一体机应遮挡二维码
	CmdTypePortalSessionReady = "portal_session_ready"
)

// PreviewFilePayload 文件预览请求载荷
type PreviewFilePayload struct {
	FileID                   string                 `json:"file_id"`
	FileURL                  string                 `json:"file_url"`
	FileName                 string                 `json:"file_name"`
	FileSize                 int64                  `json:"file_size"`
	FileType                 string                 `json:"file_type"`
	ContentHash              string                 `json:"content_hash"`
	PrintOptions             map[string]interface{} `json:"print_options,omitempty"`
	TerminalSessionID        string                 `json:"terminal_session_id,omitempty"`
	TerminalTicketHash       string                 `json:"terminal_ticket_hash,omitempty"`
	IntegrationRequestID     string                 `json:"integration_request_id,omitempty"`
	FileAccessToken          string                 `json:"file_access_token,omitempty"`            // 文件访问凭证
	FileAccessTokenExpiresAt *time.Time             `json:"file_access_token_expires_at,omitempty"` // 凭证过期时间
}

// SubmitPrintParamsPayload 提交打印参数载荷
type SubmitPrintParamsPayload struct {
	FileID               string                 `json:"file_id"`
	PrinterID            string                 `json:"printer_id"`
	Options              map[string]interface{} `json:"options"` // copies, color, duplex, paper_size, etc.
	TerminalSessionID    string                 `json:"terminal_session_id,omitempty"`
	TerminalTicketHash   string                 `json:"terminal_ticket_hash,omitempty"`
	IntegrationRequestID string                 `json:"integration_request_id,omitempty"`
}

// PrintJobPayload 打印任务指令载荷
type PrintJobPayload struct {
	JobID     string                 `json:"job_id"`
	FileID    string                 `json:"file_id"`
	FileURL   string                 `json:"file_url"`
	PrinterID string                 `json:"printer_id"`
	Options   map[string]interface{} `json:"options"`
}

type NodeEnabledPayload struct {
	Enabled bool `json:"enabled"`
}

// 指令消息格式
type Command struct {
	Type      string      `json:"type"`
	CommandID string      `json:"command_id"`
	MsgID     string      `json:"msg_id,omitempty"` // 用于通信层 ACK 的唯一消息 ID
	Timestamp time.Time   `json:"timestamp"`
	Target    string      `json:"target"` // edge_node_id 或 printer_id
	Data      interface{} `json:"data"`
}

// 指令确认响应
type CommandAck struct {
	Type      string    `json:"type"`
	CommandID string    `json:"command_id"`
	MsgID     string    `json:"msg_id,omitempty"` // 对应 Command 的 MsgID
	NodeID    string    `json:"node_id"`
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"` // accepted/rejected/processing
	Message   string    `json:"message"`
}

// 心跳数据
type HeartbeatData struct {
	SystemInfo SystemInfo             `json:"system_info"`
	Components map[string]interface{} `json:"components"`
}

type SystemInfo struct {
	CPUUsage       float64 `json:"cpu_usage"`
	MemoryUsage    float64 `json:"memory_usage"`
	DiskUsage      float64 `json:"disk_usage"`
	NetworkQuality string  `json:"network_quality"`
	Latency        int     `json:"latency"`
}

// 任务状态更新数据
type JobUpdateData struct {
	EventID              string  `json:"event_id,omitempty"`
	JobID                string  `json:"job_id"`
	Status               string  `json:"status"`
	ErrorCode            string  `json:"error_code"`
	ErrorMessage         *string `json:"error_message"`
	ImpressionsCompleted int     `json:"impressions_completed,omitempty"`
	SheetsCompleted      int     `json:"sheets_completed,omitempty"`
	QuotaConsumed        int     `json:"quota_consumed,omitempty"`
	TerminalSessionID    string  `json:"terminal_session_id,omitempty"`
	TerminalTicketHash   string  `json:"terminal_ticket_hash,omitempty"`
	IntegrationRequestID string  `json:"integration_request_id,omitempty"`
}

// JobUpdateAckPayload is sent only after Cloud has durably accepted or
// explicitly rejected an Edge terminal job-update event.
type JobUpdateAckPayload struct {
	EventID string `json:"event_id"`
	JobID   string `json:"job_id"`
	Status  string `json:"status"` // accepted/rejected
	Reason  string `json:"reason,omitempty"`
}

// 打印任务分发数据
type PrintJobData struct {
	JobID                    string     `json:"job_id"`
	Name                     string     `json:"name"`
	PrinterID                string     `json:"printer_id"`
	FilePath                 string     `json:"file_path,omitempty"`
	FileURL                  string     `json:"file_url,omitempty"`
	ContentHash              string     `json:"content_hash"`
	FileAccessToken          string     `json:"file_access_token,omitempty"`            // 文件URL一次性访问凭证
	FileAccessTokenExpiresAt *time.Time `json:"file_access_token_expires_at,omitempty"` // 下载凭证过期时间
	FileSize                 int64      `json:"file_size"`
	PageCount                int        `json:"page_count"`
	Copies                   int        `json:"copies"`
	PaperSize                string     `json:"paper_size"`
	ColorMode                string     `json:"color_mode"`
	DuplexMode               string     `json:"duplex_mode"`
	MaxRetries               int        `json:"max_retries"`
	TerminalSessionID        string     `json:"terminal_session_id,omitempty"`
	TerminalTicketHash       string     `json:"terminal_ticket_hash,omitempty"`
	IntegrationRequestID     string     `json:"integration_request_id,omitempty"`
}

// RequestUploadTokenPayload 请求上传凭证载荷 (Edge -> Cloud)
type RequestUploadTokenPayload struct {
	RequestID string `json:"request_id"`
	PrinterID string `json:"printer_id"` // 目标打印机ID
}

// TerminalSessionStateData is reported whenever the kiosk creates or clears a
// local interactive session. Empty fields explicitly mean Edge has no active
// session (including immediately after restart).
type TerminalSessionStateData struct {
	TerminalSessionID    string `json:"terminal_session_id"`
	TerminalTicketHash   string `json:"terminal_ticket_hash"`
	EntryType            string `json:"entry_type"`
	IntegrationRequestID string `json:"integration_request_id"`
}

// TerminalOccupiedPayload tells Edge a phone has entered via the current QR
// session so the kiosk should obscure the code until refresh or expiry.
type TerminalOccupiedPayload struct {
	TerminalSessionID  string    `json:"terminal_session_id"`
	TerminalTicketHash string    `json:"terminal_ticket_hash"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// PortalSessionReadyPayload intentionally carries only the one-time claim
// reference. Identity cookies and PRP access credentials never enter Cloud.
type PortalSessionReadyPayload struct {
	SitePortalCode    string    `json:"site_portal_code"`
	ClaimBaseURL      string    `json:"claim_base_url"`
	ClaimCode         string    `json:"claim_code"`
	TerminalSessionID string    `json:"terminal_session_id"`
	CloudUserID       string    `json:"cloud_user_id"`
	ExpiresAt         time.Time `json:"expires_at"`
}

// UploadTokenResponsePayload 上传凭证响应载荷 (Cloud -> Edge)
type UploadTokenResponsePayload struct {
	RequestID string    `json:"request_id"`
	Token     string    `json:"token"`      // 一次性上传凭证
	ExpiresAt time.Time `json:"expires_at"` // 过期时间
	UploadURL string    `json:"upload_url"` // API上传URL（用于程序化上传，POST请求）
	WebURL    string    `json:"web_url"`    // Web上传页面URL（用于生成二维码/链接，GET请求）
	NodeID    string    `json:"node_id"`    // 节点ID
	PrinterID string    `json:"printer_id"` // 打印机ID
}
