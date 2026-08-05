package handlers

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fly-print-cloud/api/internal/business"
	"fly-print-cloud/api/internal/config"
	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/operations"
	"fly-print-cloud/api/internal/security"
	"fly-print-cloud/api/internal/storage"
	"fly-print-cloud/api/internal/utils"
	"fly-print-cloud/api/internal/websocket"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	defaultUploadRuleMaxSizeBytes int64 = 10 * 1024 * 1024
	uploadRuleMaxPages                  = 5
)

type FileHandler struct {
	repo                   fileRepository
	config                 *config.StorageConfig
	storage                storage.Service
	wsManager              *websocket.ConnectionManager
	tokenManager           *security.TokenManager
	settingsProvider       businessSettingsProvider
	edgeNodeRepo           edgeNodeLookup
	printerRepo            printerLookup
	terminalUploadSessions terminalUploadSessionBinder
	terminalSessions       terminalSessionMatcher
}

type businessSettingsProvider interface {
	Current() (business.Settings, error)
}

type fileRepository interface {
	Create(file *models.File) error
	GetByID(id string) (*models.File, error)
}

type edgeNodeLookup interface {
	GetEdgeNodeByID(id string) (*models.EdgeNode, error)
}

type printerLookup interface {
	GetPrinterByID(id string) (*models.Printer, error)
}

type terminalUploadSessionBinder interface {
	BindFile(rawToken, fileID string, now time.Time) error
	GetByToken(rawToken string, now time.Time) (*database.TerminalUploadSessionInfo, error)
}

type terminalSessionMatcher interface {
	Matches(nodeID, sessionID, ticketHash string) (bool, error)
}

func NewFileHandler(repo fileRepository, cfg *config.StorageConfig, storageService storage.Service, wsManager *websocket.ConnectionManager, tokenManager *security.TokenManager, settingsProvider businessSettingsProvider, edgeNodeRepo edgeNodeLookup, printerRepo printerLookup) *FileHandler {
	return &FileHandler{
		repo:             repo,
		config:           cfg,
		storage:          storageService,
		wsManager:        wsManager,
		tokenManager:     tokenManager,
		settingsProvider: settingsProvider,
		edgeNodeRepo:     edgeNodeRepo,
		printerRepo:      printerRepo,
	}
}

// SetTerminalUploadSessionBinder enables the Cloud entry bridge without
// changing the existing upload HTTP protocol.
func (h *FileHandler) SetTerminalUploadSessionBinder(binder terminalUploadSessionBinder) {
	h.terminalUploadSessions = binder
}

func (h *FileHandler) SetTerminalSessionMatcher(matcher terminalSessionMatcher) {
	h.terminalSessions = matcher
}

// Upload 上传文件
func (h *FileHandler) Upload(c *gin.Context) {
	// 检查是否使用上传凭证
	token := c.Query("token")
	var uploaderID string
	var nodeID string

	if token != "" {
		// 第一阶段：使用轻量验证（不消耗Token），提前检查Token有效性
		payload, err := h.tokenManager.VerifyUploadTokenAvailable(token, c.Query("node_id"), c.Query("printer_id"))
		if err != nil {
			logger.Warn("Upload token verification failed", zap.Error(err))
			errorCode := security.GetTokenErrorCode(err)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   errorCode,
				"message": err.Error(),
			})
			return
		}
		// 暂存信息，稍后验证通过再使用
		if err := h.ensureUploadTargetActive(payload.NodeID, payload.PrinterID); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   uploadTargetErrorCode(err),
				"message": err.Error(),
			})
			return
		}
		if err := h.ensureTerminalUploadSessionActive(token); err != nil {
			c.JSON(http.StatusConflict, gin.H{
				"code":    409,
				"error":   "terminal_session_invalid",
				"message": "终端会话已失效，请返回终端重新扫码。",
			})
			return
		}
		nodeID = payload.NodeID
		if userID, authenticated := authenticatedUserID(c); authenticated {
			uploaderID = userID
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   "official_user_auth_required",
				"message": "Official account authentication is required",
			})
			return
		}
		logger.Debug("File upload pre-authorized for node", zap.String("node_id", payload.NodeID), zap.String("printer_id", payload.PrinterID))
	} else {
		// 使用 OAuth2 验证（可选认证模式下由中间件处理）
		if val, exists := c.Get("external_id"); exists {
			uploaderID = val.(string)
		} else if val, exists := c.Get("sub"); exists {
			uploaderID = val.(string)
		} else {
			// 没有凭证也没有 OAuth2 认证
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   "unauthorized",
				"message": "Upload token or OAuth2 authentication required",
			})
			return
		}
		// 从查询参数获取 node_id（可选）
		nodeID = c.Query("node_id")
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}

	// Open the file to check magic bytes
	srcFile, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer srcFile.Close()

	if err := h.validateUploadRules(fileHeader, srcFile); err != nil {
		if err == errUploadInvalidType {
			BadRequestWithCode(c, ErrCodeFileInvalidType)
			return
		}
		if err == errUploadTooLarge {
			BadRequestWithCode(c, ErrCodeFileTooLarge)
			return
		}
		if err == errUploadTooManyPages {
			BadRequestWithCode(c, ErrCodeFileTooManyPages)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate file"})
		return
	}

	// Sanitize filename
	safeFilename := security.SanitizeFilename(fileHeader.Filename)

	// Generate safe filename with extension
	ext := filepath.Ext(safeFilename)
	fileUUID := uuid.New().String()
	fileName := fileUUID + ext
	objectKey := fileName

	uploadFile, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reopen uploaded file"})
		return
	}
	defer uploadFile.Close()

	hasher := sha256.New()
	uploadReader := io.TeeReader(uploadFile, hasher)
	if _, err := h.storage.Put(c.Request.Context(), objectKey, uploadReader, storage.PutOptions{
		ContentType: fileHeader.Header.Get("Content-Type"),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}
	contentHash := fmt.Sprintf("%x", hasher.Sum(nil))

	// 第二阶段：所有验证通过后，真正验证并消耗Token
	if token != "" {
		payload, err := h.tokenManager.ValidateUploadToken(token)
		if err != nil {
			_ = h.storage.Delete(c.Request.Context(), objectKey)
			logger.Warn("Upload token validation failed", zap.Error(err))
			errorCode := security.GetTokenErrorCode(err)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   errorCode,
				"message": err.Error(),
			})
			return
		}
		logger.Debug("File upload fully authorized via token", zap.String("node_id", payload.NodeID), zap.String("printer_id", payload.PrinterID))
	}

	// Create DB record
	file := &models.File{
		OriginalName:    fileHeader.Filename,
		FileName:        fileName,
		FilePath:        objectKey,
		StorageProvider: h.config.Provider,
		StorageBucket:   h.config.MinIO.Bucket,
		ObjectKey:       objectKey,
		ContentHash:     contentHash,
		MimeType:        fileHeader.Header.Get("Content-Type"),
		Size:            fileHeader.Size,
		UploaderID:      uploaderID,
	}

	if err := h.repo.Create(file); err != nil {
		_ = h.storage.Delete(c.Request.Context(), objectKey)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file metadata"})
		return
	}
	if token != "" && h.terminalUploadSessions != nil {
		if err := h.terminalUploadSessions.BindFile(token, file.ID, time.Now()); err != nil {
			_ = h.storage.Delete(c.Request.Context(), objectKey)
			c.JSON(http.StatusConflict, gin.H{"error": "terminal_upload_session_unavailable", "message": "终端会话已失效，请返回终端重新扫码。"})
			return
		}
	}

	// Generate URL (relative path for API)
	file.URL = fmt.Sprintf("/api/v1/files/%s", file.ID)

	// Check if node_id is provided for preview
	if nodeID != "" {
		preview := websocket.PreviewFilePayload{
			FileID: file.ID, FileURL: file.URL, FileName: file.OriginalName,
			FileSize: file.Size, FileType: file.MimeType, ContentHash: file.ContentHash,
		}
		if token != "" && h.terminalUploadSessions != nil {
			if info, err := h.terminalUploadSessions.GetByToken(token, time.Now()); err == nil && info != nil {
				preview.TerminalSessionID = info.SessionID
				preview.TerminalTicketHash = info.TicketHash
			}
		}
		if preview.TerminalSessionID != "" {
			if err := h.wsManager.DispatchOfficialPreview(nodeID, preview); err != nil {
				fmt.Printf("Failed to dispatch preview to node %s: %v\n", nodeID, err)
			}
		} else if err := h.wsManager.DispatchPreviewFile(nodeID, file.ID, file.URL, file.OriginalName, file.Size, file.MimeType, file.ContentHash); err != nil {
			fmt.Printf("Failed to dispatch preview to node %s: %v\n", nodeID, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data":    file,
	})
}

var (
	errUploadInvalidType      = fmt.Errorf("invalid file type")
	errUploadTooLarge         = fmt.Errorf("file too large")
	errUploadTooManyPages     = fmt.Errorf("too many pages")
	errNodeNotFound           = errors.New("Edge node not found")
	errNodeDisabled           = errors.New("Edge node has been disabled by administrator")
	errPrinterNotFound        = errors.New("Printer not found")
	errPrinterNotBelongToNode = errors.New("Printer does not belong to this node")
	errPrinterDisabled        = errors.New("Printer has been disabled by administrator")
	errPrinterUnavailable     = errors.New("Printer is not currently available")
)

func (h *FileHandler) validateUploadRules(fileHeader *multipart.FileHeader, srcFile multipart.File) error {
	policy := h.currentUploadPolicy()

	if fileHeader.Size > policy.MaxFileSizeBytes {
		return errUploadTooLarge
	}
	buffer := make([]byte, 512)
	bytesRead, err := srcFile.Read(buffer)
	if err != nil && err != io.EOF {
		return err
	}
	contentType := http.DetectContentType(buffer[:bytesRead])
	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	if !extensionAllowed(ext, policy.AllowedExtensions) {
		return errUploadInvalidType
	}
	if !security.IsAllowedFileType(contentType, security.AllowedPrintFileTypes) {
		documentExtensions := []string{".docx", ".doc", ".pdf"}
		if !extensionAllowed(ext, documentExtensions) {
			return errUploadInvalidType
		}
	}
	if _, err := srcFile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if ext != ".pdf" && ext != ".doc" && ext != ".docx" {
		return nil
	}
	pageCount, err := utils.ValidateDocumentPageCountFromReader(srcFile, srcFile, fileHeader.Size, ext, policy.MaxPages)
	if err != nil {
		logger.Debug("document page validation failed", zap.Error(err), zap.String("file", fileHeader.Filename))
		return errUploadTooManyPages
	}
	logger.Debug("document page validation passed", zap.Int("pages", pageCount), zap.String("file", fileHeader.Filename))
	return nil
}

func extensionAllowed(ext string, allowedExtensions []string) bool {
	ext = strings.ToLower(ext)
	for _, allowedExt := range allowedExtensions {
		if ext == strings.ToLower(allowedExt) {
			return true
		}
	}
	return false
}

type UploadPolicyResponse struct {
	MaxFileSizeBytes  int64    `json:"max_file_size_bytes"`
	MaxPages          int      `json:"max_pages"`
	AllowedExtensions []string `json:"allowed_extensions"`
	AllowedMimeTypes  []string `json:"allowed_mime_types"`
}

func (h *FileHandler) currentUploadPolicy() UploadPolicyResponse {
	maxSize := defaultUploadRuleMaxSizeBytes
	maxPages := uploadRuleMaxPages
	allowedExtensions := append([]string{}, business.DefaultAllowedUploadExtensions...)
	if h != nil && h.config != nil {
		if h.config.MaxSize > 0 {
			maxSize = h.config.MaxSize
		}
		if h.config.MaxDocumentPages > 0 {
			maxPages = h.config.MaxDocumentPages
		}
	}
	if h != nil && h.settingsProvider != nil {
		if settings, err := h.settingsProvider.Current(); err == nil {
			maxSize = settings.UploadMaxSizeBytes
			maxPages = settings.MaxDocumentPages
			allowedExtensions = append([]string{}, settings.AllowedExtensions...)
		}
	}

	return UploadPolicyResponse{
		MaxFileSizeBytes:  maxSize,
		MaxPages:          maxPages,
		AllowedExtensions: allowedExtensions,
		AllowedMimeTypes:  append([]string{}, security.AllowedPrintFileTypes...),
	}
}

func (h *FileHandler) GetUploadPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "success",
		"data":    h.currentUploadPolicy(),
	})
}

// Download 下载文件
func (h *FileHandler) Download(c *gin.Context) {
	id := c.Param("id")

	// 检查是否使用下载凭证
	token := c.Query("token")

	if token != "" {
		// 使用凭证验证
		payload, err := h.tokenManager.ValidateDownloadToken(token, id, "")
		if err != nil {
			logger.Warn("Download token validation failed for file", zap.String("file_id", id), zap.Error(err))
			errorCode := security.GetTokenErrorCode(err)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   errorCode,
				"message": err.Error(),
			})
			return
		}
		logger.Debug("File download authorized via token", zap.String("file_id", payload.FileID), zap.String("job_id", payload.JobID), zap.String("node_id", payload.NodeID))
	} else {
		// 使用 OAuth2 验证
		// 检查是否有认证信息
		_, hasAuth := c.Get("external_id")
		if !hasAuth {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   "unauthorized",
				"message": "Download token or OAuth2 authentication required",
			})
			return
		}

		// 获取文件信息进行权限检查
		file, err := h.repo.GetByID(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}
		if file == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
			return
		}

		// Permission check for OAuth2 users
		roles, _ := c.Get("roles")
		rolesSlice, _ := roles.([]string)

		hasAdmin := false
		for _, r := range rolesSlice {
			if r == "admin" || r == "fly-print-admin" {
				hasAdmin = true
			}
		}

		currentUser, _ := c.Get("external_id")
		isOwner := currentUser == file.UploaderID

		// file:read is only a capability to use the file API. It does not grant
		// access to every object in storage; a caller must own the file, be an
		// administrator, or present a download ticket bound to this file.
		if !hasAdmin && !isOwner {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
			return
		}

		h.serveStoredFile(c, file)
		return
	}

	// 使用凭证验证时，直接获取并返回文件
	file, err := h.repo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	if file == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	h.serveStoredFile(c, file)
}

// VerifyUploadToken 轻量验证上传Token（不消耗一次性Token）
// GET /api/v1/files/verify-upload-token?token=xxx
func (h *FileHandler) VerifyUploadToken(c *gin.Context) {
	token := c.Query("token")
	nodeID := c.Query("node_id")
	printerID := c.Query("printer_id")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"error":   "missing_token",
			"message": "Upload token is required",
		})
		return
	}

	// 使用轻量验证方法（不标记为已使用）
	payload, err := h.tokenManager.VerifyUploadTokenAvailable(token, nodeID, printerID)
	if err != nil {
		logger.Warn("Upload token verification failed", zap.Error(err))
		errorCode := security.GetTokenErrorCode(err)
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    401,
			"error":   errorCode,
			"message": err.Error(),
			"valid":   false,
		})
		return
	}

	// 返回验证结果和Token信息
	if err := h.ensureUploadTargetActive(payload.NodeID, payload.PrinterID); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    401,
			"error":   uploadTargetErrorCode(err),
			"message": err.Error(),
			"valid":   false,
		})
		return
	}
	if err := h.ensureTerminalUploadSessionActive(token); err != nil {
		c.JSON(http.StatusConflict, gin.H{
			"code":    409,
			"error":   "terminal_session_invalid",
			"message": "终端会话已失效，请返回终端重新扫码。",
			"valid":   false,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "Upload session is valid",
		"valid":   true,
		"data": gin.H{
			"node_id":    payload.NodeID,
			"printer_id": payload.PrinterID,
			"expires_at": payload.ExpiresAt,
		},
	})
}

func (h *FileHandler) PreflightUpload(c *gin.Context) {
	token := c.Query("token")
	if token != "" {
		payload, err := h.tokenManager.VerifyUploadTokenAvailable(token, c.Query("node_id"), c.Query("printer_id"))
		if err != nil {
			logger.Warn("Upload token preflight verification failed", zap.Error(err))
			errorCode := security.GetTokenErrorCode(err)
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   errorCode,
				"message": err.Error(),
				"valid":   false,
			})
			return
		}
		if err := h.ensureUploadTargetActive(payload.NodeID, payload.PrinterID); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   uploadTargetErrorCode(err),
				"message": err.Error(),
				"valid":   false,
			})
			return
		}
		if err := h.ensureTerminalUploadSessionActive(token); err != nil {
			c.JSON(http.StatusConflict, gin.H{
				"code":    409,
				"error":   "terminal_session_invalid",
				"message": "终端会话已失效，请返回终端重新扫码。",
				"valid":   false,
			})
			return
		}
		if _, authenticated := authenticatedUserID(c); !authenticated {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"error":   "official_user_auth_required",
				"message": "Official account authentication is required",
				"valid":   false,
			})
			return
		}
	} else {
		if _, exists := c.Get("external_id"); !exists {
			if _, exists := c.Get("sub"); !exists {
				c.JSON(http.StatusUnauthorized, gin.H{
					"code":    401,
					"error":   "unauthorized",
					"message": "Upload token or OAuth2 authentication required",
					"valid":   false,
				})
				return
			}
		}
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    ErrCodeBadRequest,
			"message": "No file uploaded",
			"valid":   false,
		})
		return
	}
	srcFile, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    ErrCodeInternalServerError,
			"message": "Failed to open uploaded file",
			"valid":   false,
		})
		return
	}
	defer srcFile.Close()
	if err := h.validateUploadRules(fileHeader, srcFile); err != nil {
		if err == errUploadInvalidType {
			BadRequestWithCode(c, ErrCodeFileInvalidType)
			return
		}
		if err == errUploadTooLarge {
			BadRequestWithCode(c, ErrCodeFileTooLarge)
			return
		}
		if err == errUploadTooManyPages {
			BadRequestWithCode(c, ErrCodeFileTooManyPages)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    ErrCodeInternalServerError,
			"message": "Failed to validate file",
			"valid":   false,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "Preflight validation passed",
		"valid":   true,
	})
}

func (h *FileHandler) ensureUploadTargetActive(nodeID, printerID string) error {
	if h.edgeNodeRepo == nil || h.printerRepo == nil {
		return nil
	}

	node, err := h.edgeNodeRepo.GetEdgeNodeByID(nodeID)
	if err != nil || node == nil {
		return errNodeNotFound
	}
	if !node.Enabled {
		return errNodeDisabled
	}

	printer, err := h.printerRepo.GetPrinterByID(printerID)
	if err != nil || printer == nil {
		return errPrinterNotFound
	}
	if printer.EdgeNodeID != nodeID {
		return errPrinterNotBelongToNode
	}
	if !printer.Enabled {
		return errPrinterDisabled
	}
	if reason := operations.ValidatePrinterDispatch(printer, node, time.Now()); reason != "" {
		return errPrinterUnavailable
	}

	return nil
}

func (h *FileHandler) ensureTerminalUploadSessionActive(rawToken string) error {
	if rawToken == "" {
		return nil
	}
	if h.terminalUploadSessions == nil || h.terminalSessions == nil {
		return errTerminalSessionInvalid
	}
	info, err := h.terminalUploadSessions.GetByToken(rawToken, time.Now())
	if err != nil {
		return err
	}
	if info == nil {
		return errTerminalSessionInvalid
	}
	ok, err := h.terminalSessions.Matches(info.NodeID, info.SessionID, info.TicketHash)
	if err != nil {
		return err
	}
	if !ok {
		return errTerminalSessionInvalid
	}
	return nil
}

var errTerminalSessionInvalid = errors.New("terminal session invalid")

func authenticatedUserID(c *gin.Context) (string, bool) {
	for _, key := range []string{"external_id", "sub"} {
		value, exists := c.Get(key)
		if !exists {
			continue
		}
		userID, ok := value.(string)
		if ok && userID != "" {
			return userID, true
		}
	}
	return "", false
}

func uploadTargetErrorCode(err error) string {
	switch err {
	case errNodeNotFound:
		return "node_not_found"
	case errNodeDisabled:
		return "node_disabled"
	case errPrinterNotFound:
		return "printer_not_found"
	case errPrinterNotBelongToNode:
		return "printer_not_belong_to_node"
	case errPrinterDisabled:
		return "printer_disabled"
	case errPrinterUnavailable:
		return "printer_unavailable"
	default:
		return "unknown_error"
	}
}

func (h *FileHandler) serveStoredFile(c *gin.Context, file *models.File) {
	storageKey := file.ObjectKey
	if storageKey == "" {
		storageKey = file.FilePath
	}

	reader, _, err := h.storage.Get(c.Request.Context(), storageKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		return
	}
	defer reader.Close()

	extraHeaders := map[string]string{
		"Content-Disposition": fmt.Sprintf("attachment; filename=%q", file.OriginalName),
	}

	c.DataFromReader(http.StatusOK, file.Size, file.MimeType, reader, extraHeaders)
	if file.Size > 0 {
		c.Header("Content-Length", strconv.FormatInt(file.Size, 10))
	}
}
