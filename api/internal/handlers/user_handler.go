package handlers

import (
	"errors"
	"net/http"
	"strings"

	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/security"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// UserHandler 用户管理处理器
type UserHandler struct {
	userRepo *database.UserRepository
}

// NewUserHandler 创建用户管理处理器
func NewUserHandler(userRepo *database.UserRepository) *UserHandler {
	return &UserHandler{
		userRepo: userRepo,
	}
}

// CreateUserRequest 创建用户请求
type CreateUserRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Username string `json:"username" binding:"required,min=3,max=50"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role" binding:"required,oneof=admin operator viewer"`
}

// UpdateUserRequest 更新用户请求
type UpdateUserRequest struct {
	Email    *string `json:"email"`
	Username string  `json:"username" binding:"required,min=3,max=50"`
	Role     string  `json:"role" binding:"required,oneof=admin operator viewer"`
}

type UpdateEnabledRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

// GetCurrentUserProfile 获取当前用户业务信息
// @Summary 获取当前用户信息
// @Description 获取当前登录用户的详细信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "用户信息"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 404 {object} map[string]interface{} "用户不存在"
// @Router /api/v1/users/profile [get]
func (h *UserHandler) GetCurrentUserProfile(c *gin.Context) {
	// 从认证中间件获取 external_id
	externalID, exists := c.Get("external_id")
	if !exists {
		UnauthorizedResponse(c, "未认证")
		return
	}

	// 从本地数据库获取用户信息
	user, err := h.userRepo.GetUserByExternalID(externalID.(string))
	if err != nil {
		NotFoundResponse(c, "用户不存在")
		return
	}

	SuccessResponse(c, user)
}

// ListUsers 获取用户列表
func (h *UserHandler) ListUsers(c *gin.Context) {
	page, pageSize, offset := ParsePaginationParams(c)
	filter := database.UserListFilter{
		Search:    c.Query("search"),
		Role:      c.Query("role"),
		Status:    c.Query("status"),
		SortBy:    c.Query("sort_by"),
		SortOrder: c.Query("sort_order"),
		Offset:    offset,
		Limit:     pageSize,
	}

	users, total, err := h.userRepo.ListUsers(filter)
	if err != nil {
		logger.Error("Failed to list users", zap.Error(err))
		InternalErrorResponse(c, "获取用户列表失败")
		return
	}

	// 直接返回用户列表（敏感字段已通过 json:"-" 过滤）
	PaginatedSuccessResponse(c, users, total, page, pageSize)
}

// CreateUser 创建用户
func (h *UserHandler) CreateUser(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequestResponse(c, "请求参数无效")
		return
	}

	// 检查邮箱是否已存在
	req.Email = security.NormalizeEmail(req.Email)
	exists, err := h.userRepo.EmailExists(req.Email)
	if err != nil {
		logger.Error("Failed to check email existence", zap.Error(err))
		InternalErrorResponse(c, "检查邮箱失败")
		return
	}
	if exists {
		BadRequestResponse(c, "邮箱已存在")
		return
	}
	usernameExists, err := h.userRepo.UsernameExists(req.Username)
	if err != nil {
		logger.Error("Failed to check username existence", zap.Error(err))
		InternalErrorResponse(c, "检查用户名失败")
		return
	}
	if usernameExists {
		BadRequestResponse(c, "用户名已存在")
		return
	}

	// 创建用户
	user := &models.User{
		ID:           uuid.New().String(),
		Username:     strings.TrimSpace(req.Username),
		Email:        req.Email,
		PasswordHash: req.Password, // 在repository中会被加密
		Role:         req.Role,
		Status:       "active",
	}

	if err := h.userRepo.CreateUser(user); err != nil {
		logger.Error("Failed to create user", zap.Error(err))
		InternalErrorResponse(c, "创建用户失败")
		return
	}

	logger.Info("User created successfully by admin", zap.String("email", user.Email))
	// 直接返回用户信息（敏感字段已过滤）
	CreatedResponse(c, user)
}

// GetUser 获取用户详情
func (h *UserHandler) GetUser(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		BadRequestResponse(c, "用户ID不能为空")
		return
	}

	user, err := h.userRepo.GetUserByIDAnyStatus(userID)
	if err != nil {
		NotFoundResponse(c, "用户不存在")
		return
	}

	// 直接返回用户信息（敏感字段已过滤）
	SuccessResponse(c, user)
}

// UpdateUser 更新用户
func (h *UserHandler) UpdateUser(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		BadRequestResponse(c, "用户ID不能为空")
		return
	}

	var req UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequestResponse(c, "请求参数无效")
		return
	}
	if req.Email != nil {
		BadRequestResponse(c, "邮箱不可修改")
		return
	}

	// 检查用户是否存在
	user, err := h.userRepo.GetUserByIDAnyStatus(userID)
	if err != nil {
		NotFoundResponse(c, "用户不存在")
		return
	}

	// 检查用户名是否已被其他用户使用
	username := strings.TrimSpace(req.Username)
	exists, err := h.userRepo.UsernameExists(username, userID)
	if err != nil {
		logger.Error("Failed to check username existence", zap.Error(err))
		InternalErrorResponse(c, "检查用户名失败")
		return
	}
	if exists {
		BadRequestResponse(c, "用户名已被其他用户使用")
		return
	}

	// 更新用户信息
	user.Username = username
	user.Role = req.Role

	if err := h.userRepo.UpdateUser(user); err != nil {
		logger.Error("Failed to update user", zap.String("user_id", userID), zap.Error(err))
		InternalErrorResponse(c, "更新用户失败")
		return
	}

	logger.Info("User updated successfully", zap.String("email", user.Email))
	SuccessResponse(c, user)
}

// UpdateEnabled 更新用户启用状态；停用不删除任务和文件。
func (h *UserHandler) UpdateEnabled(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		BadRequestResponse(c, "用户ID不能为空")
		return
	}

	var req UpdateEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequestResponse(c, "请求参数无效")
		return
	}

	user, err := h.userRepo.UpdateEnabled(userID, *req.Enabled)
	if err != nil {
		if strings.Contains(err.Error(), "user not found") {
			NotFoundWithCode(c, ErrCodeUserNotFound)
			return
		}
		logger.Error("Failed to update user status", zap.String("user_id", userID), zap.Error(err))
		InternalErrorWithCode(c, ErrCodeUserUpdateFailed)
		return
	}
	SuccessResponse(c, user)
}

// DeleteUser 删除用户
func (h *UserHandler) DeleteUser(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		BadRequestResponse(c, "用户ID不能为空")
		return
	}

	// builtin JWT 的 sub 由认证中间件写入 external_id。
	currentUserID, exists := c.Get("external_id")
	if !exists {
		UnauthorizedResponse(c, "未找到当前用户信息")
		return
	}
	currentUserIDString, ok := currentUserID.(string)
	if !ok || currentUserIDString == "" {
		UnauthorizedResponse(c, "未找到当前用户信息")
		return
	}

	// 不能删除自己
	if userID == currentUserIDString {
		BadRequestResponse(c, "不能删除自己")
		return
	}

	if err := h.userRepo.DeleteUserWithPrintJobs(userID); err != nil {
		if errors.Is(err, database.ErrUserHasActivePrintJobs) {
			ErrorResponseWithCode(c, http.StatusConflict, ErrCodeUserHasActivePrintJobs, GetErrorMessage(ErrCodeUserHasActivePrintJobs))
			return
		}
		if strings.Contains(err.Error(), "user not found") {
			NotFoundWithCode(c, ErrCodeUserNotFound)
			return
		}
		logger.Error("Failed to delete user", zap.String("user_id", userID), zap.Error(err))
		InternalErrorWithCode(c, ErrCodeUserDeleteFailed)
		return
	}

	logger.Info("User deleted successfully", zap.String("user_id", userID))
	SuccessResponse(c, gin.H{"message": "用户删除成功"})
}

// ChangePassword 修改用户密码
func (h *UserHandler) ChangePassword(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		BadRequestResponse(c, "用户ID不能为空")
		return
	}

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequestResponse(c, "请求参数无效")
		return
	}
	if err := security.ValidatePasswordStrength(req.NewPassword); err != nil {
		BadRequestResponse(c, "密码强度不足")
		return
	}

	// 检查用户是否存在
	_, err := h.userRepo.GetUserByID(userID)
	if err != nil {
		NotFoundResponse(c, "用户不存在")
		return
	}

	// 更新密码
	if err := h.userRepo.UpdatePassword(userID, req.NewPassword); err != nil {
		logger.Error("Failed to change password for user", zap.String("user_id", userID), zap.Error(err))
		InternalErrorResponse(c, "修改密码失败")
		return
	}

	logger.Info("Password changed successfully for user", zap.String("user_id", userID))
	SuccessResponse(c, gin.H{"message": "密码修改成功"})
}
