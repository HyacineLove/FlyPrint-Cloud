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

// DispatchHooks keeps integration state transitions adjacent to the physical
// delivery outcome. A failed dispatch must not leave its request in
// waiting_terminal forever.
type DispatchHooks struct {
	AfterDispatched func() error
	AfterFailure    func(errorCode, errorMessage string) error
}

// DispatchPrintJobAndRecord is the single Cloud-side transition from a newly
// created job to an acknowledged, failed, or unconfirmed dispatch result.
func DispatchPrintJobAndRecord(manager *ConnectionManager, printJobRepo *database.PrintJobRepository, statusService *operations.StatusService, job *models.PrintJob, nodeID string, afterDispatched ...func() error) {
	hooks := DispatchHooks{}
	if len(afterDispatched) > 0 {
		hooks.AfterDispatched = afterDispatched[0]
	}
	DispatchPrintJobAndRecordWithHooks(manager, printJobRepo, statusService, job, nodeID, hooks)
}

// DispatchPrintJobAndRecordWithHooks is the integration-aware dispatch entry
// point. The variadic API above is retained for standard print jobs.
func DispatchPrintJobAndRecordWithHooks(manager *ConnectionManager, printJobRepo *database.PrintJobRepository, statusService *operations.StatusService, job *models.PrintJob, nodeID string, hooks DispatchHooks) {
	// A delivery is accepted only after Edge has durably recorded it. The same
	// job ID is intentionally sent again with a new message ID when that ACK is
	// missing; Edge's inbox turns those deliveries into one physical print.
	var fileAccessToken string
	var fileAccessTokenExpiresAt *time.Time
	if job.FileURL != "" && manager.TokenManager != nil {
		fileAccessToken, fileAccessTokenExpiresAt = manager.prepareFileAccessToken(nodeID, job)
	}
	if err := dispatchPrintJobAndRecordWithDispatch(manager, printJobRepo, statusService, job, nodeID,
		func() error { return manager.dispatchPrintJob(nodeID, job, fileAccessToken, fileAccessTokenExpiresAt) }, hooks); err != nil {
		logger.Error("Failed to finalize print job dispatch", zap.String("job_id", job.ID), zap.Error(err))
	}
}

func dispatchPrintJobAndRecordWithDispatch(_ *ConnectionManager, printJobRepo *database.PrintJobRepository, statusService *operations.StatusService, job *models.PrintJob, nodeID string, dispatch func() error, hooks DispatchHooks) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		err = dispatch()
		if err == nil || !errors.Is(err, ErrAckTimeout) || attempt == 3 {
			break
		}
		logger.Warn("Print job delivery ACK timed out; retrying",
			zap.String("job_id", job.ID), zap.Int("attempt", attempt))
	}
	if err == nil {
		if printJobRepo == nil {
			return fmt.Errorf("print job repository unavailable after dispatch")
		}
		if updateErr := printJobRepo.MarkDispatched(job.ID); updateErr != nil {
			return fmt.Errorf("mark job dispatched: %w", updateErr)
		}
		if hooks.AfterDispatched != nil {
			if updateErr := hooks.AfterDispatched(); updateErr != nil {
				return fmt.Errorf("update integration request to dispatched: %w", updateErr)
			}
		}
		return nil
	}
	if errors.Is(err, ErrAckTimeout) {
		if statusService == nil {
			return fmt.Errorf("status service unavailable after dispatch timeout")
		}
		changed, updateErr := statusService.ApplyDispatchUnconfirmed(job.ID, nodeID, job.PrinterID)
		if updateErr != nil {
			return fmt.Errorf("mark dispatch unconfirmed: %w", updateErr)
		}
		// A timeout can race with Edge already accepting/processing the job.
		// Only transition the integration request when this call actually
		// changed pending -> unconfirmed; otherwise a later Edge terminal
		// report owns the integration transition.
		if changed && hooks.AfterFailure != nil {
			if callbackErr := hooks.AfterFailure("dispatch_ack_timeout", "Edge node did not acknowledge the print job"); callbackErr != nil {
				return fmt.Errorf("update integration request after dispatch timeout: %w", callbackErr)
			}
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
	if hooks.AfterFailure != nil {
		if callbackErr := hooks.AfterFailure(errorCode, errorMessage); callbackErr != nil {
			return fmt.Errorf("update integration request after dispatch failure: %w", callbackErr)
		}
	}
	return nil
}
