package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"fly-print-cloud/api/internal/models"
)

// EdgeNodeRepository Edge Node 数据访问层
type EdgeNodeRepository struct {
	db *DB
}

// NewEdgeNodeRepository 创建 Edge Node 数据访问层
func NewEdgeNodeRepository(db *DB) *EdgeNodeRepository {
	return &EdgeNodeRepository{db: db}
}

// UpdateAlias updates only the Cloud-owned display alias. Edge-reported facts
// remain outside this method so a local report can never overwrite an operator alias.
func (r *EdgeNodeRepository) UpdateAlias(id, alias string) error {
	result, err := r.db.Exec(`UPDATE edge_nodes SET alias=NULLIF($2, '') WHERE id=$1 AND deleted_at IS NULL`, id, alias)
	if err != nil {
		return fmt.Errorf("update edge node alias: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("edge node not found")
	}
	return nil
}

// UpdateEnabled changes only the Cloud-owned availability switch.
func (r *EdgeNodeRepository) UpdateEnabled(id string, enabled bool) error {
	result, err := r.db.Exec(`UPDATE edge_nodes SET enabled=$2, registration_state=CASE WHEN $2 AND registration_state='disabled' THEN 'active' WHEN NOT $2 THEN 'disabled' ELSE registration_state END WHERE id=$1 AND deleted_at IS NULL`, id, enabled)
	if err != nil {
		return fmt.Errorf("update edge node enabled: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("edge node not found")
	}
	return nil
}

// CreateEdgeNode 创建 Edge Node
func (r *EdgeNodeRepository) CreateEdgeNode(node *models.EdgeNode) error {
	query := `
		INSERT INTO edge_nodes (
			id, name, status, enabled, version, last_heartbeat,
			location, latitude, longitude,
			ip_address, mac_address, network_interface,
			os_version, cpu_info, memory_info, disk_info,
			connection_quality, latency
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			$10, $11, $12,
			$13, $14, $15, $16,
			$17, $18
		)`

	_, err := r.db.Exec(query,
		node.ID, node.Name, node.ConnectionStatus, node.Enabled, node.Version, node.LastHeartbeat,
		node.Location, node.Latitude, node.Longitude,
		node.IPAddress, node.MACAddress, node.NetworkInterface,
		node.OSVersion, node.CPUInfo, node.MemoryInfo, node.DiskInfo,
		node.ConnectionQuality, node.Latency,
	)

	if err != nil {
		return fmt.Errorf("failed to create edge node: %w", err)
	}

	return nil
}

// UpsertEdgeNode 创建或更新 Edge Node（用于注册）
func (r *EdgeNodeRepository) UpsertEdgeNode(node *models.EdgeNode) error {
	query := `
		INSERT INTO edge_nodes (
			id, name, status, version, last_heartbeat,
			location, latitude, longitude,
			ip_address, mac_address, network_interface,
			os_version, cpu_info, memory_info, disk_info,
			connection_quality, latency
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11,
			$12, $13, $14, $15,
			$16, $17
		)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			status = EXCLUDED.status,
			version = EXCLUDED.version,
			last_heartbeat = EXCLUDED.last_heartbeat,
			location = EXCLUDED.location,
			latitude = EXCLUDED.latitude,
			longitude = EXCLUDED.longitude,
			ip_address = EXCLUDED.ip_address,
			mac_address = EXCLUDED.mac_address,
			network_interface = EXCLUDED.network_interface,
			os_version = EXCLUDED.os_version,
			cpu_info = EXCLUDED.cpu_info,
			memory_info = EXCLUDED.memory_info,
			disk_info = EXCLUDED.disk_info,
			connection_quality = EXCLUDED.connection_quality,
			latency = EXCLUDED.latency,
			deleted_at = NULL`

	_, err := r.db.Exec(query,
		node.ID, node.Name, node.ConnectionStatus, node.Version, node.LastHeartbeat,
		node.Location, node.Latitude, node.Longitude,
		node.IPAddress, node.MACAddress, node.NetworkInterface,
		node.OSVersion, node.CPUInfo, node.MemoryInfo, node.DiskInfo,
		node.ConnectionQuality, node.Latency,
	)

	if err != nil {
		return fmt.Errorf("failed to upsert edge node: %w", err)
	}

	return nil
}

// GetEdgeNodeByID 根据ID获取 Edge Node
func (r *EdgeNodeRepository) GetEdgeNodeByID(id string) (*models.EdgeNode, error) {
	node := &models.EdgeNode{}
	query := `
		SELECT id, name, COALESCE(alias,''), registration_state, status, enabled, version, last_heartbeat,
			   location, latitude, longitude,
			   ip_address, mac_address, network_interface,
			   os_version, cpu_info, memory_info, disk_info,
			   connection_quality, latency,
			   created_at, updated_at, deleted_at
		FROM edge_nodes WHERE id = $1 AND deleted_at IS NULL`

	var lastHeartbeat sql.NullTime
	var latitude, longitude sql.NullFloat64
	var location, ipAddress, macAddress, networkInterface sql.NullString
	var osVersion, cpuInfo, memoryInfo, diskInfo sql.NullString
	var connectionQuality sql.NullString
	var latency sql.NullInt32
	var version sql.NullString
	var deletedAt sql.NullTime

	err := r.db.QueryRow(query, id).Scan(
		&node.ID, &node.Name, &node.Alias, &node.RegistrationState, &node.ConnectionStatus, &node.Enabled, &version, &lastHeartbeat,
		&location, &latitude, &longitude,
		&ipAddress, &macAddress, &networkInterface,
		&osVersion, &cpuInfo, &memoryInfo, &diskInfo,
		&connectionQuality, &latency,
		&node.CreatedAt, &node.UpdatedAt, &deletedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("edge node not found")
		}
		return nil, fmt.Errorf("failed to get edge node: %w", err)
	}

	// 处理可为空的字段
	if version.Valid {
		node.Version = version.String
	}
	if lastHeartbeat.Valid {
		node.LastHeartbeat = lastHeartbeat.Time
	}
	if location.Valid {
		node.Location = location.String
	}
	if latitude.Valid {
		lat := latitude.Float64
		node.Latitude = &lat
	}
	if longitude.Valid {
		lng := longitude.Float64
		node.Longitude = &lng
	}
	if ipAddress.Valid {
		ip := ipAddress.String
		node.IPAddress = &ip
	}
	if macAddress.Valid {
		node.MACAddress = macAddress.String
	}
	if networkInterface.Valid {
		node.NetworkInterface = networkInterface.String
	}
	if osVersion.Valid {
		node.OSVersion = osVersion.String
	}
	if cpuInfo.Valid {
		node.CPUInfo = cpuInfo.String
	}
	if memoryInfo.Valid {
		node.MemoryInfo = memoryInfo.String
	}
	if diskInfo.Valid {
		node.DiskInfo = diskInfo.String
	}
	if connectionQuality.Valid {
		node.ConnectionQuality = connectionQuality.String
	}
	if latency.Valid {
		node.Latency = int(latency.Int32)
	}
	if deletedAt.Valid {
		node.DeletedAt = &deletedAt.Time
	}

	if err := r.loadRuntimeStatus(node); err != nil {
		return nil, err
	}
	return node, nil
}

// UpdateEdgeNode 更新 Edge Node
func (r *EdgeNodeRepository) UpdateEdgeNode(node *models.EdgeNode) error {
	query := `
		UPDATE edge_nodes SET
			name = $2, status = $3, enabled = $4, version = $5, last_heartbeat = $6,
			location = $7, latitude = $8, longitude = $9,
			ip_address = $10, mac_address = $11, network_interface = $12,
			os_version = $13, cpu_info = $14, memory_info = $15, disk_info = $16,
			connection_quality = $17, latency = $18
		WHERE id = $1`

	_, err := r.db.Exec(query,
		node.ID, node.Name, node.ConnectionStatus, node.Enabled, node.Version, node.LastHeartbeat,
		node.Location, node.Latitude, node.Longitude,
		node.IPAddress, node.MACAddress, node.NetworkInterface,
		node.OSVersion, node.CPUInfo, node.MemoryInfo, node.DiskInfo,
		node.ConnectionQuality, node.Latency,
	)

	if err != nil {
		return fmt.Errorf("failed to update edge node: %w", err)
	}

	return nil
}

// DeleteEdgeNode 删除 Edge Node（软删除）
func (r *EdgeNodeRepository) DeleteEdgeNode(id string) error {
	query := `UPDATE edge_nodes SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1 AND deleted_at IS NULL`

	_, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete edge node: %w", err)
	}

	return nil
}

// DeleteEdgeNodeTx 删除 Edge Node（软删除，使用事务）
func (r *EdgeNodeRepository) DeleteEdgeNodeTx(tx *Tx, id string) error {
	// A deleted node must not leave a usable device credential behind. Node
	// deletion is the explicit identity-reset operation; disable/enable keeps it.
	if _, err := tx.Exec(`DELETE FROM oauth2_clients WHERE edge_node_id = $1`, id); err != nil {
		return fmt.Errorf("failed to revoke edge node client in transaction: %w", err)
	}

	query := `UPDATE edge_nodes SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1 AND deleted_at IS NULL`

	_, err := tx.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete edge node in transaction: %w", err)
	}

	return nil
}

// HardDeleteEdgeNode 硬删除 Edge Node（彻底删除）
func (r *EdgeNodeRepository) HardDeleteEdgeNode(id string) error {
	query := `DELETE FROM edge_nodes WHERE id = $1`

	_, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to hard delete edge node: %w", err)
	}

	return nil
}

// ListEdgeNodes 获取 Edge Node 列表
// 支持按状态过滤、排序和搜索
func (r *EdgeNodeRepository) ListEdgeNodes(offset, limit int, status, sortBy, sortOrder, search string) ([]*models.EdgeNode, int, error) {
	var nodes []*models.EdgeNode

	// 构建查询条件
	whereClause := "WHERE deleted_at IS NULL"
	args := []interface{}{}
	argIndex := 1

	if status != "" {
		whereClause += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, status)
		argIndex++
	}

	// 添加搜索条件（按节点名称模糊搜索）
	if search != "" {
		whereClause += fmt.Sprintf(" AND name ILIKE $%d", argIndex)
		args = append(args, "%"+search+"%")
		argIndex++
	}

	// 构建排序子句
	var orderClause string
	switch sortBy {
	case "last_heartbeat":
		orderClause = "last_heartbeat"
	case "printer_count":
		// 使用子查询计算打印机数量进行排序
		orderClause = "(SELECT COUNT(*) FROM printers WHERE edge_node_id = edge_nodes.id AND deleted_at IS NULL)"
	case "name":
		orderClause = "name"
	default:
		orderClause = "created_at"
	}

	// 排序方向
	if strings.ToLower(sortOrder) == "asc" {
		orderClause += " ASC"
	} else {
		orderClause += " DESC"
	}

	// 查询总数
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM edge_nodes %s", whereClause)
	var total int
	err := r.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count edge nodes: %w", err)
	}

	// 查询数据
	query := fmt.Sprintf(`
		SELECT id, name, COALESCE(alias,''), registration_state, status, enabled, version, last_heartbeat,
			   location, latitude, longitude,
			   ip_address, mac_address, network_interface,
			   os_version, cpu_info, memory_info, disk_info,
			   connection_quality, latency,
			   created_at, updated_at, deleted_at
		FROM edge_nodes %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`, whereClause, orderClause, argIndex, argIndex+1)

	args = append(args, limit, offset)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query edge nodes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		node := &models.EdgeNode{}
		var lastHeartbeat sql.NullTime
		var latitude, longitude sql.NullFloat64
		var location, ipAddress, macAddress, networkInterface sql.NullString
		var osVersion, cpuInfo, memoryInfo, diskInfo sql.NullString
		var connectionQuality sql.NullString
		var latency sql.NullInt32
		var version sql.NullString

		var deletedAt sql.NullTime
		err := rows.Scan(
			&node.ID, &node.Name, &node.Alias, &node.RegistrationState, &node.ConnectionStatus, &node.Enabled, &version, &lastHeartbeat,
			&location, &latitude, &longitude,
			&ipAddress, &macAddress, &networkInterface,
			&osVersion, &cpuInfo, &memoryInfo, &diskInfo,
			&connectionQuality, &latency,
			&node.CreatedAt, &node.UpdatedAt, &deletedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan edge node: %w", err)
		}

		// 处理可为空的字段
		if version.Valid {
			node.Version = version.String
		}
		if lastHeartbeat.Valid {
			node.LastHeartbeat = lastHeartbeat.Time
		}
		if location.Valid {
			node.Location = location.String
		}
		if latitude.Valid {
			lat := latitude.Float64
			node.Latitude = &lat
		}
		if longitude.Valid {
			lng := longitude.Float64
			node.Longitude = &lng
		}
		if ipAddress.Valid {
			ip := ipAddress.String
			node.IPAddress = &ip
		}
		if macAddress.Valid {
			node.MACAddress = macAddress.String
		}
		if networkInterface.Valid {
			node.NetworkInterface = networkInterface.String
		}
		if osVersion.Valid {
			node.OSVersion = osVersion.String
		}
		if cpuInfo.Valid {
			node.CPUInfo = cpuInfo.String
		}
		if memoryInfo.Valid {
			node.MemoryInfo = memoryInfo.String
		}
		if diskInfo.Valid {
			node.DiskInfo = diskInfo.String
		}
		if connectionQuality.Valid {
			node.ConnectionQuality = connectionQuality.String
		}
		if latency.Valid {
			node.Latency = int(latency.Int32)
		}
		if deletedAt.Valid {
			node.DeletedAt = &deletedAt.Time
		}

		if err := r.loadRuntimeStatus(node); err != nil {
			return nil, 0, err
		}
		nodes = append(nodes, node)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows error: %w", err)
	}

	return nodes, total, nil
}

func (r *EdgeNodeRepository) loadRuntimeStatus(node *models.EdgeNode) error {
	var reason, message sql.NullString
	var printerStatusAt sql.NullTime
	var components []byte
	err := r.db.QueryRow(`SELECT health_status,health_reason_code,health_message,
		cpu_usage,memory_usage,disk_usage,components,printer_status_received_at FROM edge_nodes WHERE id=$1`, node.ID).Scan(
		&node.HealthStatus, &reason, &message, &node.CPUUsage, &node.MemoryUsage, &node.DiskUsage, &components, &printerStatusAt)
	if err != nil {
		return fmt.Errorf("failed to load edge runtime status: %w", err)
	}
	if reason.Valid {
		node.HealthReasonCode = reason.String
	}
	if message.Valid {
		node.HealthMessage = message.String
	}
	if printerStatusAt.Valid {
		node.PrinterStatusReceivedAt = &printerStatusAt.Time
	}
	if len(components) > 0 {
		_ = json.Unmarshal(components, &node.Components)
	}
	return nil
}

// UpdateHeartbeat 更新心跳时间
func (r *EdgeNodeRepository) UpdateHeartbeat(id string) error {
	// 简化：每次心跳都更新时间（因为不再存储status字段）
	query := `UPDATE edge_nodes SET last_heartbeat = CURRENT_TIMESTAMP WHERE id = $1`

	_, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}

	return nil
}

// UpdateStatus 更新状态
func (r *EdgeNodeRepository) UpdateStatus(id, status string) error {
	query := `UPDATE edge_nodes SET status = $2 WHERE id = $1`

	_, err := r.db.Exec(query, id, status)
	if err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	return nil
}

// CheckAndUpdateOfflineNodes 检查并更新超时的节点状态为离线
func (r *EdgeNodeRepository) CheckAndUpdateOfflineNodes(timeoutMinutes int) (int, error) {
	query := `
		UPDATE edge_nodes 
		SET status = 'offline' 
		WHERE status = 'online' 
		  AND last_heartbeat < CURRENT_TIMESTAMP - INTERVAL '%d minutes'
		  AND deleted_at IS NULL`

	result, err := r.db.Exec(fmt.Sprintf(query, timeoutMinutes))
	if err != nil {
		return 0, fmt.Errorf("failed to update offline nodes: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return int(rowsAffected), nil
}
