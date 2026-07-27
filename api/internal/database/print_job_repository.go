package database

import (
	"database/sql"
	"fmt"
	"time"

	"fly-print-cloud/api/internal/models"

	"github.com/google/uuid"
)

type PrintJobRepository struct {
	db *DB
}

func NewPrintJobRepository(db *DB) *PrintJobRepository {
	return &PrintJobRepository{db: db}
}

// CreatePrintJob 创建打印任务
func (r *PrintJobRepository) CreatePrintJob(job *models.PrintJob) error {
	query := `
		INSERT INTO print_jobs (
			id, name, status, printer_id, 
			user_id, user_name, file_path, file_url, content_hash, file_size, page_count, 
			copies, paper_size, color_mode, duplex_mode, 
			start_time, end_time, error_message, retry_count, 
			max_retries, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 
			$12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
		)`

	now := time.Now()
	job.ID = uuid.New().String()
	job.CreatedAt = now
	job.UpdatedAt = now

	// 将user_id转换为sql.NullString以支持可空值
	var userID sql.NullString
	if job.UserID != "" {
		userID = sql.NullString{String: job.UserID, Valid: true}
	}

	_, err := r.db.DB.Exec(query,
		job.ID, job.Name, job.Status, job.PrinterID,
		userID, job.UserName, job.FilePath, job.FileURL, job.ContentHash, job.FileSize, job.PageCount,
		job.Copies, job.PaperSize, job.ColorMode, job.DuplexMode,
		job.StartTime, job.EndTime, job.ErrorMessage, job.RetryCount,
		job.MaxRetries, job.CreatedAt, job.UpdatedAt,
	)

	return err
}

// GetPrintJobByID 根据ID获取打印任务
func (r *PrintJobRepository) GetPrintJobByID(id string) (*models.PrintJob, error) {
	query := `
		SELECT pj.id, pj.name, pj.status, pj.printer_id,
			   pj.user_id, COALESCE(NULLIF(u.username, ''), pj.user_name, ''), COALESCE(u.email, ''), file_path, file_url, content_hash, file_size, page_count,
			   copies, paper_size, color_mode, duplex_mode, 
			   start_time, end_time, COALESCE(error_message, ''), retry_count,
			   max_retries, created_at, updated_at
		FROM print_jobs pj LEFT JOIN users u ON u.id::text = pj.user_id WHERE pj.id = $1`

	job := &models.PrintJob{}
	var userID sql.NullString
	err := r.db.DB.QueryRow(query, id).Scan(
		&job.ID, &job.Name, &job.Status, &job.PrinterID,
		&userID, &job.UserName, &job.UserEmail, &job.FilePath, &job.FileURL, &job.ContentHash, &job.FileSize, &job.PageCount,
		&job.Copies, &job.PaperSize, &job.ColorMode, &job.DuplexMode,
		&job.StartTime, &job.EndTime, &job.ErrorMessage, &job.RetryCount,
		&job.MaxRetries, &job.CreatedAt, &job.UpdatedAt,
	)

	// 有值就设置，没值就空着
	if userID.Valid {
		job.UserID = userID.String
	}

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := r.loadErrorCode(job); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *PrintJobRepository) loadErrorCode(job *models.PrintJob) error {
	var code sql.NullString
	if err := r.db.QueryRow(`SELECT error_code FROM print_jobs WHERE id=$1`, job.ID).Scan(&code); err != nil {
		return err
	}
	if code.Valid {
		job.ErrorCode = code.String
	}
	return nil
}

// ListPrintJobs 获取打印任务列表
func (r *PrintJobRepository) ListPrintJobs(limit, offset int, status, printerID, userID, userEmail, edgeNodeID, initiatorCode string, startTime, endTime *time.Time) ([]*models.PrintJob, error) {
	query := `
		SELECT pj.id, pj.name, pj.status, pj.printer_id,
			   pj.user_id, COALESCE(NULLIF(u.username, ''), pj.user_name, ''), COALESCE(u.email, ''), pj.file_path, pj.file_url, pj.content_hash, pj.file_size, pj.page_count,
			   pj.copies, pj.paper_size, pj.color_mode, pj.duplex_mode, 
			   pj.start_time, pj.end_time, COALESCE(pj.error_message, ''), pj.error_code, pj.retry_count,
			   pj.max_retries, pj.created_at, pj.updated_at,
			   COALESCE(NULLIF(p.display_name, ''), p.name, '') as printer_name,
			   COALESCE(NULLIF(n.alias, ''), n.name, '') as node_name,
			   COALESCE(p.edge_node_id, '') as edge_node_id,
			   COALESCE(provider.display_name, '主系统') AS initiator_name,
			   COALESCE(provider.code, '') AS initiator_code
		FROM print_jobs pj
		LEFT JOIN users u ON u.id::text = pj.user_id
		LEFT JOIN printers p ON pj.printer_id = p.id
		LEFT JOIN edge_nodes n ON p.edge_node_id = n.id
		LEFT JOIN integration_print_requests integration_request ON integration_request.print_job_id = pj.id
		LEFT JOIN integration_providers provider ON provider.code = integration_request.provider_code
		WHERE 1=1`

	args := []interface{}{}
	argIndex := 1

	if status != "" {
		query += fmt.Sprintf(" AND pj.status = $%d", argIndex)
		args = append(args, status)
		argIndex++
	}

	if printerID != "" {
		query += fmt.Sprintf(" AND pj.printer_id = $%d", argIndex)
		args = append(args, printerID)
		argIndex++
	}

	if userID != "" {
		query += fmt.Sprintf(" AND pj.user_id = $%d", argIndex)
		args = append(args, userID)
		argIndex++
	}

	if userEmail != "" {
		query += fmt.Sprintf(" AND LOWER(u.email) = LOWER($%d)", argIndex)
		args = append(args, userEmail)
		argIndex++
	}

	if edgeNodeID != "" {
		query += fmt.Sprintf(" AND p.edge_node_id = $%d", argIndex)
		args = append(args, edgeNodeID)
		argIndex++
	}

	if initiatorCode == "official" {
		query += ` AND integration_request.id IS NULL`
	} else if initiatorCode != "" {
		query += fmt.Sprintf(" AND provider.code = $%d", argIndex)
		args = append(args, initiatorCode)
		argIndex++
	}

	if startTime != nil {
		query += fmt.Sprintf(" AND pj.created_at >= $%d", argIndex)
		args = append(args, *startTime)
		argIndex++
	}

	if endTime != nil {
		query += fmt.Sprintf(" AND pj.created_at < $%d", argIndex)
		args = append(args, *endTime)
		argIndex++
	}

	query += " ORDER BY pj.created_at DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, limit)
		argIndex++
	}

	if offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, offset)
	}

	rows, err := r.db.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*models.PrintJob
	for rows.Next() {
		job := &models.PrintJob{}
		var userID sql.NullString
		var printerName sql.NullString
		var nodeName sql.NullString
		var edgeNodeID sql.NullString
		var errorCode sql.NullString
		var initiatorCode sql.NullString
		err := rows.Scan(
			&job.ID, &job.Name, &job.Status, &job.PrinterID,
			&userID, &job.UserName, &job.UserEmail, &job.FilePath, &job.FileURL, &job.ContentHash, &job.FileSize, &job.PageCount,
			&job.Copies, &job.PaperSize, &job.ColorMode, &job.DuplexMode,
			&job.StartTime, &job.EndTime, &job.ErrorMessage, &errorCode, &job.RetryCount,
			&job.MaxRetries, &job.CreatedAt, &job.UpdatedAt,
			&printerName, &nodeName, &edgeNodeID, &job.InitiatorName, &initiatorCode,
		)
		if err != nil {
			return nil, err
		}

		// 有值就设置，没值就空着
		if userID.Valid {
			job.UserID = userID.String
		}
		if printerName.Valid {
			job.PrinterName = printerName.String
		}
		if nodeName.Valid {
			job.NodeName = nodeName.String
		}
		if edgeNodeID.Valid {
			job.EdgeNodeID = edgeNodeID.String
		}
		if errorCode.Valid {
			job.ErrorCode = errorCode.String
		}
		if initiatorCode.Valid {
			job.InitiatorCode = initiatorCode.String
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// UpdatePrintJob 更新打印任务
func (r *PrintJobRepository) UpdatePrintJob(job *models.PrintJob) error {
	query := `
		UPDATE print_jobs SET 
			name = $2, status = $3, file_path = $4,
			content_hash = $5, file_size = $6, page_count = $7, copies = $8, paper_size = $9,
			color_mode = $10, duplex_mode = $11, start_time = $12,
			end_time = $13, error_message = $14, retry_count = $15,
			max_retries = $16, updated_at = $17
		WHERE id = $1`

	job.UpdatedAt = time.Now()

	_, err := r.db.DB.Exec(query,
		job.ID, job.Name, job.Status, job.FilePath, job.ContentHash,
		job.FileSize, job.PageCount, job.Copies, job.PaperSize,
		job.ColorMode, job.DuplexMode, job.StartTime,
		job.EndTime, job.ErrorMessage, job.RetryCount,
		job.MaxRetries, job.UpdatedAt,
	)

	return err
}

// MarkDispatched records an acknowledged dispatch without overwriting a status
// update that may already have arrived from Edge.
func (r *PrintJobRepository) MarkDispatched(jobID string) error {
	_, err := r.db.Exec(`UPDATE print_jobs SET status='dispatched',error_code=NULL,error_message=NULL,
		updated_at=CURRENT_TIMESTAMP WHERE id=$1::uuid AND status='pending'`, jobID)
	return err
}

// DeletePrintJob 删除打印任务
func (r *PrintJobRepository) DeletePrintJob(id string) error {
	query := `DELETE FROM print_jobs WHERE id = $1`
	_, err := r.db.DB.Exec(query, id)
	return err
}

// GetPrintJobsByPrinterID 根据打印机ID获取任务列表
func (r *PrintJobRepository) GetPrintJobsByPrinterID(printerID string, limit, offset int) ([]*models.PrintJob, error) {
	return r.ListPrintJobs(limit, offset, "", printerID, "", "", "", "", nil, nil)
}

// GetPrintJobsByUserID 根据用户ID获取任务列表
func (r *PrintJobRepository) GetPrintJobsByUserID(userID string, limit, offset int) ([]*models.PrintJob, error) {
	return r.ListPrintJobs(limit, offset, "", "", userID, "", "", "", nil, nil)
}

// GetPendingOrDispatchedJobsByEdgeNodeID 获取指定节点下所有待处理或已分发但未完成的任务
func (r *PrintJobRepository) GetPendingOrDispatchedJobsByEdgeNodeID(edgeNodeID string) ([]*models.PrintJob, error) {
	query := `
		SELECT pj.id, pj.name, pj.status, pj.printer_id, p.name,
			   pj.user_id, pj.user_name, pj.file_path, pj.file_url, pj.content_hash, pj.file_size, pj.page_count, 
			   pj.copies, pj.paper_size, pj.color_mode, pj.duplex_mode, 
			   pj.start_time, pj.end_time, COALESCE(pj.error_message, ''), pj.retry_count,
			   pj.max_retries, pj.created_at, pj.updated_at
		FROM print_jobs pj
		JOIN printers p ON pj.printer_id = p.id
		WHERE p.edge_node_id = $1 
		AND pj.status IN ('pending', 'dispatched')
		ORDER BY pj.created_at ASC`

	rows, err := r.db.DB.Query(query, edgeNodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*models.PrintJob
	for rows.Next() {
		job := &models.PrintJob{}
		var userID sql.NullString
		err := rows.Scan(
			&job.ID, &job.Name, &job.Status, &job.PrinterID, &job.PrinterName,
			&userID, &job.UserName, &job.FilePath, &job.FileURL, &job.ContentHash, &job.FileSize, &job.PageCount,
			&job.Copies, &job.PaperSize, &job.ColorMode, &job.DuplexMode,
			&job.StartTime, &job.EndTime, &job.ErrorMessage, &job.RetryCount,
			&job.MaxRetries, &job.CreatedAt, &job.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		if userID.Valid {
			job.UserID = userID.String
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// UpdateJobStatus 更新打印任务状态和进度
func (r *PrintJobRepository) UpdateJobStatus(jobID, status string, progress int, errorMessage string) error {
	query := `
		UPDATE print_jobs SET 
			status = $2, 
			error_message = $3,
			updated_at = $4,
			start_time = CASE WHEN $5 = 'processing' THEN $4 ELSE start_time END,
			end_time = CASE WHEN $6 IN ('completed', 'failed', 'canceled', 'unconfirmed') THEN $4 ELSE end_time END
		WHERE id = $1`

	now := time.Now()
	_, err := r.db.DB.Exec(query, jobID, status, errorMessage, now, status, status)
	return err
}

// CountPrintJobs 统计打印任务总数
func (r *PrintJobRepository) CountPrintJobs(status, printerID, userID, edgeNodeID string, startTime, endTime *time.Time) (int, error) {
	return r.CountPrintJobsFiltered(status, printerID, userID, edgeNodeID, "", "", startTime, endTime)
}

// CountPrintJobsFiltered counts jobs with optional initiator/provider filter.
// initiatorCode "" = no filter; "official" = no integration request; otherwise provider code.
func (r *PrintJobRepository) CountPrintJobsFiltered(status, printerID, userID, edgeNodeID, initiatorCode, userEmail string, startTime, endTime *time.Time) (int, error) {
	query := `SELECT COUNT(*) FROM print_jobs pj`
	needsPrinter := edgeNodeID != ""
	needsIntegration := initiatorCode != ""
	if userEmail != "" {
		query += ` LEFT JOIN users u ON u.id::text = pj.user_id`
	}
	if needsPrinter {
		query += ` LEFT JOIN printers p ON pj.printer_id = p.id`
	}
	if needsIntegration {
		query += ` LEFT JOIN integration_print_requests integration_request ON integration_request.print_job_id = pj.id`
		query += ` LEFT JOIN integration_providers provider ON provider.code = integration_request.provider_code`
	}
	query += ` WHERE 1=1`
	args := []interface{}{}
	argIndex := 1

	if status != "" {
		query += fmt.Sprintf(" AND pj.status = $%d", argIndex)
		args = append(args, status)
		argIndex++
	}
	if printerID != "" {
		query += fmt.Sprintf(" AND pj.printer_id = $%d", argIndex)
		args = append(args, printerID)
		argIndex++
	}
	if userID != "" {
		query += fmt.Sprintf(" AND pj.user_id = $%d", argIndex)
		args = append(args, userID)
		argIndex++
	}
	if userEmail != "" {
		query += fmt.Sprintf(" AND LOWER(u.email) = LOWER($%d)", argIndex)
		args = append(args, userEmail)
		argIndex++
	}
	if edgeNodeID != "" {
		query += fmt.Sprintf(" AND p.edge_node_id = $%d", argIndex)
		args = append(args, edgeNodeID)
		argIndex++
	}
	if initiatorCode == "official" {
		query += ` AND integration_request.id IS NULL`
	} else if initiatorCode != "" {
		query += fmt.Sprintf(" AND provider.code = $%d", argIndex)
		args = append(args, initiatorCode)
		argIndex++
	}
	if startTime != nil {
		query += fmt.Sprintf(" AND pj.created_at >= $%d", argIndex)
		args = append(args, *startTime)
		argIndex++
	}
	if endTime != nil {
		query += fmt.Sprintf(" AND pj.created_at < $%d", argIndex)
		args = append(args, *endTime)
	}

	var total int
	err := r.db.DB.QueryRow(query, args...).Scan(&total)
	return total, err
}

// CountJobsByStatusAndDate 根据状态和日期范围统计打印任务数量
func (r *PrintJobRepository) CountJobsByStatusAndDate(status string, startDate, endDate time.Time) (int, error) {
	query := `SELECT COUNT(*) FROM print_jobs WHERE status = $1 AND created_at >= $2 AND created_at < $3`

	var count int
	err := r.db.DB.QueryRow(query, status, startDate, endDate).Scan(&count)
	return count, err
}

// TrendBuckets returns one database-aggregated row for every requested time
// bucket. The period is deliberately closed to the three Admin choices so no
// SQL date expression is ever assembled from request input.
type TrendBucket struct {
	Bucket    time.Time `json:"bucket"`
	Label     string    `json:"label"`
	Completed int       `json:"completed"`
	Failed    int       `json:"failed"`
}

func (r *PrintJobRepository) TrendBuckets(period string, now time.Time) ([]TrendBucket, error) {
	var start, end time.Time
	var step, label string
	productLocation := time.FixedZone("GMT+8", 8*60*60)
	localNow := now.In(productLocation)
	switch period {
	case "day":
		localStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, productLocation)
		start, end, step, label = localStart.UTC(), localStart.AddDate(0, 0, 1).UTC(), "hour", "HH24:00"
	case "month":
		localStart := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, productLocation)
		start, end, step, label = localStart.UTC(), localStart.AddDate(0, 1, 0).UTC(), "day", "MM-DD"
	case "year":
		localStart := time.Date(localNow.Year(), time.January, 1, 0, 0, 0, 0, productLocation)
		start, end, step, label = localStart.UTC(), localStart.AddDate(1, 0, 0).UTC(), "month", "YYYY-MM"
	default:
		return nil, fmt.Errorf("unsupported trend period: %s", period)
	}
	query := fmt.Sprintf(`
		WITH buckets AS (
			SELECT generate_series($1::timestamp, $2::timestamp - interval '1 %s', interval '1 %s') AS bucket
		)
		SELECT b.bucket, to_char(b.bucket + interval '8 hours', $3),
			COUNT(pj.id) FILTER (WHERE pj.status='completed')::int,
			COUNT(pj.id) FILTER (WHERE pj.status IN ('failed','cancelled','unconfirmed'))::int
		FROM buckets b LEFT JOIN print_jobs pj ON pj.created_at >= b.bucket
			AND pj.created_at < b.bucket + interval '1 %s'
		GROUP BY b.bucket ORDER BY b.bucket`, step, step, step)
	rows, err := r.db.Query(query, start, end, label)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TrendBucket, 0)
	for rows.Next() {
		var item TrendBucket
		if err := rows.Scan(&item.Bucket, &item.Label, &item.Completed, &item.Failed); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CountActiveJobsByPrinter 统计指定打印机的活动任务数（pending, dispatched, processing 状态）
func (r *PrintJobRepository) CountActiveJobsByPrinter(printerID string) (int, error) {
	query := `
		SELECT COUNT(*) 
		FROM print_jobs 
		WHERE printer_id = $1 AND status IN ('pending', 'dispatched', 'processing')`

	var count int
	err := r.db.DB.QueryRow(query, printerID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count active jobs by printer: %w", err)
	}
	return count, nil
}

// CleanupStaleJobs 标记长时间未更新的“打印中”任务为失败
func (r *PrintJobRepository) CleanupStaleJobs(timeout time.Duration) (int64, error) {
	query := `
		UPDATE print_jobs 
		SET status = 'failed', 
			error_message = 'Job timed out - Edge node did not report status',
			end_time = $1,
			updated_at = $1
		WHERE status IN ('pending', 'dispatched')
		AND updated_at < $2
	`

	now := time.Now()
	threshold := now.Add(-timeout)

	result, err := r.db.DB.Exec(query, now, threshold)
	if err != nil {
		return 0, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	return rowsAffected, nil
}

// ListPrintJobsWithTotal 获取打印任务列表并返回总数
func (r *PrintJobRepository) ListPrintJobsWithTotal(limit, offset int, status, printerID, userID, userEmail, edgeNodeID, initiatorCode string, startTime, endTime *time.Time) ([]*models.PrintJob, int, error) {
	// 获取总数
	total, err := r.CountPrintJobsFiltered(status, printerID, userID, edgeNodeID, initiatorCode, userEmail, startTime, endTime)
	if err != nil {
		return nil, 0, err
	}

	// 获取列表
	jobs, err := r.ListPrintJobs(limit, offset, status, printerID, userID, userEmail, edgeNodeID, initiatorCode, startTime, endTime)
	if err != nil {
		return nil, 0, err
	}

	return jobs, total, nil
}

// GetPendingJobsForRetry 获取pending状态且创建时间超过指定时长的任务
// 用于定时任务重试分发
func (r *PrintJobRepository) GetPendingJobsForRetry(minAge time.Duration) ([]*models.PrintJob, error) {
	query := `
		SELECT pj.id, pj.name, pj.status, pj.printer_id, p.name as printer_name, p.edge_node_id,
			   pj.user_id, pj.user_name, pj.file_path, pj.file_url, pj.content_hash, pj.file_size, pj.page_count,
			   pj.copies, pj.paper_size, pj.color_mode, pj.duplex_mode,
			   pj.start_time, pj.end_time, COALESCE(pj.error_message, ''), pj.retry_count,
			   pj.max_retries, pj.created_at, pj.updated_at
		FROM print_jobs pj
		JOIN printers p ON pj.printer_id = p.id
		WHERE pj.status = 'pending'
		AND pj.created_at < $1
		ORDER BY pj.created_at ASC
		LIMIT 100`

	cutoffTime := time.Now().Add(-minAge)
	rows, err := r.db.DB.Query(query, cutoffTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*models.PrintJob
	for rows.Next() {
		job := &models.PrintJob{}
		var userID sql.NullString
		var edgeNodeID string

		err := rows.Scan(
			&job.ID, &job.Name, &job.Status, &job.PrinterID, &job.PrinterName, &edgeNodeID,
			&userID, &job.UserName, &job.FilePath, &job.FileURL, &job.ContentHash, &job.FileSize, &job.PageCount,
			&job.Copies, &job.PaperSize, &job.ColorMode, &job.DuplexMode,
			&job.StartTime, &job.EndTime, &job.ErrorMessage, &job.RetryCount,
			&job.MaxRetries, &job.CreatedAt, &job.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		if userID.Valid {
			job.UserID = userID.String
		}
		job.EdgeNodeID = edgeNodeID

		jobs = append(jobs, job)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return jobs, nil
}
