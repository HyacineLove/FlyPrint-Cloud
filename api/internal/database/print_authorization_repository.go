package database

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"fly-print-cloud/api/internal/business"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"

	"go.uber.org/zap"
)

var (
	ErrPrintAuthorizationInvalid            = errors.New("invalid print authorization")
	ErrPrintAuthorizationSessionInvalid     = errors.New("print authorization session invalid")
	ErrPrintAuthorizationPortalMismatch     = errors.New("print authorization portal mismatch")
	ErrPrintAuthorizationUserDisabled       = errors.New("print authorization user disabled")
	ErrPrintAuthorizationConflict           = errors.New("print authorization confirmation conflict")
	ErrPrintQuotaInsufficient               = errors.New("print quota insufficient")
	ErrPrintAuthorizationPrinterNotFound    = errors.New("print authorization printer not found")
	ErrPrintAuthorizationPrinterNotOwned    = errors.New("print authorization printer not owned")
	ErrPrintAuthorizationPrinterUnavailable = errors.New("print authorization printer unavailable")
	ErrPrintAuthorizationPrinterUnsupported = errors.New("print authorization printer capability unsupported")
)

const authorizationPrinterStatusFreshness = 90 * time.Second

type PrintAuthorizationRepository struct {
	db *DB
}

func NewPrintAuthorizationRepository(db *DB) *PrintAuthorizationRepository {
	return &PrintAuthorizationRepository{db: db}
}

// PrintAuthorizationRequestHash 固化幂等请求内容；时间不参与哈希。
func PrintAuthorizationRequestHash(input models.PrintAuthorizationInput) string {
	payload := struct {
		NodeID            string `json:"node_id"`
		ConfirmationID    string `json:"confirmation_id"`
		TerminalSessionID string `json:"terminal_session_id"`
		SitePortalCode    string `json:"site_portal_code"`
		LocalFileID       string `json:"local_file_id"`
		FileDisplayName   string `json:"file_display_name"`
		PageCount         int    `json:"page_count"`
		Copies            int    `json:"copies"`
		PaperSize         string `json:"paper_size"`
		ColorMode         string `json:"color_mode"`
		DuplexMode        string `json:"duplex_mode"`
		PrinterID         string `json:"printer_id"`
	}{
		NodeID: input.NodeID, ConfirmationID: input.ConfirmationID,
		TerminalSessionID: input.TerminalSessionID, SitePortalCode: input.SitePortalCode,
		LocalFileID: input.LocalFileID, FileDisplayName: input.FileDisplayName,
		PageCount: input.PageCount, Copies: input.Copies, PaperSize: input.PaperSize,
		ColorMode: input.ColorMode, DuplexMode: input.DuplexMode, PrinterID: input.PrinterID,
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func validPrintAuthorizationInput(input models.PrintAuthorizationInput) bool {
	return strings.TrimSpace(input.NodeID) != "" &&
		strings.TrimSpace(input.ConfirmationID) != "" &&
		len(input.ConfirmationID) <= 128 &&
		strings.TrimSpace(input.TerminalSessionID) != "" &&
		strings.TrimSpace(input.SitePortalCode) != "" &&
		strings.TrimSpace(input.LocalFileID) != "" &&
		len(input.LocalFileID) <= 128 &&
		strings.TrimSpace(input.FileDisplayName) != "" &&
		len(input.FileDisplayName) <= 200 &&
		strings.TrimSpace(input.PrinterID) != "" &&
		strings.TrimSpace(input.PaperSize) != "" &&
		!input.Now.IsZero()
}

func (r *PrintAuthorizationRepository) Authorize(input models.PrintAuthorizationInput) (*models.PrintAuthorizationResult, error) {
	if !validPrintAuthorizationInput(input) {
		return nil, ErrPrintAuthorizationInvalid
	}
	_, reservedQuota, err := business.QuotaUsage(
		input.PageCount, input.Copies, input.DuplexMode, input.ColorMode,
	)
	if err != nil {
		return nil, ErrPrintAuthorizationInvalid
	}
	requestHash := PrintAuthorizationRequestHash(input)

	tx, err := r.db.BeginTx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var boundPortal, cloudUserID, userStatus, displayName string
	var boundPortalEnabled bool
	var quotaBalance int
	err = tx.QueryRow(`SELECT session.site_portal_code,session.cloud_user_id::text,
		user_account.status,user_account.print_quota_balance,
		portal.enabled,
		COALESCE(NULLIF(identity.display_name,''),NULLIF(user_account.username,''),'')
		FROM edge_terminal_sessions session
		JOIN users user_account ON user_account.id=session.cloud_user_id
		JOIN site_portals portal ON portal.code=session.site_portal_code
		LEFT JOIN external_identities identity
			ON identity.site_portal_code=session.site_portal_code
			AND identity.cloud_user_id=session.cloud_user_id
		WHERE session.node_id=$1 AND session.terminal_session_id=$2
		FOR UPDATE OF session,user_account`,
		input.NodeID, input.TerminalSessionID,
	).Scan(&boundPortal, &cloudUserID, &userStatus, &quotaBalance, &boundPortalEnabled, &displayName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPrintAuthorizationSessionInvalid
	}
	if err != nil {
		return nil, err
	}
	if boundPortal != input.SitePortalCode {
		return nil, ErrPrintAuthorizationPortalMismatch
	}
	if !boundPortalEnabled {
		return nil, ErrPrintAuthorizationPortalMismatch
	}
	if userStatus != "active" {
		return nil, ErrPrintAuthorizationUserDisabled
	}

	var existingJobID, existingHash string
	var existingReserved int
	err = tx.QueryRow(`SELECT id::text,authorization_request_hash,quota_reserved
		FROM print_jobs
		WHERE edge_node_id=$1 AND confirmation_id=$2
		FOR UPDATE`, input.NodeID, input.ConfirmationID).
		Scan(&existingJobID, &existingHash, &existingReserved)
	if err == nil {
		if existingHash != requestHash {
			return nil, ErrPrintAuthorizationConflict
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &models.PrintAuthorizationResult{
			JobID: existingJobID, ReservedQuota: existingReserved, QuotaBalance: quotaBalance,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var printerNodeID, printerStatus, nodeStatus string
	var printerEnabled, nodeEnabled bool
	var printerStatusReceivedAt *time.Time
	var capabilitiesJSON []byte
	err = tx.QueryRow(`SELECT printer.edge_node_id,printer.enabled,printer.status,
		printer.status_received_at,node.enabled,node.status,printer.capabilities
		FROM printers printer
		JOIN edge_nodes node ON node.id=printer.edge_node_id
		WHERE printer.id=$1::uuid
			AND printer.deleted_at IS NULL
			AND node.deleted_at IS NULL
		FOR UPDATE OF printer,node`, input.PrinterID).
		Scan(
			&printerNodeID, &printerEnabled, &printerStatus,
			&printerStatusReceivedAt, &nodeEnabled, &nodeStatus, &capabilitiesJSON,
		)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPrintAuthorizationPrinterNotFound
	}
	if err != nil {
		return nil, err
	}
	if printerNodeID != input.NodeID {
		return nil, ErrPrintAuthorizationPrinterNotOwned
	}
	if !printerEnabled || !nodeEnabled || nodeStatus != "online" ||
		printerStatus != "idle" || printerStatusReceivedAt == nil ||
		input.Now.Sub(*printerStatusReceivedAt) > authorizationPrinterStatusFreshness {
		return nil, ErrPrintAuthorizationPrinterUnavailable
	}
	var capabilities models.PrinterCapabilities
	if err := json.Unmarshal(capabilitiesJSON, &capabilities); err != nil {
		// DB 中能力数据损坏时降级为明确拒绝并告警，而不是 500。
		logger.Warn("Printer capabilities JSON corrupted; rejecting authorization",
			zap.String("printer_id", input.PrinterID), zap.Error(err))
		return nil, ErrPrintAuthorizationPrinterUnsupported
	}
	if input.ColorMode == "color" && !capabilities.ColorSupport {
		return nil, ErrPrintAuthorizationPrinterUnsupported
	}
	if input.DuplexMode != "simplex" && !capabilities.DuplexSupport {
		return nil, ErrPrintAuthorizationPrinterUnsupported
	}
	if len(capabilities.PaperSizes) > 0 {
		paperSupported := false
		for _, paperSize := range capabilities.PaperSizes {
			if paperSize == input.PaperSize {
				paperSupported = true
				break
			}
		}
		if !paperSupported {
			return nil, ErrPrintAuthorizationPrinterUnsupported
		}
	}
	if quotaBalance < reservedQuota {
		return nil, ErrPrintQuotaInsufficient
	}

	result, err := tx.Exec(`UPDATE users
		SET print_quota_balance=print_quota_balance-$2
		WHERE id=$1::uuid AND print_quota_balance>=$2`, cloudUserID, reservedQuota)
	if err != nil {
		return nil, err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		return nil, ErrPrintQuotaInsufficient
	}

	var jobID string
	err = tx.QueryRow(`INSERT INTO print_jobs (
		name,status,printer_id,user_id,user_name,file_path,file_url,content_hash,
		page_count,copies,paper_size,color_mode,duplex_mode,retry_count,max_retries,
		edge_node_id,site_portal_code,terminal_session_id,confirmation_id,
		authorization_request_hash,local_file_id,quota_reserved,created_at,updated_at
	) VALUES (
		$1,'pending',$2::uuid,$3,$4,'','','',
		$5,$6,$7,$8,$9,0,0,
		$10,$11,$12,$13,$14,$15,$16,$17,$17
	) RETURNING id::text`,
		input.FileDisplayName, input.PrinterID, cloudUserID, displayName,
		input.PageCount, input.Copies, input.PaperSize, input.ColorMode, input.DuplexMode,
		input.NodeID, boundPortal, input.TerminalSessionID, input.ConfirmationID,
		requestHash, input.LocalFileID, reservedQuota, input.Now,
	).Scan(&jobID)
	if err != nil {
		return nil, err
	}

	remainingBalance := quotaBalance - reservedQuota
	if _, err = tx.Exec(`INSERT INTO print_quota_transactions
		(user_id,print_job_id,transaction_type,delta,balance_after)
		VALUES ($1::uuid,$2::uuid,'authorization_reserve',$3,$4)`,
		cloudUserID, jobID, -reservedQuota, remainingBalance,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.PrintAuthorizationResult{
		JobID: jobID, ReservedQuota: reservedQuota, QuotaBalance: remainingBalance,
	}, nil
}
