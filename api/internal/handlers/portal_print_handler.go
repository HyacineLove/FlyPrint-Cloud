package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"

	"github.com/gin-gonic/gin"
)

type PrintAuthorizer interface {
	Authorize(input models.PrintAuthorizationInput) (*models.PrintAuthorizationResult, error)
}

type PortalPrintHandler struct {
	authorizer PrintAuthorizer
}

func NewPortalPrintHandler(authorizer PrintAuthorizer) *PortalPrintHandler {
	return &PortalPrintHandler{authorizer: authorizer}
}

type portalPrintAuthorizationRequest struct {
	ConfirmationID    string `json:"confirmation_id" binding:"required,min=1,max=128"`
	TerminalSessionID string `json:"terminal_session_id" binding:"required,min=1,max=128"`
	SitePortalCode    string `json:"site_portal_code" binding:"required,min=1,max=64"`
	LocalFileID       string `json:"local_file_id" binding:"required,min=1,max=128"`
	FileDisplayName   string `json:"file_display_name" binding:"required,min=1,max=200"`
	PageCount         int    `json:"page_count" binding:"required,min=1,max=1000"`
	Copies            int    `json:"copies" binding:"required,min=1,max=99"`
	PaperSize         string `json:"paper_size" binding:"required,min=1,max=20"`
	ColorMode         string `json:"color_mode" binding:"required,oneof=mono color"`
	DuplexMode        string `json:"duplex_mode" binding:"required,oneof=simplex longedge shortedge"`
	PrinterID         string `json:"printer_id" binding:"required,uuid"`
}

func (h *PortalPrintHandler) Authorize(c *gin.Context) {
	var request portalPrintAuthorizationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"allowed": false, "error_code": "invalid_print_authorization",
			"message": "打印授权参数无效。",
		})
		return
	}

	result, err := h.authorizer.Authorize(models.PrintAuthorizationInput{
		NodeID:            strings.TrimSpace(c.Param("node_id")),
		ConfirmationID:    strings.TrimSpace(request.ConfirmationID),
		TerminalSessionID: strings.TrimSpace(request.TerminalSessionID),
		SitePortalCode:    strings.TrimSpace(request.SitePortalCode),
		LocalFileID:       strings.TrimSpace(request.LocalFileID),
		FileDisplayName:   strings.TrimSpace(request.FileDisplayName),
		PageCount:         request.PageCount,
		Copies:            request.Copies,
		PaperSize:         strings.TrimSpace(request.PaperSize),
		ColorMode:         request.ColorMode,
		DuplexMode:        request.DuplexMode,
		PrinterID:         request.PrinterID,
		Now:               time.Now().UTC(),
	})
	if err != nil {
		status, code, message := printAuthorizationError(err)
		c.JSON(status, gin.H{"allowed": false, "error_code": code, "message": message})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"allowed":        true,
		"job_id":         result.JobID,
		"reserved_quota": result.ReservedQuota,
		"quota_balance":  result.QuotaBalance,
	})
}

func printAuthorizationError(err error) (int, string, string) {
	switch {
	case errors.Is(err, database.ErrPrintAuthorizationInvalid):
		return http.StatusBadRequest, "invalid_print_authorization", "打印授权参数无效。"
	case errors.Is(err, database.ErrPrintAuthorizationSessionInvalid):
		return http.StatusConflict, "terminal_session_invalid", "当前终端会话已失效，请重新扫码。"
	case errors.Is(err, database.ErrPrintAuthorizationPortalMismatch):
		return http.StatusConflict, "site_portal_mismatch", "当前登录入口与终端会话不一致。"
	case errors.Is(err, database.ErrPrintAuthorizationUserDisabled):
		return http.StatusForbidden, "user_disabled", "当前用户已停用，不能打印。"
	case errors.Is(err, database.ErrPrintAuthorizationConflict):
		return http.StatusConflict, "print_confirmation_conflict", "本次打印确认内容已发生变化，请重新扫码。"
	case errors.Is(err, database.ErrPrintQuotaInsufficient):
		return http.StatusConflict, "print_quota_insufficient", "打印额度不足，请联系管理员增加额度。"
	case errors.Is(err, database.ErrPrintAuthorizationPrinterNotFound):
		return http.StatusNotFound, "printer_not_found", "打印机不存在。"
	case errors.Is(err, database.ErrPrintAuthorizationPrinterNotOwned):
		return http.StatusConflict, "printer_not_belong_to_node", "打印机不属于当前终端。"
	case errors.Is(err, database.ErrPrintAuthorizationPrinterUnavailable):
		return http.StatusConflict, "printer_unavailable", "打印机当前不可用。"
	case errors.Is(err, database.ErrPrintAuthorizationPrinterUnsupported):
		return http.StatusConflict, "printer_capability_unsupported", "打印机不支持当前打印参数。"
	default:
		return http.StatusInternalServerError, "print_authorization_failed", "打印授权失败，请联系工作人员。"
	}
}
