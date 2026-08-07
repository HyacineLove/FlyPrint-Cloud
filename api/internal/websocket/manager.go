package websocket

import (
	"encoding/json"
	"sync"
	"time"

	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/operations"
	"fly-print-cloud/api/internal/security"

	"go.uber.org/zap"
)

// ConnectionManager 管理所有 WebSocket 连接
type ConnectionManager struct {
	connections   map[string]*Connection // node_id -> connection
	broadcast     chan []byte            // 广播消息通道
	register      chan *Connection       // 新连接注册
	unregister    chan *Connection       // 连接断开
	mutex         sync.RWMutex           // 并发安全
	TokenManager  *security.TokenManager // 凭证管理器
	StatusService *operations.StatusService

	occupiedMu          sync.Mutex
	pendingOccupied     map[string]*TerminalOccupiedPayload // node_id -> pending occupy until ACK
	occupiedDispatching map[string]bool                     // node_id -> ACK wait currently in flight
}

// NewConnectionManager 创建连接管理器
func NewConnectionManager(tokenManager *security.TokenManager, statusService *operations.StatusService) *ConnectionManager {
	return &ConnectionManager{
		connections:         make(map[string]*Connection),
		broadcast:           make(chan []byte),
		register:            make(chan *Connection),
		unregister:          make(chan *Connection),
		TokenManager:        tokenManager,
		StatusService:       statusService,
		pendingOccupied:     make(map[string]*TerminalOccupiedPayload),
		occupiedDispatching: make(map[string]bool),
	}
}

// Run 启动连接管理器
func (m *ConnectionManager) Run() {
	for {
		select {
		case conn := <-m.register:
			m.registerConnection(conn)

		case conn := <-m.unregister:
			m.unregisterConnection(conn)

		case message := <-m.broadcast:
			m.broadcastMessage(message)
		}
	}
}

// registerConnection 注册新连接
func (m *ConnectionManager) registerConnection(conn *Connection) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// 如果已有连接，先关闭旧连接
	if existingConn, exists := m.connections[conn.NodeID]; exists {
		logger.Info("Replacing existing connection for node", zap.String("node_id", conn.NodeID))
		existingConn.Close()
	}

	m.connections[conn.NodeID] = conn
	logger.Info("Edge Node connected", zap.String("node_id", conn.NodeID), zap.Int("total_connections", len(m.connections)))

}

// unregisterConnection 注销连接
func (m *ConnectionManager) unregisterConnection(conn *Connection) {
	m.mutex.Lock()
	wasCurrent := false
	if currentConn, exists := m.connections[conn.NodeID]; exists {
		// 只有当要注销的连接是当前映射中的连接时才删除
		// 避免新连接注册后，旧连接注销导致新连接被误删
		if currentConn == conn {
			delete(m.connections, conn.NodeID)
			wasCurrent = true
			logger.Info("Edge Node disconnected", zap.String("node_id", conn.NodeID), zap.Int("total_connections", len(m.connections)))
		} else {
			logger.Debug("Ignored unregister request for replaced connection of node", zap.String("node_id", conn.NodeID))
		}

	}
	m.mutex.Unlock()
	if wasCurrent {
		m.occupiedMu.Lock()
		// A reconnect must be allowed to replay immediately; the old
		// connection's ACK wait may otherwise suppress the replay window.
		delete(m.occupiedDispatching, conn.NodeID)
		m.occupiedMu.Unlock()
		if m.StatusService != nil {
			_ = m.StatusService.MarkUnstable(conn.NodeID)
		}
	}
	conn.Close()
}

// broadcastMessage 广播消息到所有连接
func (m *ConnectionManager) broadcastMessage(message []byte) {
	m.mutex.RLock()
	connections := make(map[string]*Connection, len(m.connections))
	for nodeID, conn := range m.connections {
		connections[nodeID] = conn
	}
	m.mutex.RUnlock()

	for nodeID, conn := range connections {
		if err := conn.enqueue(message); err != nil {
			logger.Warn("Failed to send broadcast message to node", zap.String("node_id", nodeID), zap.Error(err))
			m.unregisterConnection(conn)
		}
	}
}

// SendToNode 发送消息到指定节点
func (m *ConnectionManager) SendToNode(nodeID string, message []byte) error {
	m.mutex.RLock()
	conn, exists := m.connections[nodeID]
	m.mutex.RUnlock()
	if !exists {
		return ErrNodeNotConnected
	}
	if err := conn.enqueue(message); err != nil {
		if err == ErrConnectionQueueFull {
			m.unregisterConnection(conn)
		}
		return err
	}
	return nil
}

// GetConnectedNodes 获取已连接的节点列表
func (m *ConnectionManager) GetConnectedNodes() []string {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	nodes := make([]string, 0, len(m.connections))
	for nodeID := range m.connections {
		nodes = append(nodes, nodeID)
	}
	return nodes
}

// IsNodeConnected 检查节点是否在线
func (m *ConnectionManager) IsNodeConnected(nodeID string) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	_, exists := m.connections[nodeID]
	return exists
}

// DisconnectNode 主动断开指定节点的 WebSocket 连接
// 用于节点删除时清理资源
func (m *ConnectionManager) DisconnectNode(nodeID string) error {
	m.mutex.Lock()
	conn, exists := m.connections[nodeID]
	if !exists {
		m.mutex.Unlock()
		return ErrNodeNotConnected
	}
	// 先从 map 摘除并释放锁，避免 sleep/发送阻塞其他节点的注册与发送。
	delete(m.connections, nodeID)
	m.mutex.Unlock()

	// 发送关闭通知（可选）
	closeMsg := map[string]interface{}{
		"type": "server_close",
		"data": map[string]string{
			"reason":  "node_deleted",
			"message": "Edge node has been deleted by administrator",
		},
	}
	if msgBytes, err := json.Marshal(closeMsg); err == nil {
		if err := conn.enqueue(msgBytes); err == nil {
			// 消息发送成功，等待一小段时间让 Edge 端接收
			time.Sleep(100 * time.Millisecond)
		}
	}

	// 关闭连接
	conn.Close()

	logger.Info("Forcefully disconnected Edge Node (node deleted)", zap.String("node_id", nodeID), zap.Int("total_connections", len(m.connections)))
	return nil
}

// GetConnectionCount 获取连接数量
func (m *ConnectionManager) GetConnectionCount() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return len(m.connections)
}

// DispatchPreviewFile 发送预览文件命令
func (m *ConnectionManager) DispatchPreviewFile(nodeID string, fileID, fileURL, fileName string, fileSize int64, fileType string, contentHash string) error {
	return m.dispatchPreviewFile(nodeID, PreviewFilePayload{FileID: fileID, FileURL: fileURL, FileName: fileName, FileSize: fileSize, FileType: fileType, ContentHash: contentHash})
}

// DispatchOfficialPreview sends an official upload preview with terminal proof
// so Edge can reject stale sessions.
func (m *ConnectionManager) DispatchOfficialPreview(nodeID string, payload PreviewFilePayload) error {
	return m.dispatchPreviewFile(nodeID, payload)
}

// DispatchPortalSessionReady notifies only the selected Edge. The payload has
// a one-time Site Portal claim code and intentionally contains no PRP credential.
func (m *ConnectionManager) DispatchPortalSessionReady(nodeID string, payload PortalSessionReadyPayload) error {
	message, err := json.Marshal(&Message{
		Type:      CmdTypePortalSessionReady,
		NodeID:    nodeID,
		Timestamp: time.Now(),
		Data:      payload,
	})
	if err != nil {
		return err
	}
	return m.SendToNode(nodeID, message)
}

// MarkTerminalOccupied records pending occupy state and pushes terminal_occupied
// with ACK. HTTP entry redirect must not block on the ACK wait.
func (m *ConnectionManager) MarkTerminalOccupied(nodeID string, payload TerminalOccupiedPayload) {
	if nodeID == "" || payload.TerminalSessionID == "" || payload.TerminalTicketHash == "" {
		return
	}
	m.occupiedMu.Lock()
	copied := payload
	m.pendingOccupied[nodeID] = &copied
	m.occupiedMu.Unlock()
	go m.dispatchTerminalOccupiedWithAck(nodeID, payload)
}

// ClearTerminalOccupied drops pending occupy state (QR refresh / session clear).
func (m *ConnectionManager) ClearTerminalOccupied(nodeID string) {
	m.occupiedMu.Lock()
	delete(m.pendingOccupied, nodeID)
	m.occupiedMu.Unlock()
}

// ReplayTerminalOccupiedIfNeeded re-arms occupy after reconnect when Edge has
// not bound the ticket yet. Never re-push when Edge already reported the same
// ticket hash (that path caused a session-report ↔ occupy flood).
func (m *ConnectionManager) ReplayTerminalOccupiedIfNeeded(nodeID string, payload TerminalOccupiedPayload, edgeHasTicket bool) {
	if nodeID == "" || payload.TerminalSessionID == "" {
		return
	}
	m.occupiedMu.Lock()
	pending := m.pendingOccupied[nodeID]
	if pending != nil {
		// ACK still outstanding — leave the in-flight wait alone; only refresh metadata.
		if payload.TerminalTicketHash != "" {
			pending.TerminalTicketHash = payload.TerminalTicketHash
		}
		if !payload.ExpiresAt.IsZero() {
			pending.ExpiresAt = payload.ExpiresAt
		}
		// 若节点当前在线而 pending 仍存在，说明上次发送未确认（断线/ACK 超时），
		// 重连恢复时重新投递；Edge 已绑定同 ticket hash 时不重发（避免 occupy flood）。
		// 距上次 dispatch 在 ACK 窗口（10s）内视为在途等待，不重复投递。
		m.mutex.RLock()
		_, connected := m.connections[nodeID]
		m.mutex.RUnlock()
		inFlight := m.occupiedDispatching[nodeID]
		m.occupiedMu.Unlock()
		if connected && !edgeHasTicket && !inFlight {
			go m.dispatchTerminalOccupiedWithAck(nodeID, payload)
		}
		return
	}
	if edgeHasTicket {
		m.occupiedMu.Unlock()
		return
	}
	copied := payload
	m.pendingOccupied[nodeID] = &copied
	m.occupiedMu.Unlock()
	go m.dispatchTerminalOccupiedWithAck(nodeID, payload)
}

func (m *ConnectionManager) dispatchTerminalOccupiedWithAck(nodeID string, payload TerminalOccupiedPayload) {
	m.occupiedMu.Lock()
	if m.occupiedDispatching[nodeID] {
		m.occupiedMu.Unlock()
		return
	}
	m.occupiedDispatching[nodeID] = true
	m.occupiedMu.Unlock()
	defer func() {
		m.occupiedMu.Lock()
		delete(m.occupiedDispatching, nodeID)
		m.occupiedMu.Unlock()
	}()

	m.mutex.RLock()
	conn, exists := m.connections[nodeID]
	m.mutex.RUnlock()
	if !exists {
		logger.Debug("Deferred terminal_occupied until Edge reconnects", zap.String("node_id", nodeID))
		return
	}
	command := Command{
		Type:      CmdTypeTerminalOccupied,
		CommandID: payload.TerminalSessionID,
		Timestamp: time.Now(),
		Target:    nodeID,
		Data:      payload,
	}
	if err := conn.SendCommandWithAck(&command, 10*time.Second); err != nil {
		logger.Warn("terminal_occupied ACK failed; will retry on session report", zap.String("node_id", nodeID), zap.Error(err))
		return
	}
	m.occupiedMu.Lock()
	if pending := m.pendingOccupied[nodeID]; pending != nil && pending.TerminalSessionID == payload.TerminalSessionID {
		delete(m.pendingOccupied, nodeID)
	}
	m.occupiedMu.Unlock()
	logger.Debug("terminal_occupied acknowledged", zap.String("node_id", nodeID), zap.String("session_id", payload.TerminalSessionID))
}

func (m *ConnectionManager) dispatchPreviewFile(nodeID string, payload PreviewFilePayload) error {
	logger.Debug("Preparing to dispatch preview file to node", zap.String("node_id", nodeID), zap.String("file_name", payload.FileName), zap.String("file_id", payload.FileID))
	fileURL := payload.FileURL

	// 生成文件访问凭证（用于预览）
	if fileURL != "" && m.TokenManager != nil {
		extractedFileID := extractProxyFileID(fileURL)
		if extractedFileID != "" {
			// 生成预览专用 token，使用 "preview" 作为 jobID
			token, expiresAt, err := m.TokenManager.GenerateDownloadToken(extractedFileID, "preview", nodeID)
			if err != nil {
				logger.Error("Failed to generate download token for preview", zap.Error(err))
			} else {
				payload.FileAccessToken = token
				payload.FileAccessTokenExpiresAt = &expiresAt
				logger.Debug("Generated preview download token", zap.Time("expires_at", expiresAt))
			}
		}
	}

	msg := &Message{
		Type:      CmdTypePreviewFile,
		NodeID:    nodeID,
		Timestamp: time.Now(),
		Data:      payload,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		logger.Error("Failed to marshal preview message", zap.Error(err))
		return err
	}

	logger.Debug("Sending preview message to node", zap.String("node_id", nodeID), zap.Int("payload_size", len(msgBytes)))
	return m.SendToNode(nodeID, msgBytes)
}

// DispatchPrintJob 发送打印任务指令
func (m *ConnectionManager) DispatchPrintJob(nodeID string, job *models.PrintJob) error {
	// 兼容入口：未提供 token 时按需生成
	token, expiresAt := m.prepareFileAccessToken(nodeID, job)
	return m.dispatchPrintJob(nodeID, job, token, expiresAt)
}

// prepareFileAccessToken 为 job 生成下载凭证（未提供复用 token 时）。
func (m *ConnectionManager) prepareFileAccessToken(nodeID string, job *models.PrintJob) (string, *time.Time) {
	if job.FileURL == "" || m.TokenManager == nil {
		return "", nil
	}
	fileID := extractProxyFileID(job.FileURL)
	if fileID == "" {
		return "", nil
	}
	token, expiresAt, err := m.TokenManager.GenerateDownloadToken(fileID, job.ID, nodeID)
	if err != nil {
		logger.Error("Failed to generate download token for job", zap.String("job_id", job.ID), zap.Error(err))
		return "", nil
	}
	logger.Debug("Generated download token for job", zap.String("job_id", job.ID), zap.Time("expires_at", expiresAt))
	return token, &expiresAt
}

// dispatchPrintJob 发送打印任务指令；fileAccessToken 可由调用方传入复用，
// 避免重试换发时 revoke 上一轮已下发的在途 token。
func (m *ConnectionManager) dispatchPrintJob(nodeID string, job *models.PrintJob, fileAccessToken string, fileAccessTokenExpiresAt *time.Time) error {
	// 构造打印任务数据
	printJobData := PrintJobData{
		JobID:              job.ID,
		Name:               job.Name,
		PrinterID:          job.PrinterID,
		FilePath:           job.FilePath,
		FileURL:            job.FileURL,
		ContentHash:        job.ContentHash,
		FileSize:           job.FileSize,
		PageCount:          job.PageCount,
		Copies:             job.Copies,
		PaperSize:          job.PaperSize,
		Orientation:        job.Orientation,
		ScalePercent:       job.ScalePercent,
		ColorMode:          job.ColorMode,
		DuplexMode:         job.DuplexMode,
		MaxRetries:         job.MaxRetries,
		TerminalSessionID:  job.TerminalSessionID,
		TerminalTicketHash: job.TerminalTicketHash,
	}

	// 如果有文件URL，填充（复用或新生成）下载凭证
	if fileAccessToken != "" {
		printJobData.FileAccessToken = fileAccessToken
		printJobData.FileAccessTokenExpiresAt = fileAccessTokenExpiresAt
	}

	// 构造指令消息
	command := Command{
		Type:      CmdTypePrintJob,
		CommandID: job.ID, // 使用job ID作为command ID
		Timestamp: time.Now(),
		Target:    nodeID,
		Data:      printJobData,
	}

	// 获取连接并使用 ACK 机制发送
	m.mutex.RLock()
	conn, exists := m.connections[nodeID]
	m.mutex.RUnlock()

	if !exists {
		return ErrNodeNotConnected
	}

	// 使用 10秒超时等待 ACK
	// 如果超时，意味着 Edge 端虽然在线但未能确认接收任务
	status, err := conn.SendCommandWithAckStatus(&command, 10*time.Second)
	if err != nil {
		return err
	}
	if status != "accepted" {
		return ErrAckRejected
	}
	return nil
}

func (m *ConnectionManager) DispatchNodeEnabledChange(nodeID string, enabled bool) error {
	payload := NodeEnabledPayload{
		Enabled: enabled,
	}

	msg := &Message{
		Type:      CmdTypeNodeState,
		NodeID:    nodeID,
		Timestamp: time.Now(),
		Data:      payload,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return m.SendToNode(nodeID, msgBytes)
}

// DispatchCancelJob 发送取消打印任务通知
func (m *ConnectionManager) DispatchCancelJob(nodeID string, jobID string) error {
	logger.Info("Sending cancel job command to node", zap.String("node_id", nodeID), zap.String("job_id", jobID))

	cmd := Command{
		Type:      "cancel_job",
		CommandID: jobID,
		Timestamp: time.Now(),
		Target:    nodeID,
		Data: map[string]string{
			"job_id": jobID,
			"reason": "cancelled_by_user",
		},
	}

	msgBytes, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	return m.SendToNode(nodeID, msgBytes)
}

// GetActiveConnectionCount 获取当前活跃连接数
func (m *ConnectionManager) GetActiveConnectionCount() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return len(m.connections)
}

// GetConnectedNodeIDs 获取所有已连接的节点ID
func (m *ConnectionManager) GetConnectedNodeIDs() []string {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	nodeIDs := make([]string, 0, len(m.connections))
	for nodeID := range m.connections {
		nodeIDs = append(nodeIDs, nodeID)
	}
	return nodeIDs
}
