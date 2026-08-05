package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"fly-print-cloud/api/internal/config"
	"fly-print-cloud/api/internal/database"
	jwt "github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// TokenResponse OAuth2 token 响应
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

// UserInfoResponse UserInfo 端点响应
type UserInfoResponse struct {
	Sub               string      `json:"sub"`
	PreferredUsername string      `json:"preferred_username"`
	Email             string      `json:"email"`
	Name              string      `json:"name,omitempty"`
	RealmAccess       RealmAccess `json:"realm_access,omitempty"`
}

// RealmAccess Keycloak 兼容的 realm_access 结构
type RealmAccess struct {
	Roles []string `json:"roles"`
}

// BuiltinAuthService 内置 OAuth2 认证服务
type BuiltinAuthService struct {
	clientRepo    *database.OAuth2ClientRepository
	userRepo      *database.UserRepository
	signingSecret []byte
	tokenExpiry   int
	issuer        string
}

// NewBuiltinAuthService 创建内置认证服务
func NewBuiltinAuthService(
	clientRepo *database.OAuth2ClientRepository,
	userRepo *database.UserRepository,
	cfg *config.OAuth2Config,
) *BuiltinAuthService {
	return &BuiltinAuthService{
		clientRepo:    clientRepo,
		userRepo:      userRepo,
		signingSecret: []byte(cfg.JWTSigningSecret),
		tokenExpiry:   cfg.JWTTokenExpiry,
		issuer:        cfg.JWTIssuer,
	}
}

// HandleTokenRequest 处理 /auth/token 请求
func (s *BuiltinAuthService) HandleTokenRequest(grantType, clientID, clientSecret, email, password, scope string) (*TokenResponse, error) {
	switch grantType {
	case "client_credentials":
		return s.handleClientCredentials(clientID, clientSecret, scope)
	case "password":
		return s.handlePasswordGrant(email, password, scope)
	default:
		return nil, fmt.Errorf("unsupported grant_type: %s", grantType)
	}
}

// handleClientCredentials 处理 Client Credentials Flow
func (s *BuiltinAuthService) handleClientCredentials(clientID, clientSecret, requestedScope string) (*TokenResponse, error) {
	// 查询客户端
	client, err := s.clientRepo.GetByClientID(clientID)
	if err != nil {
		return nil, fmt.Errorf("invalid_client: client not found")
	}

	// 检查客户端是否启用
	if !client.Enabled {
		return nil, fmt.Errorf("invalid_client: client is disabled")
	}

	// 验证密钥
	if !s.clientRepo.VerifySecret(client, clientSecret) {
		return nil, fmt.Errorf("invalid_client: invalid client_secret")
	}

	// 计算授权的 scope（请求的与允许的取交集）
	grantedScope := s.intersectScopes(requestedScope, client.AllowedScopes)
	if grantedScope == "" {
		// 如果请求未指定 scope，则使用客户端允许的全部 scope
		grantedScope = client.AllowedScopes
	}

	if client.ClientType == "edge_node" && (client.EdgeNodeID == nil || *client.EdgeNodeID == "") {
		return nil, fmt.Errorf("invalid_client: edge_node client is not bound to a node")
	}
	if client.ClientType == "edge_node" {
		usable, err := s.clientRepo.IsBoundEdgeNodeUsable(client.ClientID)
		if err != nil {
			return nil, fmt.Errorf("server_error: failed to verify edge node state")
		}
		if !usable {
			return nil, fmt.Errorf("invalid_client: edge node is disabled or deleted")
		}
	}
	if client.ClientType == "site_portal" {
		if client.SitePortalCode == nil || *client.SitePortalCode == "" {
			return nil, fmt.Errorf("invalid_client: site_portal client is not bound to a portal")
		}
		usable, err := s.clientRepo.IsBoundSitePortalUsable(client.ClientID)
		if err != nil {
			return nil, fmt.Errorf("server_error: failed to verify site portal state")
		}
		if !usable {
			return nil, fmt.Errorf("invalid_client: site portal is disabled")
		}
	}

	// Device credentials carry the immutable node claim. Human and Site Portal
	// client credentials never acquire a node identity.
	nodeID := ""
	if client.EdgeNodeID != nil {
		nodeID = *client.EdgeNodeID
	}
	portalCode := ""
	if client.SitePortalCode != nil {
		portalCode = *client.SitePortalCode
	}
	tokenString, expiresIn, err := s.GenerateJWTForClient(client.ClientID, client.ClientID, client.ClientID+"@service.local", grantedScope, nodeID, client.ClientType, portalCode)
	if err != nil {
		return nil, fmt.Errorf("server_error: failed to generate token")
	}

	return &TokenResponse{
		AccessToken: tokenString,
		TokenType:   "bearer",
		ExpiresIn:   expiresIn,
		Scope:       grantedScope,
	}, nil
}

// handlePasswordGrant 处理 Password Grant Flow（邮箱登录）
func (s *BuiltinAuthService) handlePasswordGrant(email, password, requestedScope string) (*TokenResponse, error) {
	// 查询用户
	user, err := s.userRepo.GetUserByEmail(email)
	if err != nil {
		return nil, fmt.Errorf("invalid_grant: invalid email or password")
	}

	// 验证密码
	if !s.userRepo.VerifyPassword(user, password) {
		return nil, fmt.Errorf("invalid_grant: invalid email or password")
	}

	// 根据用户角色映射 scope
	allowedScope := mapUserRoleToScopes(user.Role)

	// 计算授权的 scope
	grantedScope := s.intersectScopes(requestedScope, allowedScope)
	if grantedScope == "" {
		grantedScope = allowedScope
	}

	// 更新最后登录时间
	_ = s.userRepo.UpdateLastLogin(user.ID)

	// 生成 JWT
	tokenString, expiresIn, err := s.GenerateJWT(user.ID, user.Username, user.Email, grantedScope)
	if err != nil {
		return nil, fmt.Errorf("server_error: failed to generate token")
	}

	return &TokenResponse{
		AccessToken: tokenString,
		TokenType:   "bearer",
		ExpiresIn:   expiresIn,
		Scope:       grantedScope,
	}, nil
}

// GenerateJWT 生成 JWT token
func (s *BuiltinAuthService) GenerateJWT(sub, username, email, scope string) (string, int64, error) {
	return s.GenerateJWTForNode(sub, username, email, scope, "")
}

func (s *BuiltinAuthService) GenerateJWTForClient(sub, username, email, scope, nodeID, clientType, sitePortalCode string) (string, int64, error) {
	now := time.Now()
	expiresIn := int64(s.tokenExpiry)
	claims := jwt.MapClaims{
		"sub": sub, "preferred_username": username, "email": email, "scope": scope,
		"client_type": clientType, "iss": s.issuer, "iat": now.Unix(),
		"exp": now.Add(time.Duration(s.tokenExpiry) * time.Second).Unix(),
		"realm_access": map[string]interface{}{"roles": strings.Fields(scope)},
	}
	if nodeID != "" {
		claims["node_id"] = nodeID
	}
	if sitePortalCode != "" {
		claims["site_portal_code"] = sitePortalCode
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(s.signingSecret)
	if err != nil {
		return "", 0, fmt.Errorf("failed to sign token: %w", err)
	}
	return tokenString, expiresIn, nil
}

// GenerateJWTForNode emits a signed device token. nodeID is intentionally
// optional for human users but mandatory for Edge device clients.
func (s *BuiltinAuthService) GenerateJWTForNode(sub, username, email, scope, nodeID string) (string, int64, error) {
	now := time.Now()
	expiresIn := int64(s.tokenExpiry)

	// 构建 claims，与 Keycloak JWT 结构兼容
	claims := jwt.MapClaims{
		"sub":                sub,
		"preferred_username": username,
		"email":              email,
		"scope":              scope,
		"realm_access": map[string]interface{}{
			"roles": strings.Fields(scope),
		},
		"iss": s.issuer,
		"iat": now.Unix(),
		"exp": now.Add(time.Duration(s.tokenExpiry) * time.Second).Unix(),
	}
	if nodeID != "" {
		claims["node_id"] = nodeID
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(s.signingSecret)
	if err != nil {
		return "", 0, fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, expiresIn, nil
}

// HandleUserInfo 处理 /auth/userinfo 请求
func (s *BuiltinAuthService) HandleUserInfo(tokenString string) (*UserInfoResponse, error) {
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.issuer),
		jwt.WithExpirationRequired(),
	)
	token, err := parser.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.signingSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	parsedClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	resp := &UserInfoResponse{}
	if sub, ok := parsedClaims["sub"].(string); ok {
		resp.Sub = sub
	}
	if username, ok := parsedClaims["preferred_username"].(string); ok {
		resp.PreferredUsername = username
		resp.Name = username
	}
	if email, ok := parsedClaims["email"].(string); ok {
		resp.Email = email
	}
	if scope, ok := parsedClaims["scope"].(string); ok {
		resp.RealmAccess.Roles = strings.Fields(scope)
	}

	return resp, nil
}

// intersectScopes 计算请求 scope 与允许 scope 的交集
func (s *BuiltinAuthService) intersectScopes(requested, allowed string) string {
	if requested == "" {
		return ""
	}

	allowedSet := make(map[string]bool)
	for _, s := range strings.Fields(allowed) {
		allowedSet[s] = true
	}

	var result []string
	for _, s := range strings.Fields(requested) {
		// 跳过 openid/profile 等标准 OIDC scope（内置模式不需要）
		if s == "openid" || s == "profile" {
			continue
		}
		if allowedSet[s] {
			result = append(result, s)
		}
	}
	return strings.Join(result, " ")
}

// mapUserRoleToScopes 将用户角色映射为 scope
func mapUserRoleToScopes(role string) string {
	switch role {
	case "admin":
		return "fly-print-admin fly-print-operator edge:register edge:printer edge:heartbeat file:read print:submit"
	case "operator":
		return "fly-print-operator edge:register edge:printer edge:heartbeat file:read print:submit"
	default:
		return "file:read"
	}
}

// GenerateClientSecret 生成随机客户端密钥（32字节 base64 编码）
func GenerateClientSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random secret: %w", err)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

// HashClientSecret 对客户端密钥进行 bcrypt 哈希
func HashClientSecret(secret string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash secret: %w", err)
	}
	return string(hash), nil
}
