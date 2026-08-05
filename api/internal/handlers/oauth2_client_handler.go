package handlers

import (
	"fmt"
	"net/http"

	"fly-print-cloud/api/internal/auth"
	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/security"

	"github.com/gin-gonic/gin"
)

// OAuth2ClientHandler OAuth2 客户端管理处理器
type OAuth2ClientHandler struct {
	clientRepo   *database.OAuth2ClientRepository
	secretCipher *security.ClientSecretCipher
}

// NewOAuth2ClientHandler 创建 OAuth2 客户端管理处理器
func NewOAuth2ClientHandler(clientRepo *database.OAuth2ClientRepository, secretCipher *security.ClientSecretCipher) *OAuth2ClientHandler {
	return &OAuth2ClientHandler{clientRepo: clientRepo, secretCipher: secretCipher}
}

// createOAuth2ClientRequest 创建客户端请求
type createOAuth2ClientRequest struct {
	ClientID       string `json:"client_id" binding:"required"`
	ClientType     string `json:"client_type" binding:"required"`
	EdgeNodeID     string `json:"edge_node_id"`
	SitePortalCode string `json:"site_portal_code"`
	AllowedScopes  string `json:"allowed_scopes" binding:"required"`
	Description    string `json:"description"`
}

// updateOAuth2ClientRequest 更新客户端请求
type updateOAuth2ClientRequest struct {
	AllowedScopes string `json:"allowed_scopes"`
	Description   string `json:"description"`
	Enabled       *bool  `json:"enabled"`
}

func validateOAuth2ClientType(clientType string) error {
	if clientType != "edge_node" && clientType != "site_portal" {
		return fmt.Errorf("client_type must be edge_node or site_portal")
	}
	return nil
}

// List 获取客户端列表
func (h *OAuth2ClientHandler) List(c *gin.Context) {
	page, pageSize, offset := ParsePaginationParams(c)
	// OAuth2客户端列表默认使用20条每页
	if c.Query("page_size") == "" {
		pageSize = 20
		offset = (page - 1) * pageSize
	}

	clients, total, err := h.clientRepo.List(offset, pageSize)
	if err != nil {
		InternalErrorResponse(c, "获取客户端列表失败")
		return
	}

	if clients == nil {
		clients = []*models.OAuth2Client{}
	}

	PaginatedSuccessResponse(c, clients, total, page, pageSize)
}

// Create 创建客户端
func (h *OAuth2ClientHandler) Create(c *gin.Context) {
	var req createOAuth2ClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}

	// 检查 client_id 是否已存在
	exists, err := h.clientRepo.ClientIDExists(req.ClientID)
	if err != nil {
		InternalErrorResponse(c, "检查客户端ID失败")
		return
	}
	if exists {
		BadRequestResponse(c, "client_id 已存在")
		return
	}

	// 验证 client_type
	if err := validateOAuth2ClientType(req.ClientType); err != nil {
		BadRequestResponse(c, err.Error())
		return
	}
	if req.ClientType == "edge_node" && req.EdgeNodeID == "" {
		BadRequestResponse(c, "edge_node 客户端必须指定 edge_node_id")
		return
	}
	if req.ClientType != "edge_node" && req.EdgeNodeID != "" {
		BadRequestResponse(c, "仅 edge_node 客户端可以绑定 edge_node_id")
		return
	}
	if req.ClientType == "site_portal" && req.SitePortalCode == "" {
		BadRequestResponse(c, "site_portal 客户端必须绑定 site_portal_code")
		return
	}
	if req.ClientType == "edge_node" && req.SitePortalCode != "" {
		BadRequestResponse(c, "仅 site_portal 客户端可以绑定 site_portal_code")
		return
	}

	// 生成随机密钥
	rawSecret, err := auth.GenerateClientSecret()
	if err != nil {
		InternalErrorResponse(c, "生成客户端密钥失败")
		return
	}

	// 哈希密钥
	secretHash, err := auth.HashClientSecret(rawSecret)
	if err != nil {
		InternalErrorResponse(c, "哈希客户端密钥失败")
		return
	}
	encryptedSecret, err := h.secretCipher.Encrypt(rawSecret)
	if err != nil {
		InternalErrorResponse(c, "加密客户端密钥失败")
		return
	}

	client := &models.OAuth2Client{
		ClientID:              req.ClientID,
		ClientSecretHash:      secretHash,
		ClientSecretEncrypted: encryptedSecret,
		ClientType:            req.ClientType,
		AllowedScopes:         req.AllowedScopes,
		Description:           req.Description,
		Enabled:               true,
	}
	if req.EdgeNodeID != "" {
		client.EdgeNodeID = &req.EdgeNodeID
	}
	if req.SitePortalCode != "" {
		client.SitePortalCode = &req.SitePortalCode
	}

	if err := h.clientRepo.Create(client); err != nil {
		InternalErrorResponse(c, "创建客户端失败")
		return
	}

	CreatedResponse(c, gin.H{
		"id":               client.ID,
		"client_id":        client.ClientID,
		"client_type":      client.ClientType,
		"edge_node_id":     client.EdgeNodeID,
		"site_portal_code": client.SitePortalCode,
		"allowed_scopes":   client.AllowedScopes,
		"description":      client.Description,
		"enabled":          client.Enabled,
		"created_at":       client.CreatedAt,
		"message":          "客户端已创建，可通过复制密钥按钮获取密钥",
	})
}

// Get 获取客户端详情
func (h *OAuth2ClientHandler) Get(c *gin.Context) {
	id := c.Param("id")
	client, err := h.clientRepo.GetByID(id)
	if err != nil {
		NotFoundResponse(c, "客户端不存在")
		return
	}

	SuccessResponse(c, client)
}

// Update 更新客户端信息
func (h *OAuth2ClientHandler) Update(c *gin.Context) {
	id := c.Param("id")
	client, err := h.clientRepo.GetByID(id)
	if err != nil {
		NotFoundResponse(c, "客户端不存在")
		return
	}

	var req updateOAuth2ClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ValidationErrorResponse(c, err)
		return
	}

	if req.AllowedScopes != "" {
		client.AllowedScopes = req.AllowedScopes
	}
	if req.Description != "" {
		client.Description = req.Description
	}
	if req.Enabled != nil {
		client.Enabled = *req.Enabled
	}

	if err := h.clientRepo.Update(client); err != nil {
		InternalErrorResponse(c, "更新客户端失败")
		return
	}

	SuccessResponse(c, client)
}

// ResetSecret 重置客户端密钥
func (h *OAuth2ClientHandler) ResetSecret(c *gin.Context) {
	id := c.Param("id")

	// 确认客户端存在
	_, err := h.clientRepo.GetByID(id)
	if err != nil {
		NotFoundResponse(c, "客户端不存在")
		return
	}

	// 生成新密钥
	rawSecret, err := auth.GenerateClientSecret()
	if err != nil {
		InternalErrorResponse(c, "生成客户端密钥失败")
		return
	}

	secretHash, err := auth.HashClientSecret(rawSecret)
	if err != nil {
		InternalErrorResponse(c, "哈希客户端密钥失败")
		return
	}
	encryptedSecret, err := h.secretCipher.Encrypt(rawSecret)
	if err != nil {
		InternalErrorResponse(c, "加密客户端密钥失败")
		return
	}

	if err := h.clientRepo.UpdateSecret(id, secretHash, encryptedSecret); err != nil {
		InternalErrorResponse(c, "更新客户端密钥失败")
		return
	}

	SuccessResponse(c, gin.H{
		"message": "密钥已重置",
	})
}

func (h *OAuth2ClientHandler) CopySecret(c *gin.Context) {
	client, err := h.clientRepo.GetByID(c.Param("id"))
	if err != nil {
		NotFoundResponse(c, "客户端不存在")
		return
	}
	secret, err := h.secretCipher.Decrypt(client.ClientSecretEncrypted)
	if err != nil {
		InternalErrorResponse(c, "读取客户端密钥失败")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	SuccessResponse(c, gin.H{"client_secret": secret})
}

// Delete 删除客户端
func (h *OAuth2ClientHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.clientRepo.Delete(id); err != nil {
		NotFoundResponse(c, "客户端不存在")
		return
	}

	c.JSON(http.StatusOK, Response{
		Code:    http.StatusOK,
		Message: "删除成功",
	})
}
