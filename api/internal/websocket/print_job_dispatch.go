package websocket

import (
	"errors"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/operations"

	"go.uber.org/zap"
)

// DispatchPrintJobAndRecord is the single Cloud-side transition from a newly
// created job to an acknowledged, failed, or unconfirmed dispatch result.
func DispatchPrintJobAndRecord(manager *ConnectionManager, printJobRepo *database.PrintJobRepository, statusService *operations.StatusService, job *models.PrintJob, nodeID string, afterDispatched ...func() error) {
	// A delivery is accepted only after Edge has durably recorded it. The same
	// job ID is intentionally sent again with a new message ID when that ACK is
	// missing; Edge's inbox turns those deliveries into one physical print.
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		err = manager.DispatchPrintJob(nodeID, job)
		if err == nil || !errors.Is(err, ErrAckTimeout) || attempt == 3 {
			break
		}
		logger.Warn("Print job delivery ACK timed out; retrying",
			zap.String("job_id", job.ID), zap.Int("attempt", attempt))
	}
	if err == nil {
		if updateErr := printJobRepo.MarkDispatched(job.ID); updateErr != nil {
			logger.Error("Failed to update job status to dispatched", zap.String("job_id", job.ID), zap.Error(updateErr))
		}
		if len(afterDispatched) > 0 && afterDispatched[0] != nil {
			if updateErr := afterDispatched[0](); updateErr != nil {
				logger.Error("Failed to update integration request to dispatched", zap.String("job_id", job.ID), zap.Error(updateErr))
			}
		}
		return
	}
	if errors.Is(err, ErrAckTimeout) {
		if updateErr := statusService.ApplyDispatchUnconfirmed(job.ID, nodeID, job.PrinterID); updateErr != nil {
			logger.Error("Failed to mark dispatch as unconfirmed", zap.String("job_id", job.ID), zap.Error(updateErr))
		}
		return
	}
	if errors.Is(err, ErrAckRejected) {
		if updateErr := statusService.ApplyJobResult(job.ID, nodeID, job.PrinterID, "failed", "dispatch_rejected", map[string]interface{}{"message": "边缘节点明确拒绝接收打印任务"}); updateErr != nil {
			logger.Error("Failed to record dispatch rejection", zap.String("job_id", job.ID), zap.Error(updateErr))
		}
		return
	}
	if updateErr := statusService.ApplyJobResult(job.ID, nodeID, job.PrinterID, "failed", "dispatch_failed", map[string]interface{}{"message": "打印任务未能发送到边缘节点"}); updateErr != nil {
		logger.Error("Failed to record dispatch failure", zap.String("job_id", job.ID), zap.Error(updateErr))
	}
}
