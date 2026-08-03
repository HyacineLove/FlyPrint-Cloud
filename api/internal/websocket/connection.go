package websocket

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/operations"
	"fly-print-cloud/api/internal/security"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	// 写入等待时间
	writeWait = 10 * time.Second

	// Pong 等待时间
	pongWait = 60 * time.Second

	// Ping 发送间隔
	pingPeriod = (pongWait * 9) / 10

	// 最大消息大小
	maxMessageSize = 1024 * 1024

	// ACK 等待超时时间
	ackTimeout = 5 * time.Second
)

// Connection 表示单个 WebSocket 连接
type Connection struct {
	NodeID              string
	Conn                *websocket.Conn
	Send                chan []byte
	done                chan struct{}
	closeOnce           sync.Once
	Manager             *ConnectionManager
	PrinterRepo         *database.PrinterRepository
	EdgeNodeRepo        *database.EdgeNodeRepository
	PrintJobRepo        *database.PrintJobRepository
	FileRepo            *database.FileRepository
	TokenManager        *security.TokenManager
	StatusService       *operations.StatusService
	Receipts            *database.EdgeJobUpdateReceiptRepository
	TerminalSessions    *database.TerminalSessionRepository
	TerminalTickets     *database.TerminalTicketRepository
	UploadSessions      *database.TerminalUploadSessionRepository
	Callbacks           *database.IntegrationCallbackRepository
	IntegrationRequests *database.IntegrationPrintRequestRepository

	// ACK 机制相关
	pendingAcks map[string]chan string
	ackMutex    sync.Mutex
}

// NewConnection 创建新连接
func NewConnection(nodeID string, conn *websocket.Conn, manager *ConnectionManager, printerRepo *database.PrinterRepository, edgeNodeRepo *database.EdgeNodeRepository, printJobRepo *database.PrintJobRepository, fileRepo *database.FileRepository, tokenManager *security.TokenManager, statusService *operations.StatusService, receipts *database.EdgeJobUpdateReceiptRepository, terminalSessions *database.TerminalSessionRepository, terminalTickets *database.TerminalTicketRepository, uploadSessions *database.TerminalUploadSessionRepository, callbacks *database.IntegrationCallbackRepository, integrationRequests *database.IntegrationPrintRequestRepository) *Connection {
	return &Connection{
		NodeID:              nodeID,
		Conn:                conn,
		Send:                make(chan []byte, 256),
		done:                make(chan struct{}),
		Manager:             manager,
		PrinterRepo:         printerRepo,
		EdgeNodeRepo:        edgeNodeRepo,
		PrintJobRepo:        printJobRepo,
		FileRepo:            fileRepo,
		TokenManager:        tokenManager,
		StatusService:       statusService,
		Receipts:            receipts,
		TerminalSessions:    terminalSessions,
		TerminalTickets:     terminalTickets,
		UploadSessions:      uploadSessions,
		Callbacks:           callbacks,
		IntegrationRequests: integrationRequests,
		pendingAcks:         make(map[string]chan string),
	}
}

// Close is the only lifecycle exit for a connection. The send queue is never
// closed: producers can therefore observe done instead of racing a close.
func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.Conn.Close()
	})
}

func (c *Connection) enqueue(message []byte) error {
	select {
	case <-c.done:
		return ErrConnectionClosed
	default:
	}
	select {
	case <-c.done:
		return ErrConnectionClosed
	case c.Send <- message:
		return nil
	default:
		return ErrConnectionQueueFull
	}
}

// ReadPump 处理从客户端读取消息
func (c *Connection) ReadPump() {
	defer func() {
		c.Manager.unregister <- c
		c.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, messageBytes, err := c.Conn.ReadMessage()
		if err != nil {
			logger.Error("WebSocket read error", zap.String("node_id", c.NodeID), zap.Error(err))
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Warn("WebSocket unexpected close error", zap.String("node_id", c.NodeID), zap.Error(err))
			}
			break
		}

		// 先解析消息类型
		var msg Message
		if err := json.Unmarshal(messageBytes, &msg); err != nil {
			logger.Error("Failed to parse message from node", zap.String("node_id", c.NodeID), zap.Error(err))
			continue
		}

		logger.Debug("WebSocket parsed message from node", zap.String("node_id", c.NodeID), zap.String("type", msg.Type))

		// ACK 消息特殊处理：msg_id 在顶层而非 data 中
		if msg.Type == MsgTypeAck {
			var ack CommandAck
			if err := json.Unmarshal(messageBytes, &ack); err != nil {
				logger.Error("Failed to parse ACK message from node", zap.String("node_id", c.NodeID), zap.Error(err))
				continue
			}
			c.handleAckDirect(&ack)
		} else {
			// 其他消息正常处理
			c.handleMessage(&msg)
		}
	}
}

// WritePump 处理向客户端发送消息
func (c *Connection) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case <-c.done:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
			return

		case message := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			if _, err := w.Write(message); err != nil {
				_ = w.Close()
				return
			}

			// 批量发送队列中的其他消息
			n := len(c.Send)
			for i := 0; i < n; i++ {
				if _, err := w.Write([]byte{'\n'}); err != nil {
					_ = w.Close()
					return
				}
				select {
				case queued := <-c.Send:
					if _, err := w.Write(queued); err != nil {
						_ = w.Close()
						return
					}
				case <-c.done:
					_ = w.Close()
					return
				}
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage 处理接收到的消息
func (c *Connection) handleMessage(msg *Message) {
	logger.Debug("Received message from node", zap.String("node_id", c.NodeID), zap.String("type", msg.Type))

	// 标准验证顺序（所有消息统一处理）：
	// 1. 节点存在性检查（所有消息都需要，包括 heartbeat 和 job_update）
	// 2. 节点启用检查（heartbeat 和 job_update 放行，允许禁用节点维持连接和完成任务收尾）

	// 步骤1: 检查节点是否存在（所有消息都需要）
	node, err := c.EdgeNodeRepo.GetEdgeNodeByID(c.NodeID)
	if err != nil || node == nil {
		logger.Warn("Message rejected: node not found", zap.String("node_id", c.NodeID), zap.String("message_type", msg.Type))
		c.sendRequestError("node_not_found", "Edge node not found", "", c.uploadTokenRequestID(msg))
		return
	}

	// 步骤2: 检查节点是否启用（heartbeat 和 job_update 放行）
	if msg.Type != MsgTypeHeartbeat && msg.Type != MsgTypeJobUpdate {
		if !node.Enabled {
			logger.Warn("Message rejected: node is disabled", zap.String("node_id", c.NodeID), zap.String("message_type", msg.Type))
			c.sendRequestError("node_disabled", "Edge node has been disabled by administrator", "", c.uploadTokenRequestID(msg))
			return
		}
	}

	switch msg.Type {
	case MsgTypeHeartbeat:
		c.handleHeartbeat(msg)
	case MsgTypeJobUpdate:
		c.handleJobUpdate(msg)
	case MsgTypeSubmitPrintParams:
		c.handleSubmitPrintParams(msg)
	case MsgTypeRequestUploadToken:
		c.handleRequestUploadToken(msg)
	case MsgTypeTerminalSessionState:
		c.handleTerminalSessionState(msg)
	case MsgTypeAck:
		c.handleAck(msg)
	default:
		logger.Warn("Unknown message type from node", zap.String("type", msg.Type), zap.String("node_id", c.NodeID))
	}
}

// uploadTokenRequestID extracts the correlation ID only for upload-token
// requests. Other messages intentionally remain uncorrelated error events.
func (c *Connection) uploadTokenRequestID(msg *Message) string {
	if msg == nil || msg.Type != MsgTypeRequestUploadToken {
		return ""
	}
	data, err := json.Marshal(msg.Data)
	if err != nil {
		return ""
	}
	var payload RequestUploadTokenPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	return payload.RequestID
}

func (c *Connection) handleTerminalSessionState(msg *Message) {
	if c.TerminalSessions == nil {
		logger.Error("Terminal session repository unavailable", zap.String("node_id", c.NodeID))
		return
	}
	var payload TerminalSessionStateData
	data, err := json.Marshal(msg.Data)
	if err != nil || json.Unmarshal(data, &payload) != nil {
		logger.Warn("Invalid terminal session state", zap.String("node_id", c.NodeID))
		return
	}
	if payload.TerminalTicketHash != "" && len(payload.TerminalTicketHash) != 64 {
		logger.Warn("Invalid terminal ticket hash in session state", zap.String("node_id", c.NodeID))
		return
	}
	if payload.IntegrationRequestID != "" {
		if _, err := uuid.Parse(payload.IntegrationRequestID); err != nil {
			logger.Warn("Invalid integration request ID in session state", zap.String("node_id", c.NodeID))
			return
		}
	}

	previous, _ := c.TerminalSessions.Get(c.NodeID)
	sessionChanged := previous == nil || previous.TerminalSessionID != payload.TerminalSessionID
	if sessionChanged {
		if c.TerminalTickets != nil {
			if err := c.TerminalTickets.CancelActiveForNode(c.NodeID); err != nil {
				logger.Warn("Failed to cancel terminal tickets on session refresh", zap.String("node_id", c.NodeID), zap.Error(err))
			}
		}
		if c.UploadSessions != nil {
			if err := c.UploadSessions.DeleteForNode(c.NodeID); err != nil {
				logger.Warn("Failed to revoke upload sessions on terminal refresh", zap.String("node_id", c.NodeID), zap.Error(err))
			}
		}
		if c.Manager != nil {
			c.Manager.ClearTerminalOccupied(c.NodeID)
		}
	}

	if err := c.TerminalSessions.Report(c.NodeID, payload.TerminalSessionID, payload.TerminalTicketHash, payload.EntryType, payload.IntegrationRequestID, time.Now()); err != nil {
		logger.Error("Failed to persist terminal session state", zap.String("node_id", c.NodeID), zap.Error(err))
		return
	}

	// Reconnect recovery only: active ticket + Edge missing ticket proof.
	// Do not replay when Edge already bound the same hash (avoids occupy flood).
	if payload.TerminalSessionID != "" && c.TerminalTickets != nil && c.Manager != nil {
		ticket, err := c.TerminalTickets.GetActiveForSession(c.NodeID, payload.TerminalSessionID, time.Now())
		if err == nil && ticket != nil {
			edgeHasTicket := payload.TerminalTicketHash != "" && payload.TerminalTicketHash == ticket.TicketHash
			c.Manager.ReplayTerminalOccupiedIfNeeded(c.NodeID, TerminalOccupiedPayload{
				TerminalSessionID:  ticket.TerminalSessionID,
				TerminalTicketHash: ticket.TicketHash,
				ExpiresAt:          ticket.ExpiresAt,
			}, edgeHasTicket)
		}
	}
}

// handleAck 处理确认消息（已废弃，使用 handleAckDirect）
func (c *Connection) handleAck(msg *Message) {
	logger.Warn("handleAck called via handleMessage (deprecated path)", zap.String("node_id", c.NodeID))
	// 这个路径已被 ReadPump 中的直接解析替代，不应该到达这里
}

// handleAckDirect 直接处理已解析的 ACK 消息
func (c *Connection) handleAckDirect(ack *CommandAck) {
	if ack.MsgID == "" {
		logger.Warn("Received ACK without MsgID from node", zap.String("node_id", c.NodeID))
		return
	}

	c.ackMutex.Lock()
	defer c.ackMutex.Unlock()

	if ch, exists := c.pendingAcks[ack.MsgID]; exists {
		// 非阻塞投递 ACK status（等待者可能已超时删除该 entry）
		select {
		case ch <- ack.Status:
		default:
		}
		delete(c.pendingAcks, ack.MsgID)
		logger.Debug("Received ACK for message from node", zap.String("msg_id", ack.MsgID), zap.String("node_id", c.NodeID), zap.String("command_id", ack.CommandID), zap.String("status", ack.Status))
	} else {
		logger.Warn("Received ACK for unknown or timed-out message", zap.String("msg_id", ack.MsgID), zap.String("node_id", c.NodeID))
	}
}

// SendCommandWithAck 发送指令并等待确认
func (c *Connection) SendCommandWithAck(cmd *Command, timeout time.Duration) error {
	_, err := c.SendCommandWithAckStatus(cmd, timeout)
	return err
}

// SendCommandWithAckStatus 发送指令并等待确认，返回 ACK 的 status（accepted/rejected）。
func (c *Connection) SendCommandWithAckStatus(cmd *Command, timeout time.Duration) (string, error) {
	if cmd.MsgID == "" {
		cmd.MsgID = uuid.New().String()
	}

	ackCh := make(chan string, 1)

	c.ackMutex.Lock()
	c.pendingAcks[cmd.MsgID] = ackCh
	c.ackMutex.Unlock()

	// 无论成功与否，都要确保从 map 中移除（避免内存泄漏）
	defer func() {
		c.ackMutex.Lock()
		delete(c.pendingAcks, cmd.MsgID)
		c.ackMutex.Unlock()
	}()

	logger.Debug("Sending command to node with ACK expectation", zap.String("command_type", cmd.Type), zap.String("msg_id", cmd.MsgID), zap.String("node_id", c.NodeID))
	if err := c.SendCommand(cmd); err != nil {
		return "", err
	}

	select {
	case status := <-ackCh:
		return status, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("%w: message %s", ErrAckTimeout, cmd.MsgID)
	}
}

// handleSubmitPrintParams 处理提交打印参数消息
func (c *Connection) handleSubmitPrintParams(msg *Message) {
	logger.Debug("Processing print params from node", zap.String("node_id", c.NodeID))

	var payload SubmitPrintParamsPayload

	// Check if msg.Data is already a map or needs unmarshalling
	if dataMap, ok := msg.Data.(map[string]interface{}); ok {
		// Convert map to struct manually or re-marshal/unmarshal
		dataBytes, err := json.Marshal(dataMap)
		if err != nil {
			logger.Error("Failed to marshal submit print params map from node", zap.String("node_id", c.NodeID), zap.Error(err))
			return
		}
		if err := json.Unmarshal(dataBytes, &payload); err != nil {
			logger.Error("Failed to unmarshal submit print params map from node", zap.String("node_id", c.NodeID), zap.Error(err))
			return
		}
	} else {
		// Try to marshal whatever it is (e.g. string) and unmarshal
		dataBytes, err := json.Marshal(msg.Data)
		if err != nil {
			logger.Error("Failed to marshal submit print params data from node", zap.String("node_id", c.NodeID), zap.Error(err))
			return
		}
		if err := json.Unmarshal(dataBytes, &payload); err != nil {
			logger.Error("Failed to parse submit print params data from node", zap.String("node_id", c.NodeID), zap.Error(err))
			return
		}
	}

	// 验证必要字段
	if payload.FileID == "" || payload.PrinterID == "" {
		logger.Warn("Missing required fields in submit print params from node", zap.String("node_id", c.NodeID))
		return
	}

	if payload.IntegrationRequestID != "" {
		if c.IntegrationRequests == nil || payload.TerminalSessionID == "" || payload.TerminalTicketHash == "" {
			c.sendError("integration_context_mismatch", "Integration terminal context is incomplete", payload.PrinterID)
			return
		}
		confirmation := database.IntegrationPrintConfirmation{
			RequestID: payload.IntegrationRequestID, NodeID: c.NodeID, FileID: payload.FileID, PrinterID: payload.PrinterID,
			TerminalSessionID: payload.TerminalSessionID, TerminalTicketHash: payload.TerminalTicketHash,
			Copies: optionInt(payload.Options, "copies", 1), PaperSize: optionString(payload.Options, "paper_size"),
			ColorMode: optionString(payload.Options, "color_mode"), DuplexMode: optionString(payload.Options, "duplex_mode"),
		}
		job, created, err := c.IntegrationRequests.ConfirmAndCreateJob(confirmation)
		if err != nil {
			logger.Warn("Integration print confirmation rejected", zap.String("request_id", payload.IntegrationRequestID), zap.Error(err))
			c.sendError("integration_context_mismatch", "Integration request no longer matches this terminal", payload.PrinterID)
			return
		}
		if !created {
			return
		}
		go DispatchPrintJobAndRecord(c.Manager, c.PrintJobRepo, c.StatusService, job, c.NodeID, func() error {
			return c.IntegrationRequests.MarkDispatched(payload.IntegrationRequestID, job.ID)
		})
		return
	}

	// 获取文件信息
	file, err := c.FileRepo.GetByID(payload.FileID)
	if err != nil {
		logger.Error("Failed to get file", zap.String("file_id", payload.FileID), zap.Error(err))
		return
	}
	if file == nil {
		logger.Warn("File not found", zap.String("file_id", payload.FileID))
		return
	}
	if c.UploadSessions != nil {
		binding, bindingErr := c.UploadSessions.GetByFileID(file.ID)
		if bindingErr != nil {
			logger.Warn("Failed to load official terminal binding", zap.String("file_id", file.ID), zap.Error(bindingErr))
			c.sendError("terminal_session_invalid", "Terminal session is no longer valid", payload.PrinterID)
			return
		}
		if binding != nil {
			if payload.TerminalSessionID == "" || payload.TerminalTicketHash == "" || payload.TerminalSessionID != binding.SessionID || payload.TerminalTicketHash != binding.TicketHash {
				c.sendError("terminal_session_invalid", "Terminal session is no longer valid", payload.PrinterID)
				return
			}
			if c.TerminalSessions == nil {
				c.sendError("terminal_session_invalid", "Terminal session is no longer valid", payload.PrinterID)
				return
			}
			matched, matchErr := c.TerminalSessions.Matches(c.NodeID, payload.TerminalSessionID, payload.TerminalTicketHash, "")
			if matchErr != nil || !matched {
				c.sendError("terminal_session_invalid", "Terminal session is no longer valid", payload.PrinterID)
				return
			}
		}
	}

	// 重新生成文件URL（因为DB不存储URL）
	file.URL = buildProxyFileURL(file.ID)

	// 标准验证顺序：节点存在性 → 打印机存在性 → 节点启用 → 打印机启用
	// 注意：节点存在性和启用已由 handleMessage 通用拦截器处理

	// 步骤1: 检查打印机是否存在
	printer, err := c.PrinterRepo.GetPrinterByID(payload.PrinterID)
	if err != nil {
		logger.Error("Failed to get printer", zap.String("printer_id", payload.PrinterID), zap.Error(err))
		c.sendError("printer_not_found", "Printer not found", payload.PrinterID)
		return
	}
	if printer == nil {
		logger.Warn("Printer not found", zap.String("printer_id", payload.PrinterID))
		c.sendError("printer_not_found", "Printer not found", payload.PrinterID)
		return
	}

	// 步骤2: 验证打印机是否属于该节点
	if printer.EdgeNodeID != c.NodeID {
		logger.Warn("Printer does not belong to node", zap.String("printer_id", payload.PrinterID), zap.String("node_id", c.NodeID))
		c.sendError("printer_not_belong_to_node", "Printer does not belong to this node", payload.PrinterID)
		return
	}

	// 步骤3: 检查打印机是否被禁用
	if !printer.Enabled {
		logger.Warn("Printer disabled, rejecting print job submission", zap.String("printer_id", payload.PrinterID), zap.String("node_id", c.NodeID))
		c.sendError("printer_disabled", "Printer has been disabled by administrator", payload.PrinterID)
		return
	}

	// 创建打印任务
	job := &models.PrintJob{
		Name:        file.OriginalName,
		Status:      "pending",
		PrinterID:   payload.PrinterID,
		UserID:      file.UploaderID,
		UserName:    getUserDisplayName(file.UploaderID), // 从用户ID获取显示名称
		FilePath:    file.FilePath,
		FileURL:     file.URL,
		ContentHash: file.ContentHash,
		FileSize:    file.Size,
		Copies:      1,
		MaxRetries:  3,
	}

	// 设置 Options
	if val, ok := payload.Options["copies"]; ok {
		if v, ok := val.(float64); ok {
			job.Copies = int(v)
		}
	}
	if val, ok := payload.Options["paper_size"]; ok {
		if v, ok := val.(string); ok {
			job.PaperSize = v
		}
	}
	if val, ok := payload.Options["color_mode"]; ok {
		if v, ok := val.(string); ok {
			job.ColorMode = v
		}
	}
	if val, ok := payload.Options["duplex_mode"]; ok {
		if v, ok := val.(string); ok {
			job.DuplexMode = v
		}
	}
	if val, ok := payload.Options["page_count"]; ok {
		if v, ok := val.(float64); ok {
			job.PageCount = int(v)
		}
	}

	// 保存任务
	if err := c.PrintJobRepo.CreatePrintJob(job); err != nil {
		logger.Error("Failed to create print job", zap.Error(err))
		return
	}

	logger.Info("Print job created for file", zap.String("job_id", job.ID), zap.String("file_id", file.ID))

	// 分发必须离开读取循环执行，否则读取循环无法消费 Edge 返回的 ACK。
	go DispatchPrintJobAndRecord(c.Manager, c.PrintJobRepo, c.StatusService, job, printer.EdgeNodeID)
}

// handleHeartbeat 处理心跳消息
func optionInt(options map[string]interface{}, key string, fallback int) int {
	if value, ok := options[key].(float64); ok && value >= 1 {
		return int(value)
	}
	return fallback
}

func optionString(options map[string]interface{}, key string) string {
	value, _ := options[key].(string)
	return value
}

func (c *Connection) handleHeartbeat(msg *Message) {
	logger.Debug("Processing heartbeat from node", zap.String("node_id", c.NodeID))

	// 更新 Edge Node 的最后心跳时间和状态

	// 解析心跳数据（可选）
	var heartbeatData HeartbeatData
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil || json.Unmarshal(dataBytes, &heartbeatData) != nil {
		logger.Warn("Invalid heartbeat payload", zap.String("node_id", c.NodeID))
		return
	}
	if c.StatusService == nil {
		logger.Error("Status service unavailable")
		return
	}
	err = c.StatusService.ApplyHeartbeat(c.NodeID, operations.HeartbeatSnapshot{
		CPUUsage:       heartbeatData.SystemInfo.CPUUsage,
		MemoryUsage:    heartbeatData.SystemInfo.MemoryUsage,
		DiskUsage:      heartbeatData.SystemInfo.DiskUsage,
		NetworkQuality: heartbeatData.SystemInfo.NetworkQuality,
		Latency:        heartbeatData.SystemInfo.Latency,
		Components:     heartbeatData.Components,
	})
	if err != nil {
		logger.Error("Failed to apply heartbeat", zap.String("node_id", c.NodeID), zap.Error(err))
		return
	}

	logger.Debug("Successfully processed heartbeat from node", zap.String("node_id", c.NodeID))
}

// handleRequestUploadToken 处理请求上传凭证消息
func (c *Connection) handleRequestUploadToken(msg *Message) {
	logger.Debug("Processing upload token request from node", zap.String("node_id", c.NodeID))

	// 标准验证顺序：节点存在性 → 节点启用 → 打印机存在性 → 打印机归属 → 打印机启用
	// 注意：节点存在性和启用已由 handleMessage 通用拦截器处理

	// 解析请求数据
	var payload RequestUploadTokenPayload
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		logger.Error("Failed to marshal upload token request data from node", zap.String("node_id", c.NodeID), zap.Error(err))
		c.sendError("invalid_request", "Failed to parse request data", "")
		return
	}

	if err := json.Unmarshal(dataBytes, &payload); err != nil {
		logger.Error("Failed to parse upload token request data from node", zap.String("node_id", c.NodeID), zap.Error(err))
		c.sendError("invalid_request", "Failed to parse request data", "")
		return
	}

	// 验证 printer_id 必填
	if payload.PrinterID == "" {
		logger.Warn("Missing printer_id in upload token request from node", zap.String("node_id", c.NodeID))
		c.sendRequestError("invalid_request", "printer_id is required", "", payload.RequestID)
		return
	}

	// 步骤3: 检查打印机是否存在
	printer, err := c.PrinterRepo.GetPrinterByID(payload.PrinterID)
	if err != nil {
		logger.Error("Failed to get printer", zap.String("printer_id", payload.PrinterID), zap.Error(err))
		c.sendRequestError("printer_not_found", "Printer not found", payload.PrinterID, payload.RequestID)
		return
	}
	if printer == nil {
		logger.Warn("Printer not found", zap.String("printer_id", payload.PrinterID))
		c.sendRequestError("printer_not_found", "Printer not found", payload.PrinterID, payload.RequestID)
		return
	}

	// 步骤4: 验证打印机是否属于该节点
	if printer.EdgeNodeID != c.NodeID {
		logger.Warn("Printer does not belong to node", zap.String("printer_id", payload.PrinterID), zap.String("node_id", c.NodeID))
		c.sendRequestError("printer_not_belong_to_node", "Printer does not belong to this node", payload.PrinterID, payload.RequestID)
		return
	}

	// 步骤5: 检查打印机是否被禁用
	if !printer.Enabled {
		logger.Warn("Printer disabled, rejecting upload token request", zap.String("printer_id", payload.PrinterID), zap.String("node_id", c.NodeID))
		c.sendRequestError("printer_disabled", "Printer has been disabled by administrator", payload.PrinterID, payload.RequestID)
		return
	}
	node, err := c.EdgeNodeRepo.GetEdgeNodeByID(c.NodeID)
	if err != nil || node == nil {
		c.sendRequestError("node_not_found", "Edge node not found", payload.PrinterID, payload.RequestID)
		return
	}
	if reason := operations.ValidatePrinterDispatch(printer, node, time.Now()); reason != "" {
		c.sendRequestError(reason, "Printer cannot accept a new task", payload.PrinterID, payload.RequestID)
		return
	}

	// 生成上传凭证
	token, expiresAt, err := c.TokenManager.GenerateUploadToken(c.NodeID, payload.PrinterID)
	if err != nil {
		logger.Error("Failed to generate upload token for node", zap.String("node_id", c.NodeID), zap.Error(err))
		c.sendRequestError("token_generation_failed", "Failed to generate upload token", "", payload.RequestID)
		return
	}

	// 构造两个URL（相对于 API 根路径，不包含网关前缀）
	// 1. API上传URL：用于Edge端程序化上传（POST请求）
	apiUploadURL := fmt.Sprintf("/api/v1/files?token=%s", token)

	// 2. Web上传页面URL：用于生成二维码/链接给用户（GET请求）
	webUploadURL := fmt.Sprintf("/entry?token=%s&node_id=%s&printer_id=%s", token, c.NodeID, payload.PrinterID)

	// 发送上传凭证响应
	response := map[string]interface{}{
		"type": CmdTypeUploadToken,
		"data": UploadTokenResponsePayload{
			RequestID: payload.RequestID,
			Token:     token,
			ExpiresAt: expiresAt,
			UploadURL: apiUploadURL, // API上传端点
			WebURL:    webUploadURL, // Web上传页面
			NodeID:    c.NodeID,
			PrinterID: payload.PrinterID,
		},
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		logger.Error("Failed to marshal upload token response", zap.Error(err))
		return
	}

	if err := c.enqueue(responseBytes); err != nil {
		logger.Warn("Unable to send upload token response", zap.String("node_id", c.NodeID), zap.Error(err))
		return
	}
	logger.Info("Upload token generated", zap.String("node_id", c.NodeID), zap.String("printer_id", payload.PrinterID), zap.String("api_url", apiUploadURL), zap.String("web_url", webUploadURL))
}

// sendError 发送错误消息到 Edge 节点
func (c *Connection) sendError(code, message, printerID string) {
	c.sendRequestError(code, message, printerID, "")
}

func (c *Connection) sendRequestError(code, message, printerID, requestID string) {
	errorData := map[string]interface{}{
		"code":    code,
		"message": message,
	}
	if printerID != "" {
		errorData["printer_id"] = printerID
	}
	if requestID != "" {
		errorData["request_id"] = requestID
	}

	errorMsg := map[string]interface{}{
		"type": CmdTypeError,
		"data": errorData,
	}

	errorBytes, _ := json.Marshal(errorMsg)
	if err := c.enqueue(errorBytes); err != nil {
		logger.Warn("Unable to send error to edge node", zap.String("node_id", c.NodeID), zap.Error(err))
	}
}

// handleJobUpdate 处理任务状态更新
func (c *Connection) handleJobUpdate(msg *Message) {
	logger.Debug("Processing job update from node", zap.String("node_id", c.NodeID))

	// 解析任务状态数据
	var jobData JobUpdateData
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		logger.Error("Failed to marshal job update data from node", zap.String("node_id", c.NodeID), zap.Error(err))
		return
	}

	if err := json.Unmarshal(dataBytes, &jobData); err != nil {
		logger.Error("Failed to parse job update data from node", zap.String("node_id", c.NodeID), zap.Error(err))
		return
	}

	var errMsg string
	if jobData.ErrorMessage != nil {
		errMsg = *jobData.ErrorMessage
	}

	logger.Debug("Job update data", zap.String("job_id", jobData.JobID), zap.String("status", jobData.Status), zap.String("error_code", jobData.ErrorCode), zap.String("error", errMsg))

	existingJob, err := c.PrintJobRepo.GetPrintJobByID(jobData.JobID)
	if err != nil {
		logger.Error("Failed to get current job before update", zap.String("job_id", jobData.JobID), zap.Error(err))
		c.sendJobUpdateAck(jobData.EventID, jobData.JobID, "rejected", "job_lookup_failed")
		return
	}
	if existingJob == nil {
		logger.Warn("Job not found when handling status update", zap.String("job_id", jobData.JobID))
		c.sendJobUpdateAck(jobData.EventID, jobData.JobID, "rejected", "job_not_found")
		return
	}
	printer, err := c.PrinterRepo.GetPrinterByID(existingJob.PrinterID)
	if err != nil || printer == nil || printer.EdgeNodeID != c.NodeID {
		logger.Warn("Rejecting job update for a job not owned by this node", zap.String("job_id", jobData.JobID), zap.String("node_id", c.NodeID))
		c.sendJobUpdateAck(jobData.EventID, jobData.JobID, "rejected", "job_node_mismatch")
		return
	}
	if c.Callbacks != nil {
		matched, err := c.Callbacks.ValidateJobContext(jobData.JobID, jobData.TerminalSessionID, jobData.TerminalTicketHash, jobData.IntegrationRequestID)
		if err != nil || !matched {
			logger.Warn("Ignoring job update with invalid integration terminal context", zap.String("job_id", jobData.JobID), zap.String("node_id", c.NodeID))
			c.sendJobUpdateAck(jobData.EventID, jobData.JobID, "rejected", "terminal_context_mismatch")
			return
		}
	}

	if isTerminalJobStatus(jobData.Status) {
		if _, err := uuid.Parse(jobData.EventID); err != nil {
			logger.Warn("Rejecting terminal job update without a valid event ID", zap.String("job_id", jobData.JobID))
			c.sendJobUpdateAck(jobData.EventID, jobData.JobID, "rejected", "event_id_required")
			return
		}
		if c.StatusService == nil || c.Receipts == nil {
			logger.Error("Terminal job-update persistence unavailable", zap.String("job_id", jobData.JobID))
			return
		}
		result, err := c.StatusService.ApplyTerminalJobUpdate(c.Receipts, operations.TerminalJobUpdate{
			EventID: jobData.EventID, PayloadHash: terminalJobUpdatePayloadHash(jobData), NodeID: c.NodeID,
			JobID: jobData.JobID, Status: jobData.Status, ErrorCode: jobData.ErrorCode, ErrorMessage: errMsg,
			ImpressionsCompleted: jobData.ImpressionsCompleted, SheetsCompleted: jobData.SheetsCompleted,
			QuotaConsumed: jobData.QuotaConsumed,
		})
		if err != nil {
			logger.Error("Failed to persist terminal job update", zap.String("job_id", jobData.JobID), zap.Error(err))
			return
		}
		if !result.Accepted {
			c.sendJobUpdateAck(jobData.EventID, jobData.JobID, "rejected", result.Reason)
			return
		}
		if result.Changed && c.Callbacks != nil {
			integrationStatus := map[string]string{"completed": "completed", "failed": "failed", "canceled": "cancelled", "unconfirmed": "failed"}[jobData.Status]
			if integrationStatus != "" {
				_ = c.Callbacks.TransitionForJob(jobData.JobID, integrationStatus, jobData.ErrorCode, errMsg)
			}
		}
		c.sendJobUpdateAck(jobData.EventID, jobData.JobID, "accepted", "")
		logger.Info("Terminal job update accepted", zap.String("job_id", jobData.JobID), zap.String("status", jobData.Status), zap.String("event_id", jobData.EventID))
		return
	}

	// Process-state reports are intentionally realtime-only. They do not use the
	// durable terminal receipt protocol and cannot overwrite a terminal result.
	dispatchUnconfirmed := existingJob.Status == "unconfirmed" &&
		(existingJob.ErrorCode == "dispatch_ack_timeout" || existingJob.ErrorCode == "print_timeout_unconfirmed")
	if (existingJob.Status == "completed" || existingJob.Status == "failed" || existingJob.Status == "canceled" || existingJob.Status == "unconfirmed") && !dispatchUnconfirmed {
		logger.Debug("Ignoring late status update for terminal job", zap.String("job_id", jobData.JobID), zap.String("current_status", existingJob.Status), zap.String("incoming_status", jobData.Status))
		return
	}

	if c.StatusService == nil {
		logger.Error("Status service unavailable")
		return
	}
	if err := c.StatusService.ApplyJobResult(jobData.JobID, c.NodeID, existingJob.PrinterID, jobData.Status, jobData.ErrorCode, map[string]interface{}{"message": errMsg}); err != nil {
		logger.Error("Failed to update job status", zap.String("job_id", jobData.JobID), zap.Error(err))
		return
	}
	if c.Callbacks != nil {
		integrationStatus := map[string]string{"processing": "printing", "completed": "completed", "failed": "failed", "canceled": "cancelled", "unconfirmed": "failed"}[jobData.Status]
		if integrationStatus != "" {
			_ = c.Callbacks.TransitionForJob(jobData.JobID, integrationStatus, jobData.ErrorCode, errMsg)
		}
	}

	logger.Info("Successfully updated job status", zap.String("job_id", jobData.JobID), zap.String("status", jobData.Status))
}

func isTerminalJobStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled" || status == "unconfirmed"
}

func terminalJobUpdatePayloadHash(data JobUpdateData) string {
	message := ""
	if data.ErrorMessage != nil {
		message = *data.ErrorMessage
	}
	plain := strings.Join([]string{
		data.JobID, data.Status, data.ErrorCode, message,
		fmt.Sprintf("%d", data.ImpressionsCompleted), fmt.Sprintf("%d", data.SheetsCompleted),
		fmt.Sprintf("%d", data.QuotaConsumed),
		data.TerminalSessionID, data.TerminalTicketHash, data.IntegrationRequestID,
	}, "\n")
	sum := sha256.Sum256([]byte(plain))
	return fmt.Sprintf("%x", sum[:])
}

func (c *Connection) sendJobUpdateAck(eventID, jobID, status, reason string) {
	if eventID == "" {
		return
	}
	message, err := json.Marshal(Message{Type: CmdTypeJobUpdateAck, NodeID: c.NodeID, Timestamp: time.Now(), Data: JobUpdateAckPayload{EventID: eventID, JobID: jobID, Status: status, Reason: reason}})
	if err != nil {
		logger.Error("Failed to marshal job-update acknowledgement", zap.Error(err))
		return
	}
	if err := c.enqueue(message); err != nil {
		logger.Warn("Unable to send job-update acknowledgement", zap.String("node_id", c.NodeID), zap.String("event_id", eventID), zap.Error(err))
	}
}

// SendCommand 发送指令到 Edge Node
func (c *Connection) SendCommand(cmd *Command) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	return c.enqueue(data)
}

// getUserDisplayName 从用户ID获取显示名称
// 如果无法获取用户名，返回默认值
func getUserDisplayName(userID string) string {
	if userID == "" {
		return "Unknown User"
	}

	// 注意：这里需要注入 UserRepository 来查询用户名
	// 为了避免循环依赖，暂时使用简单的显示方式
	// 在实际使用中，应该在 ConnectionManager 中注入 UserRepository
	// 并通过 Connection 访问

	// 简单处理：如果是 UUID 格式，显示前8位
	if len(userID) > 8 {
		return "User-" + userID[:8]
	}

	return "User-" + userID
}
