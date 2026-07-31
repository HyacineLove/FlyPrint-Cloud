package operations

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"fly-print-cloud/api/internal/business"
	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"
)

const statusFreshness = 90 * time.Second

type HeartbeatSnapshot struct {
	CPUUsage, MemoryUsage, DiskUsage float64
	NetworkQuality                   string
	Latency                          int
	Components                       map[string]interface{}
}

type PrinterSnapshot struct {
	PrinterID, PrinterStatus string
	SourceObservedAt         *time.Time
}

// TerminalJobUpdate is the durable Edge-to-Cloud result contract. EventID and
// PayloadHash let Cloud acknowledge a retried report without applying it twice.
type TerminalJobUpdate struct {
	EventID, PayloadHash, NodeID, JobID, Status, ErrorCode, ErrorMessage string
	ImpressionsCompleted, SheetsCompleted                                int
	QuotaConsumed                                                        int
}

type TerminalJobUpdateResult struct {
	Accepted bool
	Changed  bool
	Reason   string
}

type StatusService struct {
	db          *database.DB
	alerts      *database.OperationalAlertRepository
	coordinator *AlertCoordinator
}

func NewStatusService(db *database.DB, alerts *database.OperationalAlertRepository) *StatusService {
	return &StatusService{
		db: db, alerts: alerts, coordinator: NewAlertCoordinator(alerts),
	}
}

type nodeHealthReason struct {
	code, message, level string
}

func heartbeatReasons(h HeartbeatSnapshot) []nodeHealthReason {
	reasons := []nodeHealthReason{}
	if h.DiskUsage >= 95 {
		reasons = append(reasons, nodeHealthReason{"disk_usage_critical", "磁盘使用率已达到 95%", "critical"})
	} else if h.DiskUsage >= 90 {
		reasons = append(reasons, nodeHealthReason{"disk_usage_high", "磁盘使用率已达到 90%", "degraded"})
	}
	if h.MemoryUsage >= 95 {
		reasons = append(reasons, nodeHealthReason{"memory_usage_critical", "内存使用率已达到 95%", "critical"})
	} else if h.MemoryUsage >= 90 {
		reasons = append(reasons, nodeHealthReason{"memory_usage_high", "内存使用率持续偏高", "degraded"})
	}
	if h.NetworkQuality == "poor" {
		reasons = append(reasons, nodeHealthReason{"network_quality_poor", "网络质量持续较差", "degraded"})
	}
	if componentDegraded(h.Components, "document_conversion") {
		reasons = append(reasons, nodeHealthReason{"libreoffice_unavailable", "Office 文档转换组件不可用", "degraded"})
	}
	return reasons
}

func (s *StatusService) ApplyHeartbeat(nodeID string, h HeartbeatSnapshot) error {
	reasons := heartbeatReasons(h)
	health, reason, message := "healthy", "", ""
	for _, item := range reasons {
		if reason == "" || health != "critical" && item.level == "critical" {
			health, reason, message = item.level, item.code, item.message
		}
	}
	componentsJSON, _ := json.Marshal(h.Components)
	tx, err := s.db.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE edge_nodes SET status='online',
		health_status=CASE WHEN health_reason_code='printer_status_sync_interrupted' THEN health_status ELSE $2 END,
		health_reason_code=CASE WHEN health_reason_code='printer_status_sync_interrupted' THEN health_reason_code ELSE NULLIF($3,'') END,
		health_message=CASE WHEN health_reason_code='printer_status_sync_interrupted' THEN health_message ELSE NULLIF($4,'') END,
		last_heartbeat=CURRENT_TIMESTAMP,cpu_usage=$5,memory_usage=$6,disk_usage=$7,
		connection_quality=$8,latency=$9,components=$10 WHERE id=$1 AND deleted_at IS NULL`,
		nodeID, health, reason, message, h.CPUUsage, h.MemoryUsage, h.DiskUsage, h.NetworkQuality, h.Latency, componentsJSON)
	if err != nil {
		return err
	}
	var enabled bool
	if err = tx.QueryRow(`SELECT enabled FROM edge_nodes WHERE id=$1`, nodeID).Scan(&enabled); err != nil {
		return err
	}
	active := []string{}
	for _, item := range reasons {
		policy, ok := alertPolicy(item.code)
		if enabled && ok {
			active = append(active, item.code)
			if err = s.alerts.OpenTx(tx, "node", nodeID, nodeID, "", "", item.code, policy.AlertSpec, map[string]interface{}{"message": item.message}); err != nil {
				return err
			}
		}
	}
	managed := []string{"disk_usage_critical", "memory_usage_critical"}
	if err = s.alerts.ResolveReasonsTx(tx, "node", nodeID, managed, active); err != nil {
		return err
	}
	return tx.Commit()
}

func componentDegraded(components map[string]interface{}, name string) bool {
	v, ok := components[name].(map[string]interface{})
	return ok && (v["status"] == "degraded" || v["status"] == "critical")
}

func (s *StatusService) ApplyPrinterSnapshot(nodeID string, p PrinterSnapshot) error {
	tx, err := s.db.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE printers SET
		status_observed_since=CASE WHEN status IS NOT DISTINCT FROM $3 THEN COALESCE(status_observed_since,CURRENT_TIMESTAMP) ELSE CURRENT_TIMESTAMP END,
		status=$3,
		source_observed_at=$4,status_received_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP
		WHERE id=$1::uuid AND edge_node_id=$2 AND deleted_at IS NULL`,
		p.PrinterID, nodeID, p.PrinterStatus, p.SourceObservedAt)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("printer not found on node")
	}
	_, err = tx.Exec(`UPDATE edge_nodes SET printer_status_received_at=CURRENT_TIMESTAMP,
		health_status=CASE WHEN health_reason_code='printer_status_sync_interrupted' THEN 'healthy' ELSE health_status END,
		health_reason_code=CASE WHEN health_reason_code='printer_status_sync_interrupted' THEN NULL ELSE health_reason_code END,
		health_message=CASE WHEN health_reason_code='printer_status_sync_interrupted' THEN NULL ELSE health_message END WHERE id=$1`, nodeID)
	if err != nil {
		return err
	}
	if err = s.alerts.ResolveReasonsTx(tx, "node", nodeID, []string{"printer_status_sync_interrupted"}, nil); err != nil {
		return err
	}
	var printerEnabled, nodeEnabled bool
	var statusSince *time.Time
	if err = tx.QueryRow(`SELECT p.enabled,n.enabled,p.status_observed_since FROM printers p
		JOIN edge_nodes n ON n.id=p.edge_node_id WHERE p.id=$1::uuid`, p.PrinterID).
		Scan(&printerEnabled, &nodeEnabled, &statusSince); err != nil {
		return err
	}
	statusObservedAt := time.Time{}
	if statusSince != nil {
		statusObservedAt = *statusSince
	}
	if err = s.coordinator.ReconcilePrinter(tx, p.PrinterID, nodeID, p.PrinterStatus, statusObservedAt, printerEnabled && nodeEnabled, time.Now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *StatusService) ApplyJobResult(jobID, nodeID, printerID, status, errorCode string, details map[string]interface{}) error {
	if status != "processing" && status != "completed" && status != "failed" && status != "canceled" && status != "unconfirmed" {
		return fmt.Errorf("unsupported job status %q", status)
	}
	errorMessage, _ := details["message"].(string)
	tx, err := s.db.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE print_jobs SET status=$2::varchar,
		error_code=CASE WHEN $2::varchar IN ('processing','completed') THEN NULL ELSE NULLIF($3::varchar,'') END,
		error_message=CASE WHEN $2::varchar IN ('processing','completed') THEN NULL WHEN NULLIF($4::varchar,'') IS NULL THEN error_message ELSE $4::varchar END,
		updated_at=CURRENT_TIMESTAMP,end_time=CASE WHEN $2::varchar IN ('completed','failed','canceled','unconfirmed')
		THEN COALESCE(end_time,CURRENT_TIMESTAMP) ELSE end_time END
		WHERE id=$1::uuid AND (
			status NOT IN ('completed','failed','canceled','unconfirmed') OR
			(status='unconfirmed' AND error_code='dispatch_ack_timeout' AND $2::varchar IN ('completed','failed','canceled','unconfirmed'))
		)`, jobID, status, errorCode, errorMessage)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return tx.Commit()
	}
	if status == "unconfirmed" {
		if err = s.coordinator.OpenPrinterUnconfirmed(tx, printerID, nodeID, jobID, errorCode, details); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyTerminalJobUpdate atomically validates an Edge terminal report, applies
// the monotonic job transition, and records its receipt before an ACK is sent.
func (s *StatusService) ApplyTerminalJobUpdate(receipts *database.EdgeJobUpdateReceiptRepository, update TerminalJobUpdate) (TerminalJobUpdateResult, error) {
	if receipts == nil {
		return TerminalJobUpdateResult{}, fmt.Errorf("Edge job-update receipt repository unavailable")
	}
	if update.EventID == "" || update.PayloadHash == "" || update.NodeID == "" || update.JobID == "" {
		return TerminalJobUpdateResult{Reason: "invalid_terminal_update"}, nil
	}
	if update.Status != "completed" && update.Status != "failed" && update.Status != "canceled" && update.Status != "unconfirmed" {
		return TerminalJobUpdateResult{Reason: "unsupported_job_status"}, nil
	}

	tx, err := s.db.BeginTx()
	if err != nil {
		return TerminalJobUpdateResult{}, err
	}
	defer tx.Rollback()

	if existing, err := receipts.GetTx(tx, update.EventID); err != nil {
		return TerminalJobUpdateResult{}, err
	} else if existing != nil {
		if existing.NodeID == update.NodeID && existing.JobID == update.JobID && existing.Status == update.Status && existing.PayloadHash == update.PayloadHash {
			return TerminalJobUpdateResult{Accepted: true}, nil
		}
		return TerminalJobUpdateResult{Reason: "event_id_conflict"}, nil
	}

	var currentStatus, currentErrorCode, printerID, ownerNodeID, userID, colorMode string
	var duplexMode string
	var quotaReserved, pageCount, copies int
	var quotaConsumed, impressionsCompleted, sheetsCompleted sql.NullInt64
	err = tx.QueryRow(`SELECT j.status,COALESCE(j.error_code,''),j.printer_id::text,p.edge_node_id,
		COALESCE(j.user_id::text,''),j.quota_reserved,j.quota_consumed,
		j.impressions_completed,j.sheets_completed,j.color_mode,
		j.page_count,j.copies,j.duplex_mode
		FROM print_jobs j JOIN printers p ON p.id=j.printer_id
		WHERE j.id=$1::uuid FOR UPDATE`, update.JobID).
		Scan(
			&currentStatus, &currentErrorCode, &printerID, &ownerNodeID,
			&userID, &quotaReserved, &quotaConsumed,
			&impressionsCompleted, &sheetsCompleted, &colorMode,
			&pageCount, &copies, &duplexMode,
		)
	if err == sql.ErrNoRows {
		return TerminalJobUpdateResult{Reason: "job_not_found"}, nil
	}
	if err != nil {
		return TerminalJobUpdateResult{}, err
	}
	if ownerNodeID != update.NodeID {
		return TerminalJobUpdateResult{Reason: "job_node_mismatch"}, nil
	}

	// A result may supersede only the explicit dispatch-ACK uncertainty. All
	// other terminal states are immutable, including print-time uncertainty.
	allowFromUnconfirmed := currentStatus == "unconfirmed" && currentErrorCode == "dispatch_ack_timeout"
	if currentStatus == "completed" || currentStatus == "failed" || currentStatus == "canceled" || (currentStatus == "unconfirmed" && !allowFromUnconfirmed) {
		if currentStatus != update.Status {
			return TerminalJobUpdateResult{Reason: "job_status_conflict"}, nil
		}
		if quotaReserved > 0 && currentStatus != "unconfirmed" &&
			(!quotaConsumed.Valid ||
				impressionsCompleted.Int64 != int64(update.ImpressionsCompleted) ||
				sheetsCompleted.Int64 != int64(update.SheetsCompleted) ||
				quotaConsumed.Int64 != int64(update.QuotaConsumed)) {
			return TerminalJobUpdateResult{Reason: "print_usage_conflict"}, nil
		}
		if err := receipts.CreateTx(tx, database.EdgeJobUpdateReceipt{EventID: update.EventID, NodeID: update.NodeID, JobID: update.JobID, Status: update.Status, PayloadHash: update.PayloadHash}); err != nil {
			return TerminalJobUpdateResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return TerminalJobUpdateResult{}, err
		}
		return TerminalJobUpdateResult{Accepted: true}, nil
	}

	_, err = tx.Exec(`UPDATE print_jobs SET status=$2::varchar,
		error_code=CASE WHEN $2::varchar='completed' THEN NULL ELSE NULLIF($3::varchar,'') END,
		error_message=CASE WHEN $2::varchar='completed' THEN NULL WHEN NULLIF($4::varchar,'') IS NULL THEN error_message ELSE $4::varchar END,
		updated_at=CURRENT_TIMESTAMP,end_time=COALESCE(end_time,CURRENT_TIMESTAMP)
		WHERE id=$1::uuid`, update.JobID, update.Status, update.ErrorCode, update.ErrorMessage)
	if err != nil {
		return TerminalJobUpdateResult{}, err
	}
	if quotaReserved > 0 && update.Status != "unconfirmed" {
		expectedSheets, expectedConsumed, usageErr := business.SettledQuotaUsage(
			pageCount, copies, update.ImpressionsCompleted, duplexMode, colorMode,
		)
		if userID == "" || usageErr != nil ||
			update.SheetsCompleted != expectedSheets ||
			update.QuotaConsumed != expectedConsumed ||
			(update.Status == "completed" && update.QuotaConsumed != quotaReserved) {
			return TerminalJobUpdateResult{Reason: "invalid_print_usage"}, nil
		}
		consumed := update.QuotaConsumed
		if consumed > quotaReserved {
			return TerminalJobUpdateResult{Reason: "print_usage_exceeds_reservation"}, nil
		}
		if _, err = tx.Exec(`UPDATE print_jobs SET
			impressions_completed=$2,sheets_completed=$3,quota_consumed=$4
			WHERE id=$1::uuid`,
			update.JobID, update.ImpressionsCompleted, update.SheetsCompleted, consumed,
		); err != nil {
			return TerminalJobUpdateResult{}, err
		}
		refund := quotaReserved - consumed
		if refund > 0 {
			var balanceAfter int
			if err = tx.QueryRow(`UPDATE users SET print_quota_balance=print_quota_balance+$2
				WHERE id=$1::uuid RETURNING print_quota_balance`,
				userID, refund,
			).Scan(&balanceAfter); err != nil {
				return TerminalJobUpdateResult{}, err
			}
			if _, err = tx.Exec(`INSERT INTO print_quota_transactions
				(user_id,print_job_id,transaction_type,delta,balance_after)
				VALUES ($1::uuid,$2::uuid,'settlement_refund',$3,$4)`,
				userID, update.JobID, refund, balanceAfter,
			); err != nil {
				return TerminalJobUpdateResult{}, err
			}
		}
	}
	if update.Status == "unconfirmed" {
		if err = s.coordinator.OpenPrinterUnconfirmed(tx, printerID, update.NodeID, update.JobID, update.ErrorCode, map[string]interface{}{"message": update.ErrorMessage}); err != nil {
			return TerminalJobUpdateResult{}, err
		}
	}
	if err = receipts.CreateTx(tx, database.EdgeJobUpdateReceipt{EventID: update.EventID, NodeID: update.NodeID, JobID: update.JobID, Status: update.Status, PayloadHash: update.PayloadHash}); err != nil {
		return TerminalJobUpdateResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return TerminalJobUpdateResult{}, err
	}
	return TerminalJobUpdateResult{Accepted: true, Changed: true}, nil
}

func (s *StatusService) ApplyDispatchUnconfirmed(jobID, nodeID, printerID string) error {
	tx, err := s.db.BeginTx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE print_jobs SET status='unconfirmed',error_code='dispatch_ack_timeout',
		error_message='无法确认边缘节点是否已接收任务',end_time=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP
		WHERE id=$1::uuid AND status='pending'`, jobID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed > 0 {
		if err = s.coordinator.OpenPrinterUnconfirmed(tx, printerID, nodeID, jobID, "dispatch_ack_timeout", nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *StatusService) MarkUnstable(nodeID string) error {
	_, err := s.db.Exec(`UPDATE edge_nodes SET status='unstable' WHERE id=$1 AND status='online' AND deleted_at IS NULL`, nodeID)
	return err
}

func (s *StatusService) Sweep(now time.Time) error {
	rows, err := s.db.Query(`SELECT id,status,COALESCE(last_heartbeat,created_at),printer_status_received_at,created_at,enabled,
		(SELECT COUNT(*) FROM printers p WHERE p.edge_node_id=edge_nodes.id AND p.deleted_at IS NULL)
		FROM edge_nodes WHERE deleted_at IS NULL`)
	if err != nil {
		return err
	}
	type nodeRow struct {
		id, status         string
		heartbeat, created time.Time
		printerSync        *time.Time
		enabled            bool
		printerCount       int
	}
	list := []nodeRow{}
	for rows.Next() {
		var item nodeRow
		if err = rows.Scan(&item.id, &item.status, &item.heartbeat, &item.printerSync, &item.created, &item.enabled, &item.printerCount); err != nil {
			rows.Close()
			return err
		}
		list = append(list, item)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, item := range list {
		heartbeatAge := now.Sub(item.heartbeat)
		connection := "online"
		if heartbeatAge >= 90*time.Second {
			connection = "offline"
		} else if heartbeatAge > 30*time.Second || item.status == "unstable" {
			connection = "unstable"
		}
		tx, txErr := s.db.BeginTx()
		if txErr != nil {
			return txErr
		}
		_, txErr = tx.Exec(`UPDATE edge_nodes SET status=$2::varchar,
			health_status=CASE WHEN $2::varchar='offline' THEN 'unknown' ELSE health_status END,
			health_reason_code=CASE WHEN $2::varchar='offline' THEN NULL ELSE health_reason_code END,
			health_message=CASE WHEN $2::varchar='offline' THEN NULL ELSE health_message END WHERE id=$1`, item.id, connection)
		active := []string{}
		if txErr == nil && connection == "offline" && item.enabled {
			// Offline is an availability state, not an operational alert.
			txErr = s.coordinator.SuppressPrinterConnections(tx, item.id)
		} else if txErr == nil && connection == "online" && item.enabled && item.printerCount > 0 &&
			((item.printerSync == nil && now.Sub(item.created) > statusFreshness) || (item.printerSync != nil && now.Sub(*item.printerSync) > statusFreshness)) {
			policy, _ := alertPolicy("printer_status_sync_interrupted")
			txErr = s.alerts.OpenTx(tx, "node", item.id, item.id, "", "", "printer_status_sync_interrupted", policy.AlertSpec, nil)
			active = append(active, "printer_status_sync_interrupted")
			if txErr == nil {
				_, txErr = tx.Exec(`UPDATE edge_nodes SET health_status='critical',health_reason_code='printer_status_sync_interrupted',health_message='打印机状态超过90秒未成功同步' WHERE id=$1`, item.id)
			}
		}
		if txErr == nil && connection == "online" && len(active) == 0 {
			_, txErr = tx.Exec(`UPDATE edge_nodes SET health_status='healthy',health_reason_code=NULL,health_message=NULL WHERE id=$1 AND health_reason_code='printer_status_sync_interrupted'`, item.id)
		}
		if txErr == nil {
			txErr = s.alerts.ResolveReasonsTx(tx, "node", item.id, []string{"printer_status_sync_interrupted"}, active)
		}
		if txErr != nil {
			tx.Rollback()
			return txErr
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return s.sweepStaleJobs(now)
}

func (s *StatusService) sweepStaleJobs(now time.Time) error {
	rows, err := s.db.Query(`SELECT pj.id::text,p.edge_node_id,pj.printer_id::text FROM print_jobs pj
		JOIN printers p ON p.id=pj.printer_id WHERE pj.status='processing' AND pj.updated_at < $1`, now.Add(-15*time.Minute))
	if err != nil {
		return err
	}
	type staleJob struct{ id, nodeID, printerID string }
	jobs := []staleJob{}
	for rows.Next() {
		var job staleJob
		if err = rows.Scan(&job.id, &job.nodeID, &job.printerID); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, job := range jobs {
		if err = s.ApplyJobResult(job.id, job.nodeID, job.printerID, "unconfirmed", "print_timeout_unconfirmed", map[string]interface{}{"message": "打印任务超过15分钟且未取得明确终态"}); err != nil {
			return err
		}
	}
	return nil
}

func PrinterStatusStale(printer *models.Printer, now time.Time) bool {
	return printer == nil || printer.StatusReceivedAt == nil || now.Sub(*printer.StatusReceivedAt) > statusFreshness
}

// ValidatePrinterDispatch returns an empty string only when a new task may be sent.
// It deliberately performs ordered checks instead of manufacturing an availability state.
func ValidatePrinterDispatch(printer *models.Printer, node *models.EdgeNode, now time.Time) string {
	if printer == nil {
		return "printer_not_found"
	}
	if !printer.Enabled {
		return "printer_disabled"
	}
	if node == nil || !node.Enabled {
		return "node_disabled"
	}
	if node.ConnectionStatus != "online" {
		return "node_offline"
	}
	if PrinterStatusStale(printer, now) {
		return "printer_status_stale"
	}
	switch printer.PrinterStatus {
	case "idle":
		return ""
	case "printing":
		return "printer_busy"
	default:
		if printer.PrinterStatus == "" {
			return "printer_state_unknown"
		}
		return printer.PrinterStatus
	}
}
