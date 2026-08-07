package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type configuration struct {
	AuthorizationURL string
	TokenURL         string
	UserinfoURL      string
	ClientID         string
	ClientSecret     string
	Scope            string
	RedirectURI      string
	ListenAddress    string
	StateTTL         time.Duration
	HTTPTimeout      time.Duration
}

func configurationFromEnvironment() (configuration, error) {
	stateTTL, err := durationEnvironment("SSO_VERIFY_STATE_TTL", 5*time.Minute)
	if err != nil {
		return configuration{}, err
	}
	httpTimeout, err := durationEnvironment("SSO_VERIFY_HTTP_TIMEOUT", 10*time.Second)
	if err != nil {
		return configuration{}, err
	}
	config := configuration{
		AuthorizationURL: strings.TrimSpace(os.Getenv("SSO_VERIFY_AUTHORIZATION_URL")),
		TokenURL:         strings.TrimSpace(os.Getenv("SSO_VERIFY_TOKEN_URL")),
		UserinfoURL:      strings.TrimSpace(os.Getenv("SSO_VERIFY_USERINFO_URL")),
		ClientID:         strings.TrimSpace(os.Getenv("SSO_VERIFY_CLIENT_ID")),
		ClientSecret:     os.Getenv("SSO_VERIFY_CLIENT_SECRET"),
		Scope:            strings.TrimSpace(os.Getenv("SSO_VERIFY_SCOPE")),
		RedirectURI:      strings.TrimSpace(os.Getenv("SSO_VERIFY_REDIRECT_URI")),
		ListenAddress:    strings.TrimSpace(os.Getenv("SSO_VERIFY_LISTEN_ADDR")),
		StateTTL:         stateTTL,
		HTTPTimeout:      httpTimeout,
	}
	if config.ListenAddress == "" {
		config.ListenAddress = "127.0.0.1:8090"
	}
	if err := config.validate(); err != nil {
		return configuration{}, err
	}
	return config, nil
}

func (c configuration) validate() error {
	if c.AuthorizationURL == "" || c.TokenURL == "" ||
		c.ClientID == "" || c.ClientSecret == "" || c.Scope == "" || c.RedirectURI == "" {
		return fmt.Errorf("SSO verify configuration is incomplete")
	}
	if len(c.ClientSecret) < 32 {
		return fmt.Errorf("SSO_VERIFY_CLIENT_SECRET must be at least 32 characters")
	}
	for name, raw := range map[string]string{
		"SSO_VERIFY_AUTHORIZATION_URL": c.AuthorizationURL,
		"SSO_VERIFY_TOKEN_URL":         c.TokenURL,
		"SSO_VERIFY_USERINFO_URL":      c.UserinfoURL,
	} {
		if raw == "" {
			continue
		}
		if err := validateSSOEndpoint(name, raw); err != nil {
			return err
		}
	}
	if err := validateRedirectURI(c.RedirectURI); err != nil {
		return err
	}
	if c.StateTTL <= 0 || c.HTTPTimeout <= 0 {
		return fmt.Errorf("SSO verify timeouts must be positive")
	}
	return nil
}

func validateSSOEndpoint(name, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return fmt.Errorf("%s must be an absolute HTTPS URL without credentials, query, or fragment", name)
	}
	return nil
}

func validateRedirectURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("SSO_VERIFY_REDIRECT_URI must be an absolute callback URL without credentials, query, or fragment")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("SSO_VERIFY_REDIRECT_URI must use HTTPS; HTTP is allowed only for loopback local verification")
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func durationEnvironment(name string, defaultValue time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

type stateStore struct {
	mu    sync.Mutex
	items map[string]time.Time
}

func newStateStore() *stateStore {
	return &stateStore{items: make(map[string]time.Time)}
}

func (s *stateStore) create(expiresAt time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	state := hex.EncodeToString(raw)
	s.mu.Lock()
	s.items[state] = expiresAt
	s.mu.Unlock()
	return state, nil
}

func (s *stateStore) consume(state string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt, ok := s.items[state]
	delete(s.items, state)
	return ok && expiresAt.After(now)
}

type server struct {
	config configuration
	client *http.Client
	states *stateStore
	now    func() time.Time
}

type userProfileView struct {
	Subject     string `json:"subject,omitempty"`
	Username    string `json:"username,omitempty"`
	Email       string `json:"email,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type verificationResult struct {
	LoginSuccess    bool            `json:"login_success"`
	TokenReceived   bool            `json:"token_received"`
	TokenType       string          `json:"token_type"`
	ExpiresIn       int64           `json:"expires_in"`
	UserinfoChecked bool            `json:"userinfo_checked"`
	UserinfoStatus  int             `json:"userinfo_status,omitempty"`
	SubjectPresent  bool            `json:"subject_present"`
	ProfileActive   *bool           `json:"profile_active,omitempty"`
	ProfileClientID string          `json:"profile_client_id,omitempty"`
	Profile         userProfileView `json:"profile"`
}

func newServer(config configuration, client *http.Client) (*server, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: config.HTTPTimeout}
	}
	return &server{
		config: config,
		client: client,
		states: newStateStore(),
		now:    time.Now,
	}, nil
}

func (s *server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /login", s.login)
	mux.HandleFunc("GET /callback", s.callback)
	return securityHeaders(mux)
}

func (s *server) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><title>SSO UAT 验证</title><h1>SSO UAT 登录验证</h1><p>此 Demo 只验证授权码换取 access_token 和 UserInfo，不展示或记录完整 Token。</p><p><a href=\"login\">开始 SSO 登录</a></p></html>")
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	state, err := s.states.create(s.now().UTC().Add(s.config.StateTTL))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state_generation_failed")
		return
	}
	loginURL, err := url.Parse(s.config.AuthorizationURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authorization_endpoint_invalid")
		return
	}
	query := loginURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", s.config.ClientID)
	query.Set("redirect_uri", s.config.RedirectURI)
	query.Set("scope", s.config.Scope)
	query.Set("state", state)
	loginURL.RawQuery = query.Encode()
	http.Redirect(w, r, loginURL.String(), http.StatusFound)
}

func (s *server) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if !s.states.consume(state, s.now().UTC()) {
		writeError(w, http.StatusGone, "state_invalid_or_expired")
		return
	}
	if r.URL.Query().Get("error") != "" {
		writeError(w, http.StatusBadGateway, "sso_authorization_failed")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "authorization_code_missing")
		return
	}
	result, err := s.exchangeCode(code)
	if err != nil {
		writeError(w, http.StatusBadGateway, "sso_token_exchange_failed")
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		writeVerificationPage(w, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) exchangeCode(code string) (verificationResult, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {s.config.ClientID},
		"client_secret": {s.config.ClientSecret},
		"redirect_uri":  {s.config.RedirectURI},
		"code":          {code},
	}
	request, err := http.NewRequest(http.MethodPost, s.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return verificationResult{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return verificationResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return verificationResult{}, fmt.Errorf("token endpoint returned HTTP %d", response.StatusCode)
	}
	tokenPayload, err := decodeJSONObject(response.Body)
	if err != nil {
		return verificationResult{}, err
	}
	accessToken := stringValue(tokenPayload["access_token"])
	tokenType := strings.ToLower(stringValue(tokenPayload["token_type"]))
	expiresIn := integerValue(tokenPayload["expires_in"])
	if accessToken == "" || tokenType != "bearer" || expiresIn <= 0 {
		return verificationResult{}, fmt.Errorf("token response is incomplete")
	}

	result := verificationResult{
		LoginSuccess:    true,
		TokenReceived:   true,
		TokenType:       tokenType,
		ExpiresIn:       expiresIn,
		UserinfoChecked: false,
	}
	if s.config.UserinfoURL == "" {
		return result, nil
	}
	profileRequest, err := http.NewRequest(http.MethodGet, s.config.UserinfoURL, nil)
	if err != nil {
		return verificationResult{}, err
	}
	profileRequest.Header.Set("Authorization", "Bearer "+accessToken)
	profileRequest.Header.Set("Accept", "application/json")
	profileResponse, err := s.client.Do(profileRequest)
	if err != nil {
		return verificationResult{}, err
	}
	defer profileResponse.Body.Close()
	if profileResponse.StatusCode < 200 || profileResponse.StatusCode >= 300 {
		return verificationResult{}, fmt.Errorf("userinfo endpoint returned HTTP %d", profileResponse.StatusCode)
	}
	profile, err := decodeJSONObject(profileResponse.Body)
	if err != nil {
		return verificationResult{}, err
	}
	result.UserinfoChecked = true
	result.UserinfoStatus = profileResponse.StatusCode
	result.Profile = safeUserProfile(profile)
	result.SubjectPresent = result.Profile.Subject != ""
	if active, ok := profile["active"].(bool); ok {
		result.ProfileActive = &active
	}
	if clientID := stringValue(profile["client_id"]); clientID != "" {
		result.ProfileClientID = clientID
	}
	return result, nil
}

func safeUserProfile(profile map[string]any) userProfileView {
	return userProfileView{
		Subject:     firstProfileString(profile, "sub", "id"),
		Username:    firstProfileString(profile, "preferred_username", "username"),
		Email:       firstProfileString(profile, "email"),
		FirstName:   firstProfileString(profile, "given_name", "first_name"),
		LastName:    firstProfileString(profile, "family_name", "last_name"),
		DisplayName: firstProfileString(profile, "name", "display_name"),
	}
}

func firstProfileString(profile map[string]any, names ...string) string {
	for _, name := range names {
		if value := stringValue(profile[name]); value != "" {
			return value
		}
	}
	if attributes, ok := profile["attributes"].(map[string]any); ok {
		for _, name := range names {
			if value := stringValue(attributes[name]); value != "" {
				return value
			}
		}
	}
	return ""
}

var verificationPageTemplate = template.Must(template.New("verification").Parse(`<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>SSO UAT 验证结果</title></head>
<body>
<h1>SSO UAT 验证成功</h1>
<p>Site Portal 已通过授权码取得 Bearer Token，并成功调用 UserInfo。完整 Token、授权码和 Client Secret 不会展示。</p>
<h2>Token 验证</h2>
<ul>
<li>登录成功：{{.LoginSuccess}}</li>
<li>Token 已取得：{{.TokenReceived}}</li>
<li>Token 类型：{{.TokenType}}</li>
<li>有效期：{{.ExpiresIn}} 秒</li>
<li>UserInfo 状态：{{.UserinfoStatus}}</li>
<li>主体存在：{{.SubjectPresent}}</li>
</ul>
<h2>个人信息（来自 UserInfo）</h2>
<table>
<tr><th>字段</th><th>值</th></tr>
<tr><td>Subject</td><td>{{or .Profile.Subject "未返回"}}</td></tr>
<tr><td>用户名</td><td>{{or .Profile.Username "未返回"}}</td></tr>
<tr><td>邮箱</td><td>{{or .Profile.Email "未返回"}}</td></tr>
<tr><td>名字</td><td>{{or .Profile.FirstName "未返回"}}</td></tr>
<tr><td>姓氏</td><td>{{or .Profile.LastName "未返回"}}</td></tr>
<tr><td>显示名</td><td>{{or .Profile.DisplayName "未返回"}}</td></tr>
</table>
<p><a href="login">重新登录</a></p>
</body>
</html>`))

func writeVerificationPage(w http.ResponseWriter, result verificationResult) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	if err := verificationPageTemplate.Execute(w, result); err != nil {
		log.Printf("render verification page: %v", err)
	}
}

func decodeJSONObject(reader io.Reader) (map[string]any, error) {
	var value map[string]any
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("response is not a JSON object")
	}
	return value, nil
}

func stringValue(value any) string {
	result, _ := value.(string)
	return strings.TrimSpace(result)
}

func integerValue(value any) int64 {
	switch result := value.(type) {
	case float64:
		return int64(result)
	case int64:
		return result
	case json.Number:
		parsed, _ := result.Int64()
		return parsed
	default:
		return 0
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code}})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func main() {
	config, err := configurationFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	server, err := newServer(config, nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("SSO verify demo listening on %s", config.ListenAddress)
	log.Fatal(http.ListenAndServe(config.ListenAddress, server.Handler()))
}
