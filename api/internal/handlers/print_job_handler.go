package handlers

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/operations"
	"fly-print-cloud/api/internal/websocket"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type PrintJobHandler struct {
	printJobRepo  *database.PrintJobRepository
	printerRepo   *database.PrinterRepository
	edgeNodeRepo  *database.EdgeNodeRepository
	wsManager     *websocket.ConnectionManager
	statusService *operations.StatusService
	alertRepo     *database.OperationalAlertRepository
}

func NewPrintJobHandler(printJobRepo *database.PrintJobRepository, printerRepo *database.PrinterRepository, edgeNodeRepo *database.EdgeNodeRepository, wsManager *websocket.ConnectionManager, statusService *operations.StatusService, alertRepo *database.OperationalAlertRepository) *PrintJobHandler {
	return &PrintJobHandler{
		printJobRepo:  printJobRepo,
		printerRepo:   printerRepo,
		edgeNodeRepo:  edgeNodeRepo,
		wsManager:     wsManager,
		statusService: statusService,
		alertRepo:     alertRepo,
	}
}

// CreatePrintJobRequest 创建打印任务请求
type CreatePrintJobRequest struct {
	Name       string `json:"name"` // 可选，不提供时自动生成
	PrinterID  string `json:"printer_id" binding:"required"`
	FilePath   string `json:"file_path"`                        // 本地文件路径
	FileURL    string `json:"file_url"`                         // 文件URL
	FileSize   int64  `json:"file_size"`                        // 可选
	PageCount  int    `json:"page_count"`                       // 可选
	Copies     int    `json:"copies" binding:"omitempty,min=1"` // 可选，默认1
	PaperSize  string `json:"paper_size"`
	ColorMode  string `json:"color_mode"`
	DuplexMode string `json:"duplex_mode"`
	MaxRetries int    `json:"max_retries"` // 可选，默认3
}

// UpdatePrintJobRequest 更新打印任务请求
type UpdatePrintJobRequest struct {
	Name         *string `json:"name,omitempty"`
	Status       *string `json:"status,omitempty"`
	FilePath     *string `json:"file_path,omitempty"`
	FileSize     *int64  `json:"file_size,omitempty"`
	PageCount    *int    `json:"page_count,omitempty"`
	Copies       *int    `json:"copies,omitempty"`
	PaperSize    *string `json:"paper_size,omitempty"`
	ColorMode    *string `json:"color_mode,omitempty"`
	DuplexMode   *string `json:"duplex_mode,omitempty"`
	ErrorMessage *string `json:"error_message,omitempty"`
	RetryCount   *int    `json:"retry_count,omitempty"`
	MaxRetries   *int    `json:"max_retries,omitempty"`
}

// CreatePrintJob 创建打印任务
func (h *PrintJobHandler) CreatePrintJob(c *gin.Context) {
	var req CreatePrintJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequestResponse(c, "Invalid request parameters")
		return
	}

	// 验证文件路径或URL至少有一个
	if req.FilePath == "" && req.FileURL == "" {
		BadRequestResponse(c, "Either file_path or file_url must be provided")
		return
	}

	// 从 OAuth2 认证中获取用户信息
	userID, exists := c.Get("external_id")
	if !exists {
		UnauthorizedResponse(c, "Authentication required")
		return
	}

	userName, exists := c.Get("username")
	if !exists {
		UnauthorizedResponse(c, "Authentication required")
		return
	}

	// 自动生成任务名称
	jobName := req.Name
	if jobName == "" {
		if req.FileURL != "" {
			// 从URL提取文件名
			parts := strings.Split(req.FileURL, "/")
			filename := parts[len(parts)-1]
			// 去掉查询参数
			if idx := strings.Index(filename, "?"); idx != -1 {
				filename = filename[:idx]
			}
			// 限制长度，避免超过数据库字段限制
			if filename != "" && len(filename) <= 150 {
				jobName = filename
			} else if filename != "" {
				// 如果文件名太长，截取前150个字符
				jobName = filename[:150]
			} else {
				jobName = fmt.Sprintf("打印任务_%s", time.Now().Format("20060102_150405"))
			}
		} else if req.FilePath != "" {
			// 从文件路径提取文件名
			jobName = filepath.Base(req.FilePath)
		} else {
			jobName = fmt.Sprintf("打印任务_%s", time.Now().Format("20060102_150405"))
		}
	}

	job := &models.PrintJob{
		Name:       jobName,
		Status:     "pending",
		PrinterID:  req.PrinterID,
		UserID:     userID.(string),
		UserName:   userName.(string),
		FilePath:   req.FilePath,
		FileURL:    req.FileURL,
		FileSize:   req.FileSize,
		PageCount:  req.PageCount,
		Copies:     req.Copies,
		PaperSize:  req.PaperSize,
		ColorMode:  req.ColorMode,
		DuplexMode: req.DuplexMode,
		RetryCount: 0, // 保留字段但不使用
		MaxRetries: req.MaxRetries,
	}

	// 设置默认值
	if job.Copies == 0 {
		job.Copies = 1
	}
	if job.MaxRetries == 0 {
		job.MaxRetries = 3
	}

	// 获取打印机信息进行能力校验
	printer, err := h.printerRepo.GetPrinterByID(job.PrinterID)
	if err != nil {
		InternalErrorWithCode(c, ErrCodePrinterNotFound)
		return
	}

	if printer == nil {
		NotFoundWithCode(c, ErrCodePrinterNotFound)
		return
	}

	// 检查打印机启用状态
	if !printer.Enabled {
		ForbiddenWithCode(c, ErrCodePrinterDisabled)
		return
	}

	// 检查节点启用状态
	edgeNode, err := h.edgeNodeRepo.GetEdgeNodeByID(printer.EdgeNodeID)
	if err != nil {
		InternalErrorResponse(c, "Failed to get edge node information")
		return
	}
	if edgeNode == nil {
		NotFoundWithCode(c, ErrCodeEdgeNodeNotFound)
		return
	}
	if !edgeNode.Enabled {
		ForbiddenWithCode(c, ErrCodeEdgeNodeDisabled)
		return
	}
	if reason := operations.ValidatePrinterDispatch(printer, edgeNode, time.Now()); reason != "" {
		BadRequestResponse(c, reason)
		return
	}

	// 检查节点是否在线（通过WebSocket连接状态判断）
	if !h.wsManager.IsNodeConnected(printer.EdgeNodeID) {
		BadRequestResponse(c, "Edge node is offline, cannot create print job")
		return
	}

	// 校验打印机能力
	if err := h.validatePrintJobCapabilities(job, printer); err != nil {
		BadRequestResponse(c, err.Error())
		return
	}

	err = h.printJobRepo.CreatePrintJob(job)
	if err != nil {
		InternalErrorWithCode(c, ErrCodePrintJobCreateFailed)
		return
	}

	// 打印机信息已在上面获取并校验过

	go websocket.DispatchPrintJobAndRecord(h.wsManager, h.printJobRepo, h.statusService, job, printer.EdgeNodeID)

	c.JSON(http.StatusCreated, job)
}

// GetPrintJob 获取打印任务详情
func (h *PrintJobHandler) GetPrintJob(c *gin.Context) {
	id := c.Param("id")

	job, err := h.printJobRepo.GetPrintJobByID(id)
	if err != nil {
		InternalErrorResponse(c, "Failed to get print job")
		return
	}

	if job == nil {
		NotFoundWithCode(c, ErrCodePrintJobNotFound)
		return
	}

	SuccessResponse(c, job)
}

// ListPrintJobs 获取打印任务列表
func (h *PrintJobHandler) ListPrintJobs(c *gin.Context) {
	// 支持两种分页参数格式
	var limit, offset int

	// 优先使用 page/page_size 参数（前端使用）
	pageStr := c.Query("page")
	if pageStr != "" {
		// 使用标准分页参数解析
		page, pageSize, calculatedOffset := ParsePaginationParams(c)

		// 兼容pageSize参数（驼峰格式）
		if pageSizeStr := c.Query("pageSize"); pageSizeStr != "" {
			if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps >= 1 && ps <= 100 {
				pageSize = ps
				calculatedOffset = (page - 1) * pageSize
			}
		}

		limit = pageSize
		offset = calculatedOffset
	} else {
		// fallback 到 limit/offset 参数
		limitStr := c.DefaultQuery("limit", "20")
		offsetStr := c.DefaultQuery("offset", "0")

		limit, _ = strconv.Atoi(limitStr)
		offset, _ = strconv.Atoi(offsetStr)
	}

	// 过滤参数
	status := c.Query("status")
	printerID := c.Query("printer_id")
	userID := c.Query("user_id")
	userEmail := c.Query("user_email")
	edgeNodeID := c.Query("edge_node_id")
	initiatorCode := c.Query("initiator_code")

	// 时间筛选参数
	var startTime, endTime *time.Time
	if startTimeStr := c.Query("start_time"); startTimeStr != "" {
		// 支持 RFC3339 和 "YYYY-MM-DD HH:mm" 格式
		if t, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			startTime = &t
		} else if t, err := time.Parse("2006-01-02 15:04", startTimeStr); err == nil {
			startTime = &t
		} else if t, err := time.Parse("2006-01-02", startTimeStr); err == nil {
			startTime = &t
		}
	}
	if endTimeStr := c.Query("end_time"); endTimeStr != "" {
		if t, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			endTime = &t
		} else if t, err := time.Parse("2006-01-02 15:04", endTimeStr); err == nil {
			endTime = &t
		} else if t, err := time.Parse("2006-01-02", endTimeStr); err == nil {
			// 日期格式时，设置为该日结束时间
			t = t.Add(24 * time.Hour)
			endTime = &t
		}
	}

	jobs, total, err := h.printJobRepo.ListPrintJobsWithTotal(limit, offset, status, printerID, userID, userEmail, edgeNodeID, initiatorCode, startTime, endTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取打印任务列表失败"})
		return
	}

	// 计算当前页码
	currentPage := (offset / limit) + 1
	if limit == 0 {
		currentPage = 1
	}

	c.JSON(http.StatusOK, gin.H{
		"jobs": jobs,
		"pagination": gin.H{
			"page":     currentPage,
			"pageSize": limit,
			"total":    total,
			"limit":    limit,
			"offset":   offset,
		},
	})
}

// UpdatePrintJob 更新打印任务
func (h *PrintJobHandler) UpdatePrintJob(c *gin.Context) {
	id := c.Param("id")

	// 获取现有任务
	job, err := h.printJobRepo.GetPrintJobByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取打印任务失败"})
		return
	}

	if job == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "打印任务不存在"})
		return
	}

	var req UpdatePrintJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效"})
		return
	}

	// 更新字段
	if req.Name != nil {
		job.Name = *req.Name
	}
	if req.Status != nil {
		allowed := map[string]bool{"pending": true, "dispatched": true, "processing": true, "completed": true, "failed": true, "canceled": true, "unconfirmed": true}
		if !allowed[*req.Status] {
			BadRequestResponse(c, "无效的任务状态")
			return
		}
		job.Status = *req.Status
		// 状态变更时设置时间
		if *req.Status == "processing" && job.StartTime == nil {
			now := time.Now()
			job.StartTime = &now
		}
		// 注意：新任务流程不再使用 cancelled 状态，仅 completed 和 failed 为终态
		if (*req.Status == "completed" || *req.Status == "failed" || *req.Status == "canceled" || *req.Status == "unconfirmed") && job.EndTime == nil {
			now := time.Now()
			job.EndTime = &now
		}
	}
	if req.FilePath != nil {
		job.FilePath = *req.FilePath
	}
	if req.FileSize != nil {
		job.FileSize = *req.FileSize
	}
	if req.PageCount != nil {
		job.PageCount = *req.PageCount
	}
	if req.Copies != nil {
		job.Copies = *req.Copies
	}
	if req.PaperSize != nil {
		job.PaperSize = *req.PaperSize
	}
	if req.ColorMode != nil {
		job.ColorMode = *req.ColorMode
	}
	if req.DuplexMode != nil {
		job.DuplexMode = *req.DuplexMode
	}
	if req.ErrorMessage != nil {
		job.ErrorMessage = *req.ErrorMessage
	}
	if req.RetryCount != nil {
		job.RetryCount = *req.RetryCount
	}
	if req.MaxRetries != nil {
		job.MaxRetries = *req.MaxRetries
	}

	err = h.printJobRepo.UpdatePrintJob(job)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新打印任务失败"})
		return
	}

	c.JSON(http.StatusOK, job)
}

// validatePrintJobCapabilities 校验打印任务参数是否符合打印机能力
func (h *PrintJobHandler) validatePrintJobCapabilities(job *models.PrintJob, printer *models.Printer) error {
	// 校验颜色模式
	if job.ColorMode == "color" && !printer.Capabilities.ColorSupport {
		return fmt.Errorf("打印机 %s 不支持彩色打印", printer.Name)
	}

	// 校验双面模式
	if job.DuplexMode == "duplex" && !printer.Capabilities.DuplexSupport {
		return fmt.Errorf("打印机 %s 不支持双面打印", printer.Name)
	}

	// 校验纸张大小
	if job.PaperSize != "" && len(printer.Capabilities.PaperSizes) > 0 {
		supportedSize := false
		for _, size := range printer.Capabilities.PaperSizes {
			if size == job.PaperSize {
				supportedSize = true
				break
			}
		}
		if !supportedSize {
			return fmt.Errorf("打印机 %s 不支持纸张大小 %s，支持的大小：%v",
				printer.Name, job.PaperSize, printer.Capabilities.PaperSizes)
		}
	}

	// 校验份数（一般限制）
	if job.Copies <= 0 {
		return fmt.Errorf("打印份数必须大于0")
	}
	if job.Copies > 99 {
		return fmt.Errorf("打印份数不能超过99份")
	}

	return nil
}

// DeletePrintJob 删除打印任务（仅管理员）
func (h *PrintJobHandler) DeletePrintJob(c *gin.Context) {
	id := c.Param("id")

	// 检查任务是否存在
	job, err := h.printJobRepo.GetPrintJobByID(id)
	if err != nil {
		InternalErrorResponse(c, "Failed to get print job")
		return
	}

	if job == nil {
		NotFoundWithCode(c, ErrCodePrintJobNotFound)
		return
	}

	// 检查任务状态，仅允许删除已完成或失败的任务
	if job.Status != "completed" && job.Status != "failed" && job.Status != "canceled" && job.Status != "unconfirmed" {
		BadRequestResponse(c, "只能删除已完成或失败的打印任务")
		return
	}

	// 执行删除
	err = h.printJobRepo.DeletePrintJob(id)
	if err != nil {
		InternalErrorResponse(c, "Failed to delete print job")
		return
	}

	if h.alertRepo != nil {
		if err := h.alertRepo.DeleteForJob(id); err != nil {
			logger.Error("Failed to delete alerts for print job", zap.String("job_id", id), zap.Error(err))
			InternalErrorResponse(c, "failed to delete print job alerts")
			return
		}
	}
	logger.Info("Print job deleted by admin", zap.String("job_id", id))
	SuccessResponse(c, gin.H{"message": "打印任务已删除"})
}

// CancelPrintJob 取消打印任务
func (h *PrintJobHandler) CancelPrintJob(c *gin.Context) {
	id := c.Param("id")

	// 获取任务信息
	job, err := h.printJobRepo.GetPrintJobByID(id)
	if err != nil {
		InternalErrorResponse(c, "Failed to get print job")
		return
	}

	if job == nil {
		NotFoundWithCode(c, ErrCodePrintJobNotFound)
		return
	}

	// 检查任务状态，只能取消 pending、dispatched、processing 状态的任务
	if job.Status == "completed" || job.Status == "failed" || job.Status == "canceled" || job.Status == "unconfirmed" {
		BadRequestResponse(c, "无法取消已完成或已失败的任务")
		return
	}

	// 更新任务状态为failed，标记为用户取消。
	// 按产品策略，取消仅在云端生效，不再通知Edge执行取消。
	job.Status = "canceled"
	job.ErrorMessage = "Task cancelled by user"
	now := time.Now()
	if job.StartTime == nil {
		job.StartTime = &now
	}
	job.EndTime = &now

	err = h.printJobRepo.UpdatePrintJob(job)
	if err != nil {
		InternalErrorWithCode(c, ErrCodePrintJobCancelFailed)
		return
	}

	logger.Info("Print job cancelled by user", zap.String("job_id", id))
	SuccessResponse(c, gin.H{"message": "打印任务已取消", "job": job})
}
