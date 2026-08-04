package handlers

import (
	"fmt"
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

// EdgeNodeHandler Edge Node 管理处理器
type EdgeNodeHandler struct {
	db                  *database.DB
	edgeNodeRepo        *database.EdgeNodeRepository
	printerRepo         *database.PrinterRepository
	printJobRepo        *database.PrintJobRepository
	opsContactRepo      *database.OpsContactRepository
	wsManager           *websocket.ConnectionManager
	tokenUsageRepo      *database.TokenUsageRepository
	alertRepo           *database.OperationalAlertRepository
	providerRepo        *database.IntegrationProviderRepository
	tickets             *database.TerminalTicketRepository
	uploadSessions      *database.TerminalUploadSessionRepository
	integrationRequests *database.IntegrationPrintRequestRepository
	statusService       *operations.StatusService
	callbacks           *database.IntegrationCallbackRepository
}

// NewEdgeNodeHandler 创建 Edge Node 管理处理器
func NewEdgeNodeHandler(db *database.DB, edgeNodeRepo *database.EdgeNodeRepository, printerRepo *database.PrinterRepository, printJobRepo *database.PrintJobRepository, wsManager *websocket.ConnectionManager, tokenUsageRepo *database.TokenUsageRepository, alertRepo *database.OperationalAlertRepository, tickets *database.TerminalTicketRepository, uploadSessions *database.TerminalUploadSessionRepository, integrationRequests *database.IntegrationPrintRequestRepository, opsContactRepo *database.OpsContactRepository, providerRepo *database.IntegrationProviderRepository) *EdgeNodeHandler {
	return NewEdgeNodeHandlerWithServices(db, edgeNodeRepo, printerRepo, printJobRepo, wsManager, tokenUsageRepo, alertRepo, tickets, uploadSessions, integrationRequests, opsContactRepo, providerRepo, nil, nil)
}

// NewEdgeNodeHandlerWithServices wires the lifecycle settlement dependencies
// used when disabling or deleting an Edge node.
func NewEdgeNodeHandlerWithServices(db *database.DB, edgeNodeRepo *database.EdgeNodeRepository, printerRepo *database.PrinterRepository, printJobRepo *database.PrintJobRepository, wsManager *websocket.ConnectionManager, tokenUsageRepo *database.TokenUsageRepository, alertRepo *database.OperationalAlertRepository, tickets *database.TerminalTicketRepository, uploadSessions *database.TerminalUploadSessionRepository, integrationRequests *database.IntegrationPrintRequestRepository, opsContactRepo *database.OpsContactRepository, providerRepo *database.IntegrationProviderRepository, statusService *operations.StatusService, callbacks *database.IntegrationCallbackRepository) *EdgeNodeHandler {
	h := &EdgeNodeHandler{
		db:                  db,
		edgeNodeRepo:        edgeNodeRepo,
		printerRepo:         printerRepo,
		printJobRepo:        printJobRepo,
		opsContactRepo:      opsContactRepo,
		wsManager:           wsManager,
		tokenUsageRepo:      tokenUsageRepo,
		alertRepo:           alertRepo,
		tickets:             tickets,
		uploadSessions:      uploadSessions,
		integrationRequests: integrationRequests,
		providerRepo:        providerRepo,
		statusService:       statusService,
		callbacks:           callbacks,
	}
	return h
}

func (h *EdgeNodeHandler) settleActiveJobs(nodeID, reason string) error {
	if h.printJobRepo == nil || h.statusService == nil {
		return fmt.Errorf("active job settlement service unavailable")
	}
	refs, err := h.printJobRepo.ListActiveJobRefsByEdgeNodeID(nodeID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if err := h.statusService.ApplyJobResult(ref.ID, ref.EdgeNodeID, ref.PrinterID, "failed", reason, map[string]interface{}{"message": "Edge infrastructure was disabled or deleted"}); err != nil {
			return err
		}
		if h.callbacks != nil {
			if err := h.callbacks.TransitionForJob(ref.ID, "failed", reason, "Edge infrastructure was disabled or deleted"); err != nil {
				return err
			}
		}
	}
	return nil
}

// RegisterEdgeNodeRequest Edge Node 注册请求
// node_id 由服务端生成，客户端只需提供节点名称和硬件信息
type RegisterEdgeNodeRequest struct {
	Name             string  `json:"name" binding:"required,min=1,max=100"`
	MACAddress       string  `json:"mac_address"`
	OSVersion        string  `json:"os_version"`
	CPUInfo          string  `json:"cpu_info"`
	MemoryInfo       string  `json:"memory_info"`
	DiskInfo         string  `json:"disk_info"`
	NetworkInterface string  `json:"network_interface"`
	Location         string  `json:"location"`
	IPAddress        *string `json:"ip_address"`
	Version          string  `json:"version"`
}

// UpdateEdgeNodeRequest Edge Node 更新请求
type UpdateEdgeNodeRequest struct {
	Name              string   `json:"name" binding:"required,min=1,max=100"`
	Enabled           *bool    `json:"enabled"` // 使用指针类型以区分未设置和false
	Version           string   `json:"version"`
	Location          string   `json:"location"`
	Latitude          *float64 `json:"latitude"`
	Longitude         *float64 `json:"longitude"`
	IPAddress         *string  `json:"ip_address"`
	MACAddress        string   `json:"mac_address"`
	NetworkInterface  string   `json:"network_interface"`
	OSVersion         string   `json:"os_version"`
	CPUInfo           string   `json:"cpu_info"`
	MemoryInfo        string   `json:"memory_info"`
	DiskInfo          string   `json:"disk_info"`
	ConnectionQuality string   `json:"connection_quality"`
	Latency           int      `json:"latency"`
}

type UpdateEdgeNodeAliasRequest struct {
	Alias string `json:"alias" binding:"max=100"`
}
type UpdateEdgeNodeEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

type UpdateEdgeNodeLoginSourceRequest struct {
	LoginSource string `json:"login_source" binding:"required"`
}

// EdgeNodeInfo Edge Node 信息响应
// ProvisionEdgeNodeRequest creates the server-side identity before an Edge is
// installed. The administrator then creates one OAuth client bound to the
// returned ID; this avoids any shared-credential registration bootstrap.
type ProvisionEdgeNodeRequest struct {
	Name     string `json:"name" binding:"required,min=1,max=100"`
	Location string `json:"location"`
}

type EdgeNodeInfo struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Alias             string    `json:"alias,omitempty"`
	LoginSource       string    `json:"login_source"`
	RegistrationState string    `json:"registration_state"`
	ConnectionStatus  string    `json:"connection_status"`
	HealthStatus      string    `json:"health_status"`
	HealthReasonCode  string    `json:"health_reason_code,omitempty"`
	HealthMessage     string    `json:"health_message,omitempty"`
	Enabled           bool      `json:"enabled"`
	Version           string    `json:"version"`
	LastHeartbeat     time.Time `json:"last_heartbeat"`
	Location          string    `json:"location"`
	Latitude          *float64  `json:"latitude"`
	Longitude         *float64  `json:"longitude"`
	IPAddress         *string   `json:"ip_address,omitempty"`
	MACAddress        string    `json:"mac_address"`
	NetworkInterface  string    `json:"network_interface"`
	OSVersion         string    `json:"os_version"`
	CPUInfo           string    `json:"cpu_info"`
	MemoryInfo        string    `json:"memory_info"`
	DiskInfo          string    `json:"disk_info"`
	ConnectionQuality string    `json:"connection_quality"`
	Latency           int       `json:"latency"`
	PrinterCount      int       `json:"printer_count"` // 管理的打印机数量
	OpsContactCount   int       `json:"ops_contact_count"`
	JobCount          int       `json:"job_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// RegisterEdgeNode 注册 Edge Node
// 服务端生成 node_id (UUID)，状态默认为 offline
// Edge 端需保存返回的 node_id，避免重复注册
// ProvisionEdgeNode is an administrator-only bootstrap path. It deliberately
// does not accept hardware facts from a browser; Edge supplies those only
// after it has authenticated with its node-bound credential.
func (h *EdgeNodeHandler) ProvisionEdgeNode(c *gin.Context) {
	var req ProvisionEdgeNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}

	node := &models.EdgeNode{
		ID:               uuid.NewString(),
		Name:             req.Name,
		Location:         req.Location,
		ConnectionStatus: "offline",
		HealthStatus:     "unknown",
		Enabled:          true,
		LastHeartbeat:    time.Now(),
		Version:          "provisioned",
	}
	if err := h.edgeNodeRepo.CreateEdgeNode(node); err != nil {
		logger.Error("Failed to provision Edge node", zap.Error(err))
		InternalErrorResponse(c, "failed to provision Edge node")
		return
	}

	logger.Info("Edge Node provisioned", zap.String("node_id", node.ID), zap.String("node_name", node.Name))
	CreatedResponse(c, gin.H{
		"id":       node.ID,
		"name":     node.Name,
		"location": node.Location,
		"enabled":  node.Enabled,
	})
}

func (h *EdgeNodeHandler) RegisterEdgeNode(c *gin.Context) {
	var req RegisterEdgeNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}

	// 创建 Edge Node，服务端生成 UUID 作为 node_id
	node := &models.EdgeNode{
		ID:               uuid.New().String(), // 服务端生成 UUID
		Name:             req.Name,
		ConnectionStatus: "offline",
		HealthStatus:     "unknown",
		Enabled:          true, // 注册完成后默认启用节点
		LastHeartbeat:    time.Now(),
		MACAddress:       req.MACAddress,
		OSVersion:        req.OSVersion,
		CPUInfo:          req.CPUInfo,
		MemoryInfo:       req.MemoryInfo,
		DiskInfo:         req.DiskInfo,
		NetworkInterface: req.NetworkInterface,
		Location:         req.Location,
		IPAddress:        req.IPAddress,
		Version:          req.Version,
	}

	if err := h.edgeNodeRepo.CreateEdgeNode(node); err != nil {
		logger.Error("Failed to register edge node", zap.Error(err))
		InternalErrorResponse(c, "注册 Edge Node 失败")
		return
	}

	// 返回节点信息
	nodeInfo := EdgeNodeInfo{
		ID:                node.ID,
		Name:              node.Name,
		Alias:             node.Alias,
		LoginSource:       node.LoginSource,
		RegistrationState: node.RegistrationState,
		ConnectionStatus:  node.ConnectionStatus,
		HealthStatus:      node.HealthStatus,
		HealthReasonCode:  node.HealthReasonCode,
		HealthMessage:     node.HealthMessage,
		Enabled:           node.Enabled,
		Version:           node.Version,
		LastHeartbeat:     node.LastHeartbeat,
		Location:          node.Location,
		Latitude:          node.Latitude,
		Longitude:         node.Longitude,
		IPAddress:         node.IPAddress,
		MACAddress:        node.MACAddress,
		NetworkInterface:  node.NetworkInterface,
		OSVersion:         node.OSVersion,
		CPUInfo:           node.CPUInfo,
		MemoryInfo:        node.MemoryInfo,
		DiskInfo:          node.DiskInfo,
		ConnectionQuality: node.ConnectionQuality,
		Latency:           node.Latency,
		CreatedAt:         node.CreatedAt,
		UpdatedAt:         node.UpdatedAt,
	}

	logger.Info("Edge Node registered successfully", zap.String("node_name", node.Name))
	CreatedResponse(c, nodeInfo)
}

// ListEdgeNodes 获取 Edge Node 列表
// 支持分页、状态过滤、排序和搜索
func (h *EdgeNodeHandler) ListEdgeNodes(c *gin.Context) {
	// 获取分页参数
	page, pageSize, _ := ParsePaginationParams(c)
	status := c.Query("connection_status")

	// 获取排序参数
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")

	// 获取搜索参数
	search := c.Query("search")

	// 验证排序字段
	validSortFields := map[string]bool{
		"last_heartbeat": true,
		"printer_count":  true,
		"created_at":     true,
		"name":           true,
	}
	if !validSortFields[sortBy] {
		sortBy = "created_at"
	}

	// 验证排序方向
	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "desc"
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	// 可选：检查并更新超时的节点状态（3分钟超时）
	_, _ = h.edgeNodeRepo.CheckAndUpdateOfflineNodes(3)

	// 查询 Edge Node 列表
	nodes, total, err := h.edgeNodeRepo.ListEdgeNodes(offset, pageSize, status, sortBy, sortOrder, search)
	if err != nil {
		InternalErrorResponse(c, "Failed to get edge node list")
		return
	}

	// 转换为响应格式
	nodeInfos := make([]EdgeNodeInfo, len(nodes))
	for i, node := range nodes {
		// 获取该边缘节点管理的打印机数量
		printerCount, err := h.printerRepo.CountPrintersByEdgeNode(node.ID)
		if err != nil {
			printerCount = 0 // 如果查询失败，设置为0
		}
		opsContactCount := 0
		if h.opsContactRepo != nil {
			if count, err := h.opsContactRepo.CountActiveByNode(node.ID); err == nil {
				opsContactCount = count
			}
		}
		jobCount := 0
		if h.printJobRepo != nil {
			if count, err := h.printJobRepo.CountPrintJobs("", "", "", node.ID, nil, nil); err == nil {
				jobCount = count
			}
		}

		nodeInfos[i] = EdgeNodeInfo{
			ID:                node.ID,
			Name:              node.Name,
			Alias:             node.Alias,
			LoginSource:       node.LoginSource,
			RegistrationState: node.RegistrationState,
			ConnectionStatus:  node.ConnectionStatus,
			HealthStatus:      node.HealthStatus,
			HealthReasonCode:  node.HealthReasonCode,
			HealthMessage:     node.HealthMessage,
			Enabled:           node.Enabled,
			Version:           node.Version,
			LastHeartbeat:     node.LastHeartbeat,
			Location:          node.Location,
			Latitude:          node.Latitude,
			Longitude:         node.Longitude,
			IPAddress:         node.IPAddress,
			MACAddress:        node.MACAddress,
			NetworkInterface:  node.NetworkInterface,
			OSVersion:         node.OSVersion,
			CPUInfo:           node.CPUInfo,
			MemoryInfo:        node.MemoryInfo,
			DiskInfo:          node.DiskInfo,
			ConnectionQuality: node.ConnectionQuality,
			Latency:           node.Latency,
			PrinterCount:      printerCount,
			OpsContactCount:   opsContactCount,
			JobCount:          jobCount,
			CreatedAt:         node.CreatedAt,
			UpdatedAt:         node.UpdatedAt,
		}
	}

	PaginatedSuccessResponse(c, nodeInfos, total, page, pageSize)
}

// GetEdgeNode 获取 Edge Node 详情
func (h *EdgeNodeHandler) GetEdgeNode(c *gin.Context) {
	nodeID := c.Param("id")
	if nodeID == "" {
		BadRequestResponse(c, "Edge Node ID 不能为空")
		return
	}

	node, err := h.edgeNodeRepo.GetEdgeNodeByID(nodeID)
	if err != nil {
		NotFoundResponse(c, "Edge Node 不存在")
		return
	}

	// 获取该边缘节点管理的打印机数量
	printerCount, err := h.printerRepo.CountPrintersByEdgeNode(node.ID)
	if err != nil {
		printerCount = 0 // 如果查询失败，设置为0
	}
	opsContactCount := 0
	if h.opsContactRepo != nil {
		if count, err := h.opsContactRepo.CountActiveByNode(node.ID); err == nil {
			opsContactCount = count
		}
	}
	jobCount := 0
	if h.printJobRepo != nil {
		if count, err := h.printJobRepo.CountPrintJobs("", "", "", node.ID, nil, nil); err == nil {
			jobCount = count
		}
	}

	nodeInfo := EdgeNodeInfo{
		ID:                node.ID,
		Name:              node.Name,
		Alias:             node.Alias,
		LoginSource:       node.LoginSource,
		RegistrationState: node.RegistrationState,
		ConnectionStatus:  node.ConnectionStatus,
		HealthStatus:      node.HealthStatus,
		HealthReasonCode:  node.HealthReasonCode,
		HealthMessage:     node.HealthMessage,
		Enabled:           node.Enabled,
		Version:           node.Version,
		LastHeartbeat:     node.LastHeartbeat,
		Location:          node.Location,
		Latitude:          node.Latitude,
		Longitude:         node.Longitude,
		IPAddress:         node.IPAddress,
		MACAddress:        node.MACAddress,
		NetworkInterface:  node.NetworkInterface,
		OSVersion:         node.OSVersion,
		CPUInfo:           node.CPUInfo,
		MemoryInfo:        node.MemoryInfo,
		DiskInfo:          node.DiskInfo,
		ConnectionQuality: node.ConnectionQuality,
		Latency:           node.Latency,
		PrinterCount:      printerCount,
		OpsContactCount:   opsContactCount,
		JobCount:          jobCount,
		CreatedAt:         node.CreatedAt,
		UpdatedAt:         node.UpdatedAt,
	}

	SuccessResponse(c, nodeInfo)
}

// UpdateEdgeNode 更新 Edge Node
func (h *EdgeNodeHandler) UpdateEdgeNode(c *gin.Context) {
	nodeID := c.Param("id")
	if nodeID == "" {
		BadRequestResponse(c, "Edge Node ID 不能为空")
		return
	}

	var req UpdateEdgeNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}

	// 检查节点是否存在
	node, err := h.edgeNodeRepo.GetEdgeNodeByID(nodeID)
	if err != nil {
		NotFoundResponse(c, "Edge Node 不存在")
		return
	}

	// 更新节点信息
	node.Name = req.Name

	// 只有当Status字段不为空时才更新

	// 处理Enabled字段更新（逻辑级联，不修改printer的enable状态）
	oldEnabled := node.Enabled
	if req.Enabled != nil {
		node.Enabled = *req.Enabled
	}

	node.Version = req.Version
	node.Location = req.Location
	node.Latitude = req.Latitude
	node.Longitude = req.Longitude
	node.IPAddress = req.IPAddress
	node.MACAddress = req.MACAddress
	node.NetworkInterface = req.NetworkInterface
	node.OSVersion = req.OSVersion
	node.CPUInfo = req.CPUInfo
	node.MemoryInfo = req.MemoryInfo
	node.DiskInfo = req.DiskInfo
	node.ConnectionQuality = req.ConnectionQuality
	node.Latency = req.Latency

	if err := h.edgeNodeRepo.UpdateEdgeNode(node); err != nil {
		logger.Error("Failed to update edge node", zap.String("node_id", nodeID), zap.Error(err))
		InternalErrorResponse(c, "更新 Edge Node 失败")
		return
	}

	// 当节点从启用变为禁用时，撤销该节点的所有上传Token
	if oldEnabled && !node.Enabled && h.tokenUsageRepo != nil {
		if revoked, err := h.tokenUsageRepo.RevokeTokensByNodeAndType("upload", node.ID); err != nil {
			logger.Warn("Failed to revoke upload tokens for disabled node", zap.String("node_id", node.ID), zap.Error(err))
		} else if revoked > 0 {
			logger.Info("Revoked upload tokens for disabled node", zap.Int64("revoked", revoked), zap.String("node_id", node.ID))
		}
	}

	// 不再向 Edge 端发送 node_state 消息
	// 禁用节点的请求将在云端通过中间件拦截

	nodeInfo := EdgeNodeInfo{
		ID:                node.ID,
		Name:              node.Name,
		Alias:             node.Alias,
		LoginSource:       node.LoginSource,
		RegistrationState: node.RegistrationState,
		ConnectionStatus:  node.ConnectionStatus,
		HealthStatus:      node.HealthStatus,
		HealthReasonCode:  node.HealthReasonCode,
		HealthMessage:     node.HealthMessage,
		Enabled:           node.Enabled,
		Version:           node.Version,
		LastHeartbeat:     node.LastHeartbeat,
		Location:          node.Location,
		Latitude:          node.Latitude,
		Longitude:         node.Longitude,
		IPAddress:         node.IPAddress,
		MACAddress:        node.MACAddress,
		NetworkInterface:  node.NetworkInterface,
		OSVersion:         node.OSVersion,
		CPUInfo:           node.CPUInfo,
		MemoryInfo:        node.MemoryInfo,
		DiskInfo:          node.DiskInfo,
		ConnectionQuality: node.ConnectionQuality,
		Latency:           node.Latency,
		CreatedAt:         node.CreatedAt,
		UpdatedAt:         node.UpdatedAt,
	}

	logger.Info("Edge Node updated successfully", zap.String("node_name", node.Name))
	SuccessResponse(c, nodeInfo)
}

// UpdateAlias keeps the operator-owned alias separate from the name reported
// by Edge. An empty value intentionally clears the alias.
func (h *EdgeNodeHandler) UpdateAlias(c *gin.Context) {
	var req UpdateEdgeNodeAliasRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}
	if err := h.edgeNodeRepo.UpdateAlias(c.Param("id"), req.Alias); err != nil {
		NotFoundResponse(c, "Edge Node 不存在")
		return
	}
	node, err := h.edgeNodeRepo.GetEdgeNodeByID(c.Param("id"))
	if err != nil {
		NotFoundResponse(c, "Edge Node 不存在")
		return
	}
	SuccessResponse(c, gin.H{"id": node.ID, "name": node.Name, "alias": node.Alias})
}

func (h *EdgeNodeHandler) UpdateEnabled(c *gin.Context) {
	var req UpdateEdgeNodeEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}
	if err := h.edgeNodeRepo.UpdateEnabled(c.Param("id"), req.Enabled); err != nil {
		NotFoundResponse(c, "Edge Node 不存在")
		return
	}
	// UpdateEnabled acquires the node row lock before committing. Settling
	// after that commit prevents an authorization transaction that raced with
	// the update from creating a job which the pre-check could not see.
	if !req.Enabled && h.printJobRepo != nil {
		activeCount, err := h.printJobRepo.CountActiveJobsByEdgeNodeID(c.Param("id"))
		if err != nil {
			logger.Error("Failed to count active jobs after disabling edge node", zap.String("node_id", c.Param("id")), zap.Error(err))
			InternalErrorResponse(c, "检查活动任务失败")
			return
		}
		if activeCount > 0 {
			if err := h.settleActiveJobs(c.Param("id"), "edge_node_disabled"); err != nil {
				logger.Error("Failed to settle active jobs after disabling edge node", zap.String("node_id", c.Param("id")), zap.Error(err))
				InternalErrorResponse(c, "收敛活动任务失败")
				return
			}
		}
	}
	if !req.Enabled && h.tokenUsageRepo != nil {
		_, _ = h.tokenUsageRepo.RevokeTokensByNodeAndType("upload", c.Param("id"))
	}
	if !req.Enabled && h.uploadSessions != nil {
		_ = h.uploadSessions.DeleteForNode(c.Param("id"))
	}
	SuccessResponse(c, gin.H{"id": c.Param("id"), "enabled": req.Enabled})
}

func (h *EdgeNodeHandler) UpdateLoginSource(c *gin.Context) {
	var req UpdateEdgeNodeLoginSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil || !isValidTerminalLoginSource(req.LoginSource) {
		BadRequestResponse(c, "invalid terminal login source")
		return
	}
	if req.LoginSource != "official" {
		if h.providerRepo == nil {
			InternalErrorResponse(c, "provider repository unavailable")
			return
		}
		provider, err := h.providerRepo.Get(req.LoginSource, false)
		if err != nil || provider == nil || !provider.Enabled {
			BadRequestResponse(c, "terminal login provider is not enabled")
			return
		}
	}
	if err := h.edgeNodeRepo.UpdateLoginSource(c.Param("id"), req.LoginSource); err != nil {
		NotFoundResponse(c, "Edge Node not found")
		return
	}
	SuccessResponse(c, gin.H{"id": c.Param("id"), "login_source": req.LoginSource})
}

// DeleteEdgeNode 删除 Edge Node
func (h *EdgeNodeHandler) DeleteEdgeNode(c *gin.Context) {
	nodeID := c.Param("id")
	if nodeID == "" {
		BadRequestResponse(c, "Edge Node ID 不能为空")
		return
	}

	// 检查节点是否存在
	_, err := h.edgeNodeRepo.GetEdgeNodeByID(nodeID)
	if err != nil {
		NotFoundResponse(c, "Edge Node 不存在")
		return
	}

	// 开始事务。软删除提交后再收敛任务，避免并发授权在删除前提交而漏过预检查。
	tx, err := h.db.BeginTx()
	if err != nil {
		logger.Error("Failed to begin transaction for deleting node", zap.String("node_id", nodeID), zap.Error(err))
		InternalErrorResponse(c, "开始事务失败")
		return
	}

	// 确保事务最终被回滚或提交
	committed := false
	defer func() {
		if !committed {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				logger.Error("Failed to rollback transaction", zap.Error(rollbackErr))
			}
		}
	}()

	// 1. 先取消仍可被打开或继续处理的临时业务数据。
	// 历史票据、订单和打印任务保留，避免破坏审计和外部状态查询。
	if h.integrationRequests != nil {
		if err := h.integrationRequests.CancelForNodeTx(tx, nodeID); err != nil {
			logger.Error("Failed to cancel integration requests for node", zap.String("node_id", nodeID), zap.Error(err))
			InternalErrorResponse(c, "取消节点第三方任务失败")
			return
		}
	}
	if h.tickets != nil {
		if err := h.tickets.CancelActiveForNodeTx(tx, nodeID); err != nil {
			logger.Error("Failed to cancel terminal tickets for node", zap.String("node_id", nodeID), zap.Error(err))
			InternalErrorResponse(c, "取消终端票据失败")
			return
		}
	}
	if h.uploadSessions != nil {
		if err := h.uploadSessions.DeleteForNodeTx(tx, nodeID); err != nil {
			logger.Error("Failed to delete upload sessions for node", zap.String("node_id", nodeID), zap.Error(err))
			InternalErrorResponse(c, "清理上传会话失败")
			return
		}
	}
	// Soft-delete does not fire FK ON DELETE CASCADE; clear ops bindings now.
	if h.opsContactRepo != nil {
		if err := h.opsContactRepo.ClearBindingsForNodeTx(tx, nodeID); err != nil {
			logger.Error("Failed to clear ops contact bindings for node", zap.String("node_id", nodeID), zap.Error(err))
			InternalErrorResponse(c, "取消运维人员节点绑定失败")
			return
		}
	}

	// 2. 清理告警并软删除该节点下打印机。打印机不能硬删除，因为历史
	// terminal_tickets 仍通过外键引用它们。
	if h.alertRepo != nil {
		if err := h.alertRepo.DeleteForNodeTx(tx, nodeID); err != nil {
			logger.Error("Failed to delete alerts for node", zap.String("node_id", nodeID), zap.Error(err))
			InternalErrorResponse(c, "删除节点告警失败")
			return
		}
	}
	if h.printerRepo != nil {
		if err := h.printerRepo.DeletePrintersByEdgeNodeTx(tx, nodeID); err != nil {
			logger.Error("Failed to delete printers for node", zap.String("node_id", nodeID), zap.Error(err))
			InternalErrorResponse(c, "删除打印机失败")
			return
		}
		logger.Info("Successfully deleted all printers for node", zap.String("node_id", nodeID))
	}

	// 3. 删除节点（软删除）
	if err := h.edgeNodeRepo.DeleteEdgeNodeTx(tx, nodeID); err != nil {
		logger.Error("Failed to delete edge node", zap.String("node_id", nodeID), zap.Error(err))
		InternalErrorResponse(c, "删除 Edge Node 失败")
		return
	}

	// 提交事务
	if err := tx.Commit(); err != nil {
		logger.Error("Failed to commit transaction for deleting node", zap.String("node_id", nodeID), zap.Error(err))
		InternalErrorResponse(c, "提交事务失败")
		return
	}
	committed = true
	if h.printJobRepo != nil {
		activeCount, err := h.printJobRepo.CountActiveJobsByEdgeNodeID(nodeID)
		if err != nil {
			logger.Error("Failed to count active jobs after deleting edge node", zap.String("node_id", nodeID), zap.Error(err))
			InternalErrorResponse(c, "检查活动任务失败")
			return
		}
		if activeCount > 0 {
			if err := h.settleActiveJobs(nodeID, "edge_node_deleted"); err != nil {
				logger.Error("Failed to settle active jobs after deleting edge node", zap.String("node_id", nodeID), zap.Error(err))
				InternalErrorResponse(c, "收敛活动任务失败")
				return
			}
		}
	}

	// 关闭该节点的 WebSocket 连接（如果存在）
	// 这个操作在事务提交后执行，即使失败也不影响数据库操作
	if h.wsManager != nil {
		if err := h.wsManager.DisconnectNode(nodeID); err != nil {
			if err.Error() != "edge node not connected" {
				logger.Warn("Failed to disconnect WebSocket for deleted node", zap.String("node_id", nodeID), zap.Error(err))
			}
		} else {
			logger.Info("WebSocket connection closed for deleted node", zap.String("node_id", nodeID))
		}
	}

	logger.Info("Edge Node deleted successfully", zap.String("node_id", nodeID))
	SuccessResponse(c, gin.H{"message": "Edge Node 删除成功"})
}
