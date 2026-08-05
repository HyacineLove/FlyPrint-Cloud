package websocket

import (
	"errors"
	"fmt"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/operations"

	"go.uber.org/zap"
)

// DispatchPrintJobAndRecord is the single Cloud-side transition from a newly
// created job to an acknowledged, failed, or unconfirmed dispatch result.
func DispatchPrintJobAndRecord(manager *ConnectionManager, printJobRepo *database.PrintJobRepository, statusService *operations.StatusService, job *models.PrintJob, nodeID string) {
	var fileAccessToken string
	var fileAccessTokenExpiresAt *time.Time
	if job.FileURL != "" && manager.TokenManager != nil {
		fileAccessToken, fileAccessTokenExpiresAt = manager.prepareFileAccessToken(nodeID, job)
	}
	if err := dispatchPrintJobAndRecord(manager, printJobRepo, statusService, job, nodeID,
		func() error { return manager.dispatchPrintJob(nodeID, job, fileAccessToken, fileAccessTokenExpiresAt) }); err != nil {
		logger.Error("Failed to finalize print job dispatch", zap.String("job_id", job.ID), zap.Error(err))
	}
}

func dispatchPrintJobAndRecord(manager *ConnectionManager, printJobRepo *database.PrintJobRepository, statusService *operations.StatusService, job *models.PrintJob, nodeID string, dispatch func() error) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		err = dispatch()
		if err == nil || !errors.Is(err, ErrAckTimeout) || attempt == 3 {
			break
		}
		logger.Warn("Print job delivery ACK timed out; retrying", zap.String("job_id", job.ID), zap.Int("attempt", attempt))
	}
	if err == nil {
		if printJobRepo == nil {
			return fmt.Errorf("print job repository unavailable after dispatch")
		}
		if updateErr := printJobRepo.MarkDispatched(job.ID); updateErr != nil {
			return fmt.Errorf("mark job dispatched: %w", updateErr)
		}
		return nil
	}
	if errors.Is(err, ErrAckTimeout) {
		if statusService == nil {
			return fmt.Errorf("status service unavailable after dispatch timeout")
		}
		if _, updateErr := statusService.ApplyDispatchUnconfirmed(job.ID, nodeID, job.PrinterID); updateErr != nil {
			return fmt.Errorf("mark dispatch unconfirmed: %w", updateErr)
		}
		return nil
	}

	errorCode := "dispatch_failed"
	errorMessage := "Print job was not delivered to the Edge node"
	if errors.Is(err, ErrAckRejected) {
		errorCode = "dispatch_rejected"
		errorMessage = "Edge node rejected the print job"
	}
	if statusService == nil {
		return fmt.Errorf("status service unavailable after dispatch failure")
	}
	if updateErr := statusService.ApplyJobResult(job.ID, nodeID, job.PrinterID, "failed", errorCode, map[string]interface{}{"message": errorMessage}); updateErr != nil {
		return fmt.Errorf("record dispatch failure: %w", updateErr)
	}
	return nil
}
