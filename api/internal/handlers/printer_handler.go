package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/operations"
	"fly-print-cloud/api/internal/websocket"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type PrinterHandler struct {
	printerRepo    *database.PrinterRepository
	edgeNodeRepo   *database.EdgeNodeRepository
	printJobRepo   *database.PrintJobRepository
	wsManager      *websocket.ConnectionManager
	tokenUsageRepo *database.TokenUsageRepository
	statusService  *operations.StatusService
	alertRepo      *database.OperationalAlertRepository
}

func NewPrinterHandler(printerRepo *database.PrinterRepository, edgeNodeRepo *database.EdgeNodeRepository, printJobRepo *database.PrintJobRepository, wsManager *websocket.ConnectionManager, tokenUsageRepo *database.TokenUsageRepository, statusService *operations.StatusService, alertRepo *database.OperationalAlertRepository) *PrinterHandler {
	return &PrinterHandler{printerRepo: printerRepo, edgeNodeRepo: edgeNodeRepo, printJobRepo: printJobRepo, wsManager: wsManager, tokenUsageRepo: tokenUsageRepo, statusService: statusService, alertRepo: alertRepo}
}

// NewPrinterHandlerWithCallbacks wires callback persistence for lifecycle
// settlement before a printer is disabled or deleted.
func (h *PrinterHandler) settleActiveJobs(printerID, reason string) error {
	if h.printJobRepo == nil || h.statusService == nil {
		return fmt.Errorf("active job settlement service unavailable")
	}
	refs, err := h.printJobRepo.ListActiveJobRefsByPrinterID(printerID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if err := h.statusService.ApplyJobResult(ref.ID, ref.EdgeNodeID, ref.PrinterID, "failed", reason, map[string]interface{}{"message": "Printer was disabled or deleted"}); err != nil {
			return err
		}
	}
	return nil
}

// UpdatePrinterRequest 更新打印机请求（Edge Node使用）
type UpdatePrinterRequest struct {
	Name            string                     `json:"name" binding:"required,min=1,max=100"`
	Model           string                     `json:"model"`
	SerialNumber    string                     `json:"serial_number"`
	FirmwareVersion string                     `json:"firmware_version"`
	PortInfo        string                     `json:"port_info"`
	IPAddress       *string                    `json:"ip_address"`
	MACAddress      string                     `json:"mac_address"`
	NetworkConfig   string                     `json:"network_config"`
	Capabilities    models.PrinterCapabilities `json:"capabilities"`
}

// AdminUpdatePrinterRequest 管理界面更新打印机请求
type AdminUpdatePrinterRequest struct {
	DisplayName *string `json:"display_name" binding:"omitempty,max=100"`
	Enabled     *bool   `json:"enabled"` // 使用指针类型以区分未设置和false
}

// PrinterWithStatus 包含实际状态的打印机信息
type PrinterWithStatus struct {
	*models.Printer
	StatusStale bool `json:"status_stale"`
	JobCount    int  `json:"job_count"`
}

// NewPrinterWithStatus 创建包含实际状态的打印机信息
func NewPrinterWithStatus(printer *models.Printer) *PrinterWithStatus {
	return &PrinterWithStatus{
		Printer:     printer,
		StatusStale: operations.PrinterStatusStale(printer, time.Now()),
	}
}

// Edge 注册打印机请求（简化版）
type EdgeRegisterPrinterRequest struct {
	Name            string                     `json:"name" binding:"required,min=1,max=100"`
	Model           string                     `json:"model"`
	SerialNumber    string                     `json:"serial_number"`
	FirmwareVersion string                     `json:"firmware_version"`
	PortInfo        string                     `json:"port_info"`
	IPAddress       *string                    `json:"ip_address"`
	MACAddress      string                     `json:"mac_address"`
	Capabilities    models.PrinterCapabilities `json:"capabilities"`
}

// validatePrinterCapabilities 验证打印机能力数据
func validatePrinterCapabilities(cap models.PrinterCapabilities) error {
	// 验证纸张尺寸不为空
	if len(cap.PaperSizes) == 0 {
		return fmt.Errorf("paper_sizes cannot be empty")
	}

	// 验证纸张尺寸数量不超过50个
	if len(cap.PaperSizes) > 50 {
		return fmt.Errorf("paper_sizes exceeds maximum of 50")
	}

	// 验证每个纸张尺寸不为空且长度合理
	for i, size := range cap.PaperSizes {
		if size == "" {
			return fmt.Errorf("paper_sizes[%d] cannot be empty", i)
		}
		if len(size) > 50 {
			return fmt.Errorf("paper_sizes[%d] exceeds maximum length of 50", i)
		}
	}

	// 验证介质类型数量不超过50个
	if len(cap.MediaTypes) > 50 {
		return fmt.Errorf("media_types exceeds maximum of 50")
	}

	// 验证每个介质类型长度合理
	for i, mediaType := range cap.MediaTypes {
		if len(mediaType) > 50 {
			return fmt.Errorf("media_types[%d] exceeds maximum length of 50", i)
		}
	}

	// 验证分辨率字段长度
	if len(cap.Resolution) > 50 {
		return fmt.Errorf("resolution exceeds maximum length of 50")
	}

	// 验证打印速度字段长度
	if len(cap.PrintSpeed) > 50 {
		return fmt.Errorf("print_speed exceeds maximum length of 50")
	}

	return nil
}

// 管理员 API

// ListPrinters 获取所有打印机列表（管理员）
// 支持分页、按Edge Node筛选、按状态筛选、按名称搜索
func (h *PrinterHandler) ListPrinters(c *gin.Context) {
	page, pageSize, _ := ParsePaginationParams(c)
	edgeNodeID := c.Query("edge_node_id") // 支持按Edge Node筛选
	printerID := c.Query("printer_id")
	status := c.Query("printer_status")
	search := c.Query("search") // 支持按名称/别名/ID搜索

	var printers []*models.Printer
	var total int
	var err error

	if printerID != "" {
		printer, getErr := h.printerRepo.GetPrinterByID(printerID)
		if getErr != nil {
			if strings.Contains(getErr.Error(), "not found") {
				SuccessResponse(c, gin.H{
					"items":       []*PrinterWithStatus{},
					"total":       0,
					"page":        page,
					"page_size":   pageSize,
					"total_pages": 0,
				})
				return
			}
			logger.Error("Failed to get printer by id", zap.Error(getErr))
			InternalErrorResponse(c, "获取打印机列表失败")
			return
		}
		printers = []*models.Printer{printer}
		total = 1
	} else if edgeNodeID != "" {
		// 按Edge Node筛选
		printers, err = h.printerRepo.ListPrintersByEdgeNode(edgeNodeID)
		if err != nil {
			logger.Error("Failed to list printers by edge node", zap.Error(err))
			InternalErrorResponse(c, "获取打印机列表失败")
			return
		}
		total = len(printers)
	} else {
		// 获取所有打印机
		printers, total, err = h.printerRepo.ListPrinters(page, pageSize)
		if err != nil {
			logger.Error("Failed to list printers", zap.Error(err))
			InternalErrorResponse(c, "获取打印机列表失败")
			return
		}
	}

	// 应用状态筛选（后端筛选）
	if status != "" {
		filtered := make([]*models.Printer, 0)
		for _, printer := range printers {
			if printer.PrinterStatus == status {
				filtered = append(filtered, printer)
			}
		}
		printers = filtered
		total = len(printers)
	}

	// 应用搜索筛选（按名称、别名、ID搜索，后端筛选）
	if search != "" {
		searchLower := strings.ToLower(search)
		filtered := make([]*models.Printer, 0)
		for _, printer := range printers {
			// 搜索名称
			if strings.Contains(strings.ToLower(printer.Name), searchLower) {
				filtered = append(filtered, printer)
				continue
			}
			// 搜索别名
			if printer.DisplayName != "" && strings.Contains(strings.ToLower(printer.DisplayName), searchLower) {
				filtered = append(filtered, printer)
				continue
			}
			// 搜索ID
			if strings.Contains(strings.ToLower(printer.ID), searchLower) {
				filtered = append(filtered, printer)
				continue
			}
		}
		printers = filtered
		total = len(printers)
	}

	// 添加状态新鲜度元数据，不派生新的打印机状态。
	printersWithStatus := make([]*PrinterWithStatus, len(printers))
	for i, printer := range printers {
		item := NewPrinterWithStatus(printer)
		if h.printJobRepo != nil {
			if count, countErr := h.printJobRepo.CountPrintJobs("", printer.ID, "", "", nil, nil); countErr == nil {
				item.JobCount = count
			}
		}
		printersWithStatus[i] = item
	}

	totalPages := (total + pageSize - 1) / pageSize
	response := gin.H{
		"items":       printersWithStatus,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	}

	SuccessResponse(c, response)
}

// GetPrinter 获取打印机详情
func (h *PrinterHandler) GetPrinter(c *gin.Context) {
	printerID := c.Param("id")
	if printerID == "" {
		BadRequestResponse(c, "打印机 ID 不能为空")
		return
	}

	printer, err := h.printerRepo.GetPrinterByID(printerID)
	if err != nil {
		NotFoundResponse(c, "打印机不存在")
		return
	}

	printerWithStatus := NewPrinterWithStatus(printer)
	SuccessResponse(c, printerWithStatus)
}

// UpdatePrinter 更新打印机（管理员）
func (h *PrinterHandler) UpdatePrinter(c *gin.Context) {
	printerID := c.Param("id")
	if printerID == "" {
		BadRequestResponse(c, "打印机 ID 不能为空")
		return
	}

	// 检查打印机是否存在
	printer, err := h.printerRepo.GetPrinterByID(printerID)
	if err != nil {
		NotFoundResponse(c, "打印机不存在")
		return
	}

	// 尝试解析为管理界面的简单更新请求
	disablingPrinter := false
	var adminReq AdminUpdatePrinterRequest
	if err := c.ShouldBindJSON(&adminReq); err == nil {
		// 管理界面更新（仅更新display_name和enabled）
		oldEnabled := printer.Enabled
		if adminReq.DisplayName != nil {
			// display_name is the Cloud-owned alias. A deliberate empty value
			// clears it and restores the Edge-reported name in the Admin UI.
			printer.DisplayName = strings.TrimSpace(*adminReq.DisplayName)
		}
		if adminReq.Enabled != nil {
			printer.Enabled = *adminReq.Enabled
		}
		disablingPrinter = oldEnabled && !printer.Enabled
	} else {
		// 尝试解析为Edge Node的完整更新请求
		var req UpdatePrinterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			BadRequestResponse(c, "请求参数无效")
			return
		}

		// 验证打印机能力数据
		if err := validatePrinterCapabilities(req.Capabilities); err != nil {
			BadRequestResponse(c, "打印机能力数据无效: "+err.Error())
			return
		}

		// 更新所有打印机信息
		printer.Name = req.Name
		printer.Model = req.Model
		printer.SerialNumber = req.SerialNumber
		printer.FirmwareVersion = req.FirmwareVersion
		printer.PortInfo = req.PortInfo
		printer.IPAddress = req.IPAddress
		printer.MACAddress = req.MACAddress
		printer.NetworkConfig = req.NetworkConfig
		printer.Capabilities = req.Capabilities
	}

	if err := h.printerRepo.UpdatePrinter(printer); err != nil {
		logger.Error("Failed to update printer", zap.String("printer_id", printerID), zap.Error(err))
		InternalErrorResponse(c, "更新打印机失败")
		return
	}
	// UpdatePrinter commits the printer row lock before this sweep. A racing
	// authorization therefore either observes disabled state or is included.
	if disablingPrinter && h.printJobRepo != nil {
		activeCount, err := h.printJobRepo.CountActiveJobsByPrinter(printerID)
		if err != nil {
			logger.Error("Failed to count active jobs after disabling printer", zap.String("printer_id", printerID), zap.Error(err))
			InternalErrorResponse(c, "检查活动任务失败")
			return
		}
		if activeCount > 0 {
			if err := h.settleActiveJobs(printerID, "printer_disabled"); err != nil {
				logger.Error("Failed to settle active jobs after disabling printer", zap.String("printer_id", printerID), zap.Error(err))
				InternalErrorResponse(c, "收敛活动任务失败")
				return
			}
		}
	}
	if disablingPrinter && h.tokenUsageRepo != nil {
		if revoked, err := h.tokenUsageRepo.RevokeTokensByNodeAndResource("upload", printer.EdgeNodeID, printer.ID); err != nil {
			logger.Warn("Failed to revoke upload tokens for disabled printer on node", zap.String("printer_id", printer.ID), zap.String("node_id", printer.EdgeNodeID), zap.Error(err))
		} else if revoked > 0 {
			logger.Info("Revoked upload tokens for disabled printer on node", zap.Int64("revoked", revoked), zap.String("printer_id", printer.ID), zap.String("node_id", printer.EdgeNodeID))
		}
	}

	// 功能 3.2.3: 移除 printer_state WebSocket 消息
	// 打印机启用/禁用状态变更不再通过 WebSocket 通知 Edge 端
	// Edge 端请求时会通过 API 错误码感知打印机状态

	logger.Info("Printer updated successfully", zap.String("printer_name", printer.Name))
	SuccessResponse(c, printer)
}

// DeletePrinter 删除打印机
func (h *PrinterHandler) DeletePrinter(c *gin.Context) {
	printerID := c.Param("id")
	if printerID == "" {
		BadRequestResponse(c, "打印机 ID 不能为空")
		return
	}

	// 检查打印机是否存在
	_, err := h.printerRepo.GetPrinterByID(printerID)
	if err != nil {
		NotFoundResponse(c, "打印机不存在")
		return
	}

	// 删除打印机
	if err := h.printerRepo.DeletePrinter(printerID); err != nil {
		logger.Error("Failed to delete printer", zap.String("printer_id", printerID), zap.Error(err))
		InternalErrorResponse(c, "删除打印机失败")
		return
	}
	if h.printJobRepo != nil {
		activeCount, err := h.printJobRepo.CountActiveJobsByPrinter(printerID)
		if err != nil {
			logger.Error("Failed to count active jobs after deleting printer", zap.String("printer_id", printerID), zap.Error(err))
			InternalErrorResponse(c, "检查活动任务失败")
			return
		}
		if activeCount > 0 {
			if err := h.settleActiveJobs(printerID, "printer_deleted"); err != nil {
				logger.Error("Failed to settle active jobs after deleting printer", zap.String("printer_id", printerID), zap.Error(err))
				InternalErrorResponse(c, "收敛活动任务失败")
				return
			}
		}
	}

	// 功能 3.2.4: 移除 printer_deleted WebSocket 消息
	// 打印机删除不再通过 WebSocket 通知 Edge 端
	// Edge 端请求时会收到 printer_not_found 错误，然后触发重新注册

	if h.alertRepo != nil {
		if err := h.alertRepo.DeleteForPrinter(printerID); err != nil {
			logger.Error("Failed to delete alerts for printer", zap.String("printer_id", printerID), zap.Error(err))
		}
	}
	logger.Info("Printer deleted successfully", zap.String("printer_id", printerID))
	SuccessResponse(c, gin.H{"message": "打印机删除成功"})
}

// Edge Node API

// EdgeRegisterPrinter Edge Node 注册打印机
// 优化：云端生成唯一ID，如果打印机已存在则返回原有ID
func (h *PrinterHandler) EdgeRegisterPrinter(c *gin.Context) {
	edgeNodeID := c.Param("node_id")
	if edgeNodeID == "" {
		BadRequestResponse(c, "Edge Node ID 不能为空")
		return
	}

	var req EdgeRegisterPrinterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequestResponse(c, "请求参数无效")
		return
	}

	// 验证打印机能力数据
	if err := validatePrinterCapabilities(req.Capabilities); err != nil {
		BadRequestResponse(c, "打印机能力数据无效: "+err.Error())
		return
	}

	// 验证 Edge Node 是否存在
	_, err := h.edgeNodeRepo.GetEdgeNodeByID(edgeNodeID)
	if err != nil {
		BadRequestResponse(c, "Edge Node 不存在")
		return
	}

	// 检查是否已存在相同名称的打印机
	existingPrinter, err := h.printerRepo.GetPrinterByNameAndEdgeNode(req.Name, edgeNodeID)
	isNew := false

	var printer *models.Printer
	if err == nil && existingPrinter != nil {
		// 打印机已存在，更新信息但保持原ID
		existingPrinter.Model = req.Model
		existingPrinter.SerialNumber = req.SerialNumber
		existingPrinter.FirmwareVersion = req.FirmwareVersion
		existingPrinter.PortInfo = req.PortInfo
		existingPrinter.IPAddress = req.IPAddress
		existingPrinter.MACAddress = req.MACAddress
		existingPrinter.Capabilities = req.Capabilities

		if err := h.printerRepo.UpdatePrinter(existingPrinter); err != nil {
			logger.Error("Failed to update existing printer", zap.String("printer_id", existingPrinter.ID), zap.Error(err))
			InternalErrorResponse(c, "更新打印机失败")
			return
		}
		printer = existingPrinter
		logger.Info("Printer updated by edge node", zap.String("printer_name", printer.Name), zap.String("edge_node_id", edgeNodeID), zap.String("printer_id", printer.ID))
	} else {
		if !errors.Is(err, sql.ErrNoRows) {
			logger.Error("Failed to look up existing printer", zap.String("printer_name", req.Name), zap.String("edge_node_id", edgeNodeID), zap.Error(err))
			InternalErrorResponse(c, "查询打印机失败")
			return
		}

		// 打印机不存在，创建新记录
		printer = &models.Printer{
			ID:              uuid.New().String(),
			Name:            req.Name,
			Model:           req.Model,
			SerialNumber:    req.SerialNumber,
			PrinterStatus:   "printer_state_unknown",
			Enabled:         true, // 默认启用
			FirmwareVersion: req.FirmwareVersion,
			PortInfo:        req.PortInfo,
			IPAddress:       req.IPAddress,
			MACAddress:      req.MACAddress,
			NetworkConfig:   "",
			Capabilities:    req.Capabilities,
			EdgeNodeID:      edgeNodeID,
		}

		isNew = true
		if err := h.printerRepo.CreatePrinter(printer); err != nil {
			logger.Error("Failed to create printer by edge node", zap.String("edge_node_id", edgeNodeID), zap.Error(err))
			InternalErrorResponse(c, "注册打印机失败")
			return
		}
		logger.Info("Printer registered by edge node", zap.String("printer_name", printer.Name), zap.String("edge_node_id", edgeNodeID), zap.String("printer_id", printer.ID))
	}

	// 返回响应，包含打印机ID和是否为新注册
	CreatedResponse(c, gin.H{
		"id":      printer.ID,
		"name":    printer.Name,
		"is_new":  isNew,
		"printer": printer,
	})
}

func (h *PrinterHandler) EdgeDeletePrinter(c *gin.Context) {
	edgeNodeID := c.Param("node_id")
	printerID := c.Param("printer_id")
	if edgeNodeID == "" || printerID == "" {
		BadRequestResponse(c, "参数不能为空")
		return
	}
	_, err := h.edgeNodeRepo.GetEdgeNodeByID(edgeNodeID)
	if err != nil {
		BadRequestResponse(c, "Edge Node 不存在")
		return
	}
	if err := h.printerRepo.DeletePrinterByEdgeNode(printerID, edgeNodeID); err != nil {
		NotFoundResponse(c, "打印机不存在")
		return
	}
	if h.printJobRepo != nil {
		activeCount, err := h.printJobRepo.CountActiveJobsByPrinter(printerID)
		if err != nil {
			logger.Error("Failed to count active jobs after edge printer deletion", zap.String("printer_id", printerID), zap.String("edge_node_id", edgeNodeID), zap.Error(err))
			InternalErrorResponse(c, "检查活动任务失败")
			return
		}
		if activeCount > 0 {
			if err := h.settleActiveJobs(printerID, "printer_deleted"); err != nil {
				logger.Error("Failed to settle active jobs after edge printer deletion", zap.String("printer_id", printerID), zap.String("edge_node_id", edgeNodeID), zap.Error(err))
				InternalErrorResponse(c, "收敛活动任务失败")
				return
			}
		}
	}
	SuccessResponse(c, gin.H{"message": "打印机删除成功"})
}

// ========== 功能 3.2.2: 批量状态上报 API ==========

// PrinterStatusItem 单个打印机状态项
type PrinterStatusItem struct {
	PrinterID        string     `json:"printer_id" binding:"required"`
	PrinterStatus    string     `json:"printer_status" binding:"required,max=64"`
	SourceObservedAt *time.Time `json:"source_observed_at"`
}

// EdgeBatchStatusRequest 批量状态上报请求
type EdgeBatchStatusRequest struct {
	Printers []PrinterStatusItem `json:"printers" binding:"required,min=1,max=100"`
}

// PrinterStatusError 单个打印机状态更新错误
type PrinterStatusError struct {
	PrinterID string `json:"printer_id"`
	Error     string `json:"error"`
}

// EdgeBatchUpdatePrinterStatus Edge Node 批量上报打印机状态
func (h *PrinterHandler) EdgeBatchUpdatePrinterStatus(c *gin.Context) {
	edgeNodeID := c.Param("node_id")
	if edgeNodeID == "" {
		BadRequestResponse(c, "Edge Node ID 不能为空")
		return
	}

	var req EdgeBatchStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequestResponse(c, "请求参数无效")
		return
	}

	// 验证 Edge Node 是否存在
	_, err := h.edgeNodeRepo.GetEdgeNodeByID(edgeNodeID)
	if err != nil {
		BadRequestResponse(c, "Edge Node 不存在")
		return
	}

	var updated int
	var failed int
	var errors []PrinterStatusError

	for _, item := range req.Printers {
		// 获取打印机信息
		printer, err := h.printerRepo.GetPrinterByID(item.PrinterID)
		if err != nil {
			failed++
			errors = append(errors, PrinterStatusError{
				PrinterID: item.PrinterID,
				Error:     "printer_not_found",
			})
			continue
		}

		// 验证打印机属于该 Edge Node
		if printer.EdgeNodeID != edgeNodeID {
			failed++
			errors = append(errors, PrinterStatusError{
				PrinterID: item.PrinterID,
				Error:     "printer_not_belong_to_node",
			})
			continue
		}

		// 功能 3.2.3: 放行禁用打印机的状态上报请求
		// 禁用的打印机仍然可以上报状态，以便监控其实际运行状态
		// 节点禁用检查由中间件 EdgeNodeEnabledCheck 处理

		// 更新打印机状态
		if h.statusService == nil {
			err = fmt.Errorf("status service unavailable")
		} else {
			err = h.statusService.ApplyPrinterSnapshot(edgeNodeID, operations.PrinterSnapshot{
				PrinterID: item.PrinterID, PrinterStatus: item.PrinterStatus,
				SourceObservedAt: item.SourceObservedAt,
			})
		}
		if err != nil {
			logger.Error("Failed to update printer status", zap.String("printer_id", item.PrinterID), zap.Error(err))
			failed++
			errors = append(errors, PrinterStatusError{
				PrinterID: item.PrinterID,
				Error:     "update_failed",
			})
			continue
		}

		updated++
	}

	logger.Info("Batch status update from edge node", zap.String("edge_node_id", edgeNodeID), zap.Int("updated", updated), zap.Int("failed", failed))
	SuccessResponse(c, gin.H{
		"updated": updated,
		"failed":  failed,
		"errors":  errors,
	})
}
