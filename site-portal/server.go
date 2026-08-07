package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	opsCookieName  = "flyprint_site_portal_ops"
	userCookieName = "flyprint_site_portal_user"
)

type configuration struct {
	Code                     string
	DisplayName              string
	CloudAPIBaseURL          string
	CloudOAuthClientID       string
	CloudOAuthClientSecret   string
	IdentityAuthorizationURL string
	IdentityTokenURL         string
	IdentityUserinfoURL      string
	IdentityClientID         string
	IdentityClientSecret     string
	IdentityScope            string
	IdentityProfileFormat    string
	IdentityCallbackURL      string
	IdentityOpsAPIBaseURL    string
	PRPBaseURL               string
	PRPAPIBaseURL            string
	UploadEnabled            bool
	LoginStateTTL            time.Duration
	ClaimTTL                 time.Duration
	OpsSessionTTL            time.Duration
	UserSessionTTL           time.Duration
	CookieSecure             bool
}

func (c configuration) validate() error {
	required := []string{
		c.Code, c.DisplayName, c.CloudAPIBaseURL, c.CloudOAuthClientID, c.CloudOAuthClientSecret,
		c.IdentityAuthorizationURL, c.IdentityTokenURL, c.IdentityUserinfoURL,
		c.IdentityClientID, c.IdentityClientSecret, c.IdentityScope, c.IdentityCallbackURL,
		c.PRPBaseURL,
	}
	for _, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("Site Portal configuration is incomplete")
		}
	}
	for _, raw := range []string{
		c.CloudAPIBaseURL, c.IdentityAuthorizationURL, c.IdentityTokenURL,
		c.IdentityUserinfoURL, c.IdentityCallbackURL, c.PRPBaseURL,
	} {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("Site Portal URL configuration must use absolute HTTP(S) URLs")
		}
	}
	if len(c.CloudOAuthClientID) < 3 || len(c.CloudOAuthClientSecret) < 32 ||
		len(c.IdentityClientID) < 3 || len(c.IdentityClientSecret) < 32 {
		return fmt.Errorf("Site Portal service credentials must be at least 32 characters")
	}
	profileFormat := strings.ToLower(strings.TrimSpace(c.IdentityProfileFormat))
	if profileFormat != "legacy" && profileFormat != "oidc" {
		return fmt.Errorf("Site Portal OAuth userinfo profile format must be legacy or oidc")
	}
	if c.LoginStateTTL <= 0 || c.ClaimTTL <= 0 || c.OpsSessionTTL <= 0 || c.UserSessionTTL <= 0 {
		return fmt.Errorf("Site Portal TTL values must be positive")
	}
	if c.UploadEnabled {
		parsed, err := url.Parse(c.PRPAPIBaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("Site Portal PRP API URL must use an absolute HTTP(S) URL")
		}
	}
	if raw := strings.TrimSpace(c.IdentityOpsAPIBaseURL); raw != "" {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("Site Portal identity operator API URL must use an absolute HTTP(S) URL")
		}
	}
	return nil
}

type portalServer struct {
	config       configuration
	cloud        cloudBoundary
	identity     identityBoundary
	loginStates  *loginStateStore
	claims       *claimStore
	opsSessions  *localOpsSessionStore
	userSessions *browserSessionStore
	prp          prpBoundary
	now          func() time.Time
}

func newPortalServer(config configuration, cloud cloudBoundary, identity identityBoundary) *portalServer {
	return &portalServer{
		config: config, cloud: cloud, identity: identity,
		loginStates: newLoginStateStore(), claims: newClaimStore(),
		opsSessions: newLocalOpsSessionStore(), userSessions: newBrowserSessionStore(),
		prp: &prpClient{baseURL: strings.TrimRight(config.PRPAPIBaseURL, "/"), client: http.DefaultClient},
		now: time.Now,
	}
}

func (s *portalServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writePortalJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /entry", s.entry)
	mux.HandleFunc("GET /auth/callback", s.authCallback)
	mux.HandleFunc("POST /api/claims/redeem", s.redeemClaim)
	mux.HandleFunc("GET /files", s.filesPage)
	if s.config.UploadEnabled {
		mux.HandleFunc("POST /api/files/upload-context", s.createFileUploadContext)
	}
	if strings.TrimSpace(s.config.IdentityOpsAPIBaseURL) != "" {
		mux.HandleFunc("GET /ops", s.opsPage)
		mux.HandleFunc("POST /api/ops/login", s.opsLogin)
		mux.HandleFunc("POST /api/ops/logout", s.opsLogout)
		mux.HandleFunc("GET /api/ops/users", s.opsUsers)
		mux.HandleFunc("POST /api/ops/users", s.opsUsers)
		mux.HandleFunc("DELETE /api/ops/users/{id}", s.opsDeleteUser)
		mux.HandleFunc("POST /api/ops/users/{id}/reset-password", s.opsResetPassword)
	}
	return portalSecurityHeaders(mux)
}

func (s *portalServer) entry(w http.ResponseWriter, r *http.Request) {
	terminalTicket := strings.TrimSpace(r.URL.Query().Get("terminal_ticket"))
	if terminalTicket == "" {
		renderPortalError(w, http.StatusBadRequest, "二维码无效", "请返回打印终端重新扫码。")
		return
	}
	context, err := s.cloud.validateContext(terminalTicket)
	if err != nil || context.SitePortalCode != s.config.Code || !context.ExpiresAt.After(s.now()) {
		renderPortalError(w, http.StatusGone, "二维码已失效", "请返回打印终端刷新二维码后重新扫码。")
		return
	}
	expiresAt := s.now().Add(s.config.LoginStateTTL)
	if context.ExpiresAt.Before(expiresAt) {
		expiresAt = context.ExpiresAt
	}
	state := s.loginStates.create(loginState{
		TerminalTicket: terminalTicket, Context: context, ExpiresAt: expiresAt,
	})
	loginURL, _ := url.Parse(s.config.IdentityAuthorizationURL)
	query := loginURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", s.config.IdentityClientID)
	query.Set("redirect_uri", s.config.IdentityCallbackURL)
	query.Set("scope", s.config.IdentityScope)
	query.Set("state", state)
	loginURL.RawQuery = query.Encode()

	// 票据校验成功后直接进入统一身份服务，不在 Site Portal 增加中间登录页。
	http.Redirect(w, r, loginURL.String(), http.StatusFound)
}

func (s *portalServer) authCallback(w http.ResponseWriter, r *http.Request) {
	state, err := s.loginStates.redeem(r.URL.Query().Get("state"), s.now())
	if err != nil {
		renderPortalError(w, http.StatusGone, "登录会话已失效", "请返回打印终端重新扫码。")
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("error")) != "" {
		renderPortalError(w, http.StatusBadGateway, "登录失败", "身份服务未完成授权。")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		renderPortalError(w, http.StatusBadRequest, "登录失败", "身份服务没有返回有效授权码。")
		return
	}
	identity, err := s.identity.exchangeCode(code)
	if err != nil {
		renderPortalError(w, http.StatusBadGateway, "登录失败", "身份服务暂时无法完成登录。")
		return
	}
	expiresAt := s.now().Add(s.config.ClaimTTL)
	if identity.ExpiresAt.Before(expiresAt) {
		expiresAt = identity.ExpiresAt
	}
	if state.Context.ExpiresAt.Before(expiresAt) {
		expiresAt = state.Context.ExpiresAt
	}
	if !expiresAt.After(s.now()) {
		renderPortalError(w, http.StatusGone, "登录会话已失效", "请返回打印终端重新扫码。")
		return
	}

	claimCode := s.claims.create(claim{
		SitePortalCode: s.config.Code, NodeID: state.Context.NodeID,
		TerminalSessionID: state.Context.TerminalSessionID,
		ExternalUserID:    identity.ExternalUserID, DisplayName: identity.DisplayName,
		AccessToken: identity.AccessToken, AccessTokenExpiresAt: identity.ExpiresAt,
		PRPBaseURL: s.config.PRPBaseURL, ExpiresAt: expiresAt,
	})
	_, err = s.cloud.completeLogin(loginCompletion{
		TerminalTicket: state.TerminalTicket,
		ExternalUserID: identity.ExternalUserID,
		DisplayName:    identity.DisplayName,
		ClaimCode:      claimCode,
		ClaimExpiresAt: expiresAt,
	})
	if err != nil {
		s.claims.remove(claimCode)
		renderPortalError(w, http.StatusBadGateway, "登录结果未送达", "请返回打印终端重新扫码。")
		return
	}
	if !s.config.UploadEnabled {
		// 当前 Site Portal 只负责完成登录和向 Cloud 发布一次性 Claim，
		// 不提供浏览器业务页面；Edge 收到 Cloud 通知后继续后续流程。
		w.WriteHeader(http.StatusNoContent)
		return
	}
	sessionExpiresAt := s.now().Add(s.config.UserSessionTTL)
	if identity.ExpiresAt.Before(sessionExpiresAt) {
		sessionExpiresAt = identity.ExpiresAt
	}
	sessionKey := s.userSessions.create(browserSession{
		ExternalUserID: identity.ExternalUserID, DisplayName: identity.DisplayName,
		PRPBaseURL: s.config.PRPBaseURL, AccessToken: identity.AccessToken,
		AccessTokenExpiresAt: identity.ExpiresAt, ExpiresAt: sessionExpiresAt,
	})
	http.SetCookie(w, &http.Cookie{
		Name: userCookieName, Value: sessionKey, Path: "/", HttpOnly: true,
		Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode,
		Expires: sessionExpiresAt,
	})
	http.Redirect(w, r, s.filesPath(), http.StatusSeeOther)
}

// filesPath preserves the public reverse-proxy prefix when the portal is
// mounted below a path such as /fly-print-site-portal/.
func (s *portalServer) filesPath() string {
	parsed, err := url.Parse(s.config.IdentityCallbackURL)
	if err == nil {
		callbackPath := path.Clean(parsed.Path)
		if strings.HasSuffix(callbackPath, "/auth/callback") {
			basePath := strings.TrimSuffix(callbackPath, "/auth/callback")
			if basePath == "" {
				return "/files"
			}
			return basePath + "/files"
		}
	}
	return "/files"
}

func (s *portalServer) redeemClaim(w http.ResponseWriter, r *http.Request) {
	var input redeemClaimInput
	if err := decodePortalJSON(r, &input); err != nil {
		writePortalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_claim_request"})
		return
	}
	value, err := s.claims.redeem(input, s.now())
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, errClaimBindingMismatch) {
			status = http.StatusForbidden
		}
		writePortalJSON(w, status, map[string]string{"error": "claim_unavailable"})
		return
	}
	writePortalJSON(w, http.StatusOK, map[string]any{
		"site_portal_code":        value.SitePortalCode,
		"external_user_id":        value.ExternalUserID,
		"display_name":            value.DisplayName,
		"prp_base_url":            value.PRPBaseURL,
		"access_token":            value.AccessToken,
		"access_token_expires_at": value.AccessTokenExpiresAt,
	})
}

func (s *portalServer) userSession(r *http.Request) (browserSession, bool) {
	cookie, err := r.Cookie(userCookieName)
	if err != nil {
		return browserSession{}, false
	}
	return s.userSessions.get(cookie.Value, s.now())
}

func (s *portalServer) filesPage(w http.ResponseWriter, r *http.Request) {
	if !s.config.UploadEnabled {
		session, ok := s.userSession(r)
		if !ok || !session.AccessTokenExpiresAt.After(s.now()) {
			renderPortalError(w, http.StatusUnauthorized, "登录已失效", "请返回打印终端重新扫码登录。")
			return
		}
		body := `<h1>登录成功</h1><p>Site Portal 已完成登录，当前打印终端可以继续获取文件列表。</p>`
		renderPortalPage(w, http.StatusOK, "登录成功", body)
		return
	}
	session, ok := s.userSession(r)
	if !ok || !session.AccessTokenExpiresAt.After(s.now()) {
		renderPortalError(w, http.StatusUnauthorized, "登录已失效", "请返回打印终端重新扫码登录。")
		return
	}
	// 微信内置浏览器会把包含图片类型的 accept 解释为仅拍照/相册。
	// 上传页保持单一文件入口且不设置 accept，实际类型仍由 PRP 严格校验。
	body := `<h1>上传打印文件</h1><p>当前用户：` + template.HTMLEscapeString(session.DisplayName) +
		`</p><input id="file" type="file" hidden><button id="choose-file" type="button" style="width:100%;padding:28px 18px;margin:10px 0 8px">点击选择文件</button><p id="selected-file" class="muted">未选择文件</p><button id="upload" disabled>上传文件</button><p id="result" class="muted">请选择 PDF、图片或 DOCX 文件。</p>
<script>
const input=document.getElementById('file'),choose=document.getElementById('choose-file'),button=document.getElementById('upload'),result=document.getElementById('result'),selected=document.getElementById('selected-file');
choose.onclick=()=>input.click();
input.onchange=()=>{const file=input.files[0];selected.textContent=file?file.name:'未选择文件';button.disabled=!file};
button.onclick=async()=>{
  const file=input.files[0];if(!file){result.textContent='请先选择文件。';return}
  button.disabled=true;result.textContent='正在申请上传…';
  try{
    const contextResponse=await fetch('/api/files/upload-context',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
    const context=await contextResponse.json();if(!contextResponse.ok)throw new Error('无法创建上传任务');
    const form=new FormData();form.append('file',file);
    await new Promise((resolve,reject)=>{
      const request=new XMLHttpRequest();request.open('POST',context.upload_url);
      request.setRequestHeader('Authorization','Bearer '+context.upload_context);
      request.upload.onprogress=event=>{if(event.lengthComputable)result.textContent='上传中 '+Math.round(event.loaded/event.total*100)+'%'};
      request.onload=()=>request.status>=200&&request.status<300?resolve():reject(new Error('上传失败'));
      request.onerror=()=>reject(new Error('上传失败'));request.send(form);
    });
    input.value='';selected.textContent='未选择文件';result.textContent='上传成功，可返回打印终端选择文件。';
  }catch(error){result.textContent=error.message}finally{button.disabled=!input.files[0]}
};
</script>`
	renderPortalPage(w, http.StatusOK, "上传打印文件", body)
}

func (s *portalServer) createFileUploadContext(w http.ResponseWriter, r *http.Request) {
	session, ok := s.userSession(r)
	if !ok {
		writePortalJSON(w, http.StatusUnauthorized, map[string]string{"error": "user_session_invalid"})
		return
	}
	if !session.AccessTokenExpiresAt.After(s.now()) {
		writePortalJSON(w, http.StatusUnauthorized, map[string]string{"error": "prp_token_expired"})
		return
	}
	result, err := s.prp.createUploadContext(session.AccessToken)
	if err != nil {
		writePortalJSON(w, http.StatusBadGateway, map[string]string{"error": "prp_unavailable"})
		return
	}
	writePortalJSON(w, http.StatusCreated, result)
}

func (s *portalServer) opsPage(w http.ResponseWriter, _ *http.Request) {
	renderPortalPage(w, http.StatusOK, "运维用户管理", opsPageBody)
}

func (s *portalServer) opsLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodePortalJSON(r, &input); err != nil {
		writePortalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	identitySession, err := s.identity.opsLogin(strings.TrimSpace(input.Username), input.Password)
	if err != nil {
		writePortalJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
		return
	}
	expiresAt := identitySession.ExpiresAt
	localLimit := s.now().Add(s.config.OpsSessionTTL)
	if localLimit.Before(expiresAt) {
		expiresAt = localLimit
	}
	localToken := s.opsSessions.create(localOpsSession{
		IdentityToken: identitySession.Token, ExpiresAt: expiresAt,
	})
	http.SetCookie(w, &http.Cookie{
		Name: opsCookieName, Value: localToken, Path: "/", HttpOnly: true,
		Secure: s.config.CookieSecure, SameSite: http.SameSiteStrictMode,
		Expires: expiresAt,
	})
	writePortalJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *portalServer) opsSession(r *http.Request) (localOpsSession, bool) {
	cookie, err := r.Cookie(opsCookieName)
	if err != nil {
		return localOpsSession{}, false
	}
	return s.opsSessions.get(cookie.Value, s.now())
}

func (s *portalServer) opsLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(opsCookieName); err == nil {
		s.opsSessions.remove(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: opsCookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: s.config.CookieSecure, SameSite: http.SameSiteStrictMode,
		MaxAge: -1,
	})
	writePortalJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *portalServer) proxyOps(w http.ResponseWriter, r *http.Request, identityPath string) {
	session, ok := s.opsSession(r)
	if !ok {
		writePortalJSON(w, http.StatusUnauthorized, map[string]string{"error": "ops_session_invalid"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writePortalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	response, status, err := s.identity.opsRequest(r.Method, identityPath, session.IdentityToken, body)
	if err != nil {
		writePortalJSON(w, http.StatusBadGateway, map[string]string{"error": "identity_service_unavailable"})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(response)
}

func (s *portalServer) opsUsers(w http.ResponseWriter, r *http.Request) {
	path := "/api/ops/users"
	if search := r.URL.Query().Get("search"); search != "" {
		path += "?search=" + url.QueryEscape(search)
	}
	s.proxyOps(w, r, path)
}

func (s *portalServer) opsDeleteUser(w http.ResponseWriter, r *http.Request) {
	s.proxyOps(w, r, "/api/ops/users/"+url.PathEscape(r.PathValue("id")))
}

func (s *portalServer) opsResetPassword(w http.ResponseWriter, r *http.Request) {
	s.proxyOps(w, r, "/api/ops/users/"+url.PathEscape(r.PathValue("id"))+"/reset-password")
}

func randomToken(byteCount int) string {
	raw := make([]byte, byteCount)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(raw)
}

func decodePortalJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("request body must contain one JSON value")
	}
	return nil
}

func writePortalJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func renderPortalError(w http.ResponseWriter, status int, title, message string) {
	renderPortalPage(w, status, title, `<h1>`+template.HTMLEscapeString(title)+`</h1><p>`+
		template.HTMLEscapeString(message)+`</p>`)
}

func renderPortalPage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>`+
		template.HTMLEscapeString(title)+`</title><style>`+portalPageStyle+`</style></head><body><main class="portal-main">`+
		body+`</main></body></html>`)
}

func portalSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

const opsPageBody = `<h1>官方用户管理</h1>
<section id="login"><label>运维账号<input id="ops-user" autocomplete="username"></label><label>密码<input id="ops-pass" type="password" autocomplete="current-password"></label><button id="ops-login">登录</button></section>
<section id="manager" class="hidden"><div class="row"><input id="search" placeholder="搜索账号或姓名"><button id="refresh">搜索</button><button id="logout">退出</button></div>
<h2>创建用户</h2><input id="new-user" placeholder="账号"><input id="new-name" placeholder="姓名"><input id="new-pass" type="password" placeholder="初始密码"><button id="create">创建</button><div id="users"></div></section><p id="message" class="muted"></p>
<script>
const $=id=>document.getElementById(id),msg=$('message'),login=$('login'),manager=$('manager');
async function api(path,options={}){const r=await fetch(path,{...options,headers:{'Content-Type':'application/json',...(options.headers||{})}});const text=await r.text();let data={};try{data=text?JSON.parse(text):{}}catch{}if(!r.ok)throw new Error(data.message||data.error||'操作失败');return data}
async function load(){const data=await api('/api/ops/users?search='+encodeURIComponent($('search').value));$('users').innerHTML='';for(const u of data.users||[]){const box=document.createElement('div');box.className='user';box.innerHTML='<strong></strong><p class="muted"></p><button class="delete">删除账户</button><button class="reset">重置密码</button>';box.querySelector('strong').textContent=u.display_name;box.querySelector('p').textContent='账号：'+u.username;box.querySelector('.delete').onclick=async()=>{if(confirm('确认删除账户 '+u.username+'？此操作不可恢复。')){await api('/api/ops/users/'+encodeURIComponent(u.id),{method:'DELETE'});msg.textContent='账户已删除';await load()}};box.querySelector('.reset').onclick=async()=>{const p=prompt('输入新密码（至少 10 位）');if(p){await api('/api/ops/users/'+encodeURIComponent(u.id)+'/reset-password',{method:'POST',body:JSON.stringify({new_password:p})});msg.textContent='密码已重置'}};$('users').appendChild(box)}}
$('ops-login').onclick=async()=>{try{await api('/api/ops/login',{method:'POST',body:JSON.stringify({username:$('ops-user').value,password:$('ops-pass').value})});$('ops-pass').value='';login.classList.add('hidden');manager.classList.remove('hidden');await load()}catch(e){msg.textContent=e.message}};
$('refresh').onclick=()=>load().catch(e=>msg.textContent=e.message);
$('create').onclick=async()=>{try{await api('/api/ops/users',{method:'POST',body:JSON.stringify({username:$('new-user').value,display_name:$('new-name').value,password:$('new-pass').value})});$('new-pass').value='';await load()}catch(e){msg.textContent=e.message}};
$('logout').onclick=async()=>{await api('/api/ops/logout',{method:'POST',body:'{}'});manager.classList.add('hidden');login.classList.remove('hidden')};
load().then(()=>{login.classList.add('hidden');manager.classList.remove('hidden')}).catch(()=>{});
</script>`
