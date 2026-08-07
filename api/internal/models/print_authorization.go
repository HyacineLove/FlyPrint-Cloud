package models

import "time"

// PrintAuthorizationInput 是 Edge 在一次用户确认中提交的非秘密审计信息。
type PrintAuthorizationInput struct {
	NodeID            string
	ConfirmationID    string
	TerminalSessionID string
	SitePortalCode    string
	LocalFileID       string
	FileDisplayName   string
	PageCount         int
	Copies            int
	PaperSize         string
	Orientation       string
	ScalePercent      int
	ColorMode         string
	DuplexMode        string
	PrinterID         string
	Now               time.Time
}

type PrintAuthorizationResult struct {
	JobID         string `json:"job_id"`
	ReservedQuota int    `json:"reserved_quota"`
	QuotaBalance  int    `json:"quota_balance"`
}
