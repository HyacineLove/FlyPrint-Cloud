package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"fly-print-cloud/api/internal/auth"
	"fly-print-cloud/api/internal/config"
	"fly-print-cloud/api/internal/database"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
)

// OAuth2Handler OAuth2 认证处理器
type OAuth2Handler struct {
	mode         string // "builtin" | "keycloak"
	builtinAuth  *auth.BuiltinAuthService
	cookieSecure bool

	// Keycloak 模式字段
	config                 *oauth2.Config
	userInfoURL            string
	adminConsoleURL        string
	logoutURL              string
	logoutRedirectURIParam string
	userRepo               *database.UserRepository
}

const (
	oauthStateCookieName = "flyprint_oauth_state"
	oauthStateTTL        = 10 * time.Minute
)

// NewOAuth2Handler 创建 OAuth2 处理器
func NewOAuth2Handler(oauth2Cfg *config.OAuth2Config, adminCfg *config.AdminConfig, userRepo *database.UserRepository, builtinAuth *auth.BuiltinAuthService, cookieSecure bool) *OAuth2Handler {
	handler := &OAuth2Handler{
		mode:            oauth2Cfg.Mode,
		builtinAuth:     builtinAuth,
		cookieSecure:    cookieSecure,
		adminConsoleURL: adminCfg.ConsoleURL,
		userRepo:        userRepo,
	}

	// builtin 模式下不需要外部 OAuth2 配置
	if oauth2Cfg.IsBuiltinMode() {
		return handler
	}

	// Keycloak 模式：配置外部 OAuth2
	if oauth2Cfg.ClientID == "" || oauth2Cfg.AuthURL == "" || oauth2Cfg.TokenURL == "" {
		handler.config = nil
		return handler
	}

	handler.config = &oauth2.Config{
		ClientID:     oauth2Cfg.ClientID,
		ClientSecret: oauth2Cfg.ClientSecret,
		RedirectURL:  oauth2Cfg.RedirectURI,
		Endpoint: oauth2.Endpoint{
			AuthURL:  oauth2Cfg.AuthURL,
			TokenURL: oauth2Cfg.TokenURL,
		},
		Scopes: []string{"openid", "profile", "email", "admin:users", "admin:edge-nodes", "admin:printers", "admin:print-jobs"},
	}
	handler.userInfoURL = oauth2Cfg.UserInfoURL
	handler.logoutURL = oauth2Cfg.LogoutURL
	handler.logoutRedirectURIParam = oauth2Cfg.LogoutRedirectURIParam

	return handler
}

// Token 处理 POST /auth/token（OAuth2 Token 端点）
func (h *OAuth2Handler) Token(c *gin.Context) {
	if h.mode != "builtin" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "builtin token endpoint not available in keycloak mode",
		})
		return
	}

	if err := c.Request.ParseForm(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "invalid form data",
		})
		return
	}

	grantType := c.Request.FormValue("grant_type")
	clientID := c.Request.FormValue("client_id")
	clientSecret := c.Request.FormValue("client_secret")
	// OAuth2 keeps the standard username form field; in builtin mode its value
	// is the account email and is never treated as a separate username.
	email := c.Request.FormValue("username")
	password := c.Request.FormValue("password")
	scope := c.Request.FormValue("scope")

	resp, err := h.builtinAuth.HandleTokenRequest(grantType, clientID, clientSecret, email, password, scope)
	if err != nil {
		errMsg := err.Error()
		// 根据错误类型返回不同的 HTTP 状态码
		statusCode := http.StatusBadRequest
		errorType := "invalid_request"
		if strings.HasPrefix(errMsg, "invalid_client") {
			statusCode = http.StatusUnauthorized
			errorType = "invalid_client"
		} else if strings.HasPrefix(errMsg, "invalid_grant") {
			statusCode = http.StatusUnauthorized
			errorType = "invalid_grant"
		} else if strings.HasPrefix(errMsg, "server_error") {
			statusCode = http.StatusInternalServerError
			errorType = "server_error"
		}

		c.JSON(statusCode, gin.H{
			"error":             errorType,
			"error_description": errMsg,
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// UserInfo 处理 GET /auth/userinfo
func (h *OAuth2Handler) UserInfo(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "missing bearer token",
		})
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	if h.mode == "builtin" {
		// 内置模式：直接解析 JWT
		info, err := h.builtinAuth.HandleUserInfo(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.JSON(http.StatusOK, info)
		return
	}

	// Keycloak 模式：代理到外部 UserInfo 端点
	userInfo, err := h.fetchOAuth2UserInfo(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	c.JSON(http.StatusOK, userInfo)
}

// Login 发起 OAuth2 授权
func (h *OAuth2Handler) Login(c *gin.Context) {
	if h.mode == "builtin" {
		// 内置模式：重定向到 Admin Console 登录页（前端处理密码登录）
		c.Redirect(http.StatusFound, h.adminConsoleURL+"/login")
		return
	}

	// Keycloak 模式
	if h.config == nil {
		BadRequestResponse(c, "OAuth2 配置未设置")
		return
	}

	state := generateRandomState()
	h.setCookie(c, oauthStateCookieName, state, int(oauthStateTTL.Seconds()), "/auth/callback")
	authURL := h.config.AuthCodeURL(state, oauth2.AccessTypeOffline)
	c.Redirect(http.StatusFound, authURL)
}

// Callback 处理 OAuth2 回调
func (h *OAuth2Handler) Callback(c *gin.Context) {
	if h.mode == "builtin" {
		// 内置模式不使用 Authorization Code Flow callback
		BadRequestResponse(c, "builtin mode does not use callback")
		return
	}

	// Keycloak 模式
	if h.config == nil {
		BadRequestResponse(c, "OAuth2 配置未设置")
		return
	}
	if !h.validateAndClearState(c) {
		return
	}

	if errorCode := c.Query("error"); errorCode != "" {
		errorDesc := c.Query("error_description")
		BadRequestResponse(c, "OAuth2 授权失败: "+errorCode+" - "+errorDesc)
		return
	}

	code := c.Query("code")
	if code == "" {
		BadRequestResponse(c, "缺少授权码")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token, err := h.config.Exchange(ctx, code)
	if err != nil {
		InternalErrorResponse(c, "Token 交换失败")
		return
	}

	h.setCookie(c, "access_token", token.AccessToken, int(time.Until(token.Expiry).Seconds()), "/")

	if token.RefreshToken != "" {
		h.setCookie(c, "refresh_token", token.RefreshToken, 7*24*3600, "/")
	}

	if idToken := token.Extra("id_token"); idToken != nil {
		if idTokenStr, ok := idToken.(string); ok {
			h.setCookie(c, "id_token", idTokenStr, int(time.Until(token.Expiry).Seconds()), "/")
		}
	}

	if h.userRepo != nil {
		h.syncUserOnLogin(token.AccessToken)
	}

	c.Redirect(http.StatusFound, h.adminConsoleURL)
}

// OAuth2UserInfo OAuth2 用户信息结构
type OAuth2UserInfo struct {
	Sub               string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// syncUserOnLogin 登录时同步用户信息到本地数据库
func (h *OAuth2Handler) syncUserOnLogin(accessToken string) {
	userInfo, err := h.fetchOAuth2UserInfo(accessToken)
	if err != nil {
		fmt.Printf("用户同步失败: %v\n", err)
		return
	}

	_, err = h.userRepo.GetUserByExternalID(userInfo.Sub)
	if err == nil {
		return
	}

	_, err = h.userRepo.CreateUserFromOAuth2(
		userInfo.Sub,
		userInfo.PreferredUsername,
		userInfo.Email,
	)
	if err != nil {
		fmt.Printf("创建用户失败: %v\n", err)
	}
}

// Me 获取当前用户认证信息
func (h *OAuth2Handler) Me(c *gin.Context) {
	accessToken, err := c.Cookie("access_token")
	if err != nil {
		UnauthorizedResponse(c, "未登录")
		return
	}

	if h.mode == "builtin" {
		// 内置模式：直接解析 JWT 获取用户信息
		info, err := h.builtinAuth.HandleUserInfo(accessToken)
		if err != nil {
			UnauthorizedResponse(c, "Token 无效")
			return
		}
		SuccessResponse(c, gin.H{
			"external_id":   info.Sub,
			"username":      info.PreferredUsername,
			"email":         info.Email,
			"name":          info.Name,
			"roles":         info.RealmAccess.Roles,
			"access_token":  accessToken,
			"authenticated": true,
		})
		return
	}

	// Keycloak 模式
	oauth2UserInfo, err := h.fetchOAuth2UserInfo(accessToken)
	if err != nil {
		UnauthorizedResponse(c, "Token 无效")
		return
	}

	SuccessResponse(c, gin.H{
		"external_id":   oauth2UserInfo.Sub,
		"username":      oauth2UserInfo.PreferredUsername,
		"email":         oauth2UserInfo.Email,
		"name":          oauth2UserInfo.Name,
		"roles":         oauth2UserInfo.RealmAccess.Roles,
		"access_token":  accessToken,
		"authenticated": true,
	})
}

// Mode 返回当前 OAuth2 模式（公开端点，供前端判断）
func (h *OAuth2Handler) Mode(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"mode": h.mode,
	})
}

// fetchOAuth2UserInfo 从 OAuth2 服务器获取用户信息
func (h *OAuth2Handler) fetchOAuth2UserInfo(accessToken string) (*OAuth2UserInfo, error) {
	if h.userInfoURL == "" {
		return nil, fmt.Errorf("userinfo URL not configured")
	}

	req, err := http.NewRequest("GET", h.userInfoURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo request failed: %d", resp.StatusCode)
	}

	var userInfo OAuth2UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, err
	}

	return &userInfo, nil
}

// Verify 验证认证状态 (用于 Nginx auth_request)
func (h *OAuth2Handler) Verify(c *gin.Context) {
	accessToken, err := c.Cookie("access_token")
	if err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}

	if h.mode == "builtin" {
		// 内置模式：直接解析 JWT 验证
		_, err := h.builtinAuth.HandleUserInfo(accessToken)
		if err != nil {
			c.Status(http.StatusUnauthorized)
			return
		}
		c.Status(http.StatusOK)
		return
	}

	// Keycloak 模式
	_, err = h.fetchOAuth2UserInfo(accessToken)
	if err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	c.Status(http.StatusOK)
}

// Logout 登出
func (h *OAuth2Handler) Logout(c *gin.Context) {
	idToken, _ := c.Cookie("id_token")

	// 清除所有认证相关的 cookies
	h.clearCookie(c, "access_token", "/")
	h.clearCookie(c, "refresh_token", "/")
	h.clearCookie(c, "id_token", "/")

	if h.mode == "builtin" || h.logoutURL == "" {
		// 内置模式或没有配置登出 URL：只做本地登出
		SuccessResponse(c, gin.H{"message": "登出成功"})
		return
	}

	// Keycloak 模式：重定向到 OAuth2 提供商登出页面
	redirectURI := url.QueryEscape(h.adminConsoleURL)
	fullLogoutURL := fmt.Sprintf("%s?%s=%s", h.logoutURL, h.logoutRedirectURIParam, redirectURI)

	if idToken != "" {
		idTokenEncoded := url.QueryEscape(idToken)
		fullLogoutURL += fmt.Sprintf("&id_token_hint=%s", idTokenEncoded)
	}

	c.Redirect(http.StatusFound, fullLogoutURL)
}

// generateRandomState 生成随机 state 参数
func generateRandomState() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func (h *OAuth2Handler) validateAndClearState(c *gin.Context) bool {
	expected, err := c.Cookie(oauthStateCookieName)
	h.clearCookie(c, oauthStateCookieName, "/auth/callback")
	actual := c.Query("state")
	if err != nil || expected == "" || actual == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		BadRequestResponse(c, "OAuth2 state 无效或已过期")
		return false
	}
	return true
}

func (h *OAuth2Handler) setCookie(c *gin.Context, name, value string, maxAge int, path string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, value, maxAge, path, "", h.cookieSecure, true)
}

func (h *OAuth2Handler) clearCookie(c *gin.Context, name, path string) {
	h.setCookie(c, name, "", -1, path)
}
