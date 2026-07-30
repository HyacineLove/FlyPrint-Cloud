# Slice 1 Unified Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first runnable vertical slice in which an official or private-domain user signs in through a Site Portal, Cloud silently maps the external identity, and the matching Edge claims the user's PRP access credential without that credential entering Cloud.

**Architecture:** Cloud keeps the existing short-lived terminal ticket as the physical-presence proof, but routes the selected ticket to one configured default Site Portal instead of the legacy upload page. The Site Portal uses an authorization-code exchange with the identity demo, retains the returned PRP credential only in memory, reports a one-time claim code to Cloud, and Cloud notifies only the matching Edge session. Edge validates the terminal session, atomically claims the identity and credential from the Site Portal, and stores the result only in process memory.

**Tech Stack:** Go 1.25 (`net/http`, Gin, PostgreSQL, sqlmock), Python 3/FastAPI client integration, browser HTML/CSS/JavaScript, Docker.

## Global Constraints

- No fallback authentication path, alternate file path, or compatibility printing path may be added.
- Cloud must not receive or persist the identity-service access token, login cookie, user password, or future PRP file bytes.
- The raw terminal ticket is consumed only after the identity login succeeds and Cloud accepts the completion.
- A claim code is single-use, expires after five minutes, and is bound to Site Portal, Edge node, terminal session, and external user.
- Official account creation does not pre-create a Cloud user; the Cloud user appears on the first successful login only.
- Passwords are bcrypt-hashed, never returned by APIs, and never written to logs.
- Slice 1 does not implement file upload, PRP listing/download, print authorization, multiple Site Portal selection, or old-chain removal.

---

### Task 1: Freeze the cross-service protocol

**Files:**
- Create: `api/internal/models/site_portal.go`
- Modify: `api/internal/websocket/message.go`
- Test: `api/internal/websocket/site_portal_protocol_test.go`

**Interfaces:**
- Produces: `SitePortal`, `ExternalIdentity`, `PortalLoginCompletion`, `PortalSessionReadyPayload`.
- Produces message type `portal_session_ready`.

- [ ] **Step 1: Write the failing serialization test**

```go
func TestPortalSessionReadyDoesNotSerializePrivateCredential(t *testing.T) {
    payload := PortalSessionReadyPayload{
        SitePortalCode: "official",
        ClaimBaseURL: "https://portal.example.test",
        ClaimCode: "claim-1",
        TerminalSessionID: "session-1",
        CloudUserID: "user-1",
    }
    raw, err := json.Marshal(payload)
    if err != nil { t.Fatal(err) }
    text := string(raw)
    if !strings.Contains(text, `"claim_code":"claim-1"`) { t.Fatalf("missing claim code: %s", text) }
    for _, forbidden := range []string{"access_token", "prp_credential", "cookie", "password"} {
        if strings.Contains(text, forbidden) { t.Fatalf("private field %q leaked: %s", forbidden, text) }
    }
}
```

- [ ] **Step 2: Run the focused test and confirm it fails because the payload type is missing**

Run: `go test ./internal/websocket -run TestPortalSessionReadyDoesNotSerializePrivateCredential -v`

- [ ] **Step 3: Add the minimal protocol models**

```go
type PortalSessionReadyPayload struct {
    SitePortalCode   string    `json:"site_portal_code"`
    ClaimBaseURL     string    `json:"claim_base_url"`
    ClaimCode        string    `json:"claim_code"`
    TerminalSessionID string   `json:"terminal_session_id"`
    CloudUserID      string    `json:"cloud_user_id"`
    ExpiresAt        time.Time `json:"expires_at"`
}
```

- [ ] **Step 4: Run the focused test and the full websocket package**

Run: `go test ./internal/websocket -v`

- [ ] **Step 5: Commit the protocol checkpoint**

```powershell
git add api/internal/models/site_portal.go api/internal/websocket/message.go api/internal/websocket/site_portal_protocol_test.go
git commit -m "feat: define site portal identity protocol"
```

### Task 2: Add Cloud Site Portal configuration and external identity mapping

**Files:**
- Create: `api/internal/database/migrations/011_site_portal_identity.sql`
- Create: `api/internal/database/site_portal_repository.go`
- Create: `api/internal/database/site_portal_repository_test.go`
- Create: `api/internal/database/external_identity_repository.go`
- Create: `api/internal/database/external_identity_repository_test.go`
- Modify: `api/internal/database/migrations/runner.go`
- Modify: `api/internal/database/database.go`
- Modify: `api/internal/models/models.go`

**Interfaces:**
- Produces: `GetByCode(code string)`, `GetDefaultForNode(nodeID string)`, `Authenticate(code, rawToken string)`.
- Produces: `CompleteLogin(portalCode, ticketHash, externalUserID, displayName string, now time.Time) (*PortalLoginCompletion, error)`.

- [ ] **Step 1: Write failing sqlmock tests**

```go
func TestCompleteLoginCreatesMappingAndConsumesTicketInOneTransaction(t *testing.T) {
    // Expect ticket/session lock, no existing mapping, user insert, identity insert,
    // terminal ticket consume, and transaction commit. Assert returned node,
    // terminal session, and Cloud user IDs are literal expected values.
}

func TestCompleteLoginRejectsInactiveMappedUserWithoutConsumingTicket(t *testing.T) {
    // Expect rollback after reading status=inactive and no UPDATE terminal_tickets.
}
```

- [ ] **Step 2: Run the repository tests and confirm missing repository failures**

Run: `go test ./internal/database -run 'Test(GetDefaultSitePortal|CompleteLogin)' -v`

- [ ] **Step 3: Add normalized tables and repository code**

```sql
CREATE TABLE IF NOT EXISTS site_portals (
  code VARCHAR(64) PRIMARY KEY,
  display_name VARCHAR(120) NOT NULL,
  entry_url VARCHAR(1000) NOT NULL,
  claim_base_url VARCHAR(1000) NOT NULL,
  api_token_hash CHAR(64) NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE edge_nodes
  ADD COLUMN IF NOT EXISTS default_site_portal_code VARCHAR(64);
CREATE TABLE IF NOT EXISTS external_identities (
  site_portal_code VARCHAR(64) NOT NULL REFERENCES site_portals(code),
  external_user_id VARCHAR(255) NOT NULL,
  cloud_user_id UUID NOT NULL REFERENCES users(id),
  display_name VARCHAR(120) NOT NULL,
  last_login_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (site_portal_code, external_user_id)
);
```

- [ ] **Step 4: Implement deterministic silent-user identifiers**

```go
func silentUserLogin(sitePortalCode, externalUserID string) (string, string) {
    digest := sha256.Sum256([]byte(sitePortalCode + "\x00" + externalUserID))
    suffix := hex.EncodeToString(digest[:12])
    return "sp_" + suffix, suffix + "@identity.flyprint.invalid"
}
```

- [ ] **Step 5: Run database tests**

Run: `go test ./internal/database -v`

- [ ] **Step 6: Commit the Cloud persistence checkpoint**

```powershell
git add api/internal/database api/internal/models
git commit -m "feat: persist site portal identity mappings"
```

### Task 3: Route terminal entry and complete login in Cloud

**Files:**
- Create: `api/internal/handlers/site_portal_handler.go`
- Create: `api/internal/handlers/site_portal_handler_test.go`
- Modify: `api/internal/handlers/terminal_ticket_handler.go`
- Modify: `api/internal/handlers/terminal_ticket_handler_test.go`
- Modify: `api/internal/websocket/manager.go`
- Modify: `api/cmd/server/main.go`
- Modify: `api/internal/config/config.go`
- Modify: `api/config.example.yaml`

**Interfaces:**
- Consumes: repositories from Task 2.
- Produces: `POST /api/v1/site-portal/context`.
- Produces: `POST /api/v1/site-portal/login-completions`.
- Produces: default redirect from `/entry?terminal_ticket=...`.

- [ ] **Step 1: Write failing handler tests for delayed consumption**

```go
func TestContextValidationDoesNotConsumeTerminalTicket(t *testing.T) {
    // Send authenticated portal request with a selected valid ticket.
    // Assert 200 context and that CompleteLogin was not invoked.
}

func TestLoginCompletionMapsUserThenSendsCredentialFreeReadyMessage(t *testing.T) {
    // Assert 200, mapped Cloud user ID, target node, and payload with claim code
    // but no identity access token.
}
```

- [ ] **Step 2: Write the failing default redirect test**

```go
func TestEntryPageRedirectsToConfiguredDefaultSitePortal(t *testing.T) {
    // Valid ticket + matching Edge session + enabled default portal.
    // Assert 302 Location is the configured entry URL with only terminal_ticket added.
}
```

- [ ] **Step 3: Run focused handlers tests and verify expected failures**

Run: `go test ./internal/handlers -run 'Test(ContextValidation|LoginCompletion|EntryPageRedirects)' -v`

- [ ] **Step 4: Implement strict Site Portal bearer authentication and handlers**

```go
type completePortalLoginRequest struct {
    TerminalTicket string `json:"terminal_ticket" binding:"required"`
    ExternalUserID string `json:"external_user_id" binding:"required"`
    DisplayName    string `json:"display_name" binding:"required"`
    ClaimCode      string `json:"claim_code" binding:"required"`
    ClaimExpiresAt time.Time `json:"claim_expires_at" binding:"required"`
}
```

- [ ] **Step 5: Add `DispatchPortalSessionReady` and route wiring**

```go
func (m *ConnectionManager) DispatchPortalSessionReady(nodeID string, payload PortalSessionReadyPayload) error {
    raw, err := json.Marshal(&Message{Type: CmdTypePortalSessionReady, NodeID: nodeID, Timestamp: time.Now(), Data: payload})
    if err != nil { return err }
    return m.SendToNode(nodeID, raw)
}
```

- [ ] **Step 6: Run handler, websocket, and full Cloud tests**

Run: `go test ./internal/handlers ./internal/websocket ./...`

- [ ] **Step 7: Commit the Cloud HTTP/WS checkpoint**

```powershell
git add api
git commit -m "feat: complete site portal login through cloud"
```

### Task 4: Implement the identity-service demo

**Files:**
- Create: `sso-login-demo/go.mod`
- Create: `sso-login-demo/main.go`
- Create: `sso-login-demo/server.go`
- Create: `sso-login-demo/store.go`
- Create: `sso-login-demo/server_test.go`
- Create: `sso-login-demo/Dockerfile`

**Interfaces:**
- Produces: `GET/POST /login`, `POST /api/token`.
- Produces: `POST /api/ops/login`, `GET/POST /api/ops/users`, `PATCH /api/ops/users/{id}/enabled`, `POST /api/ops/users/{id}/reset-password`.
- Produces one-time authorization codes and short-lived PRP access tokens.

- [ ] **Step 1: Write failing behavior tests**

```go
func TestPublicRegistrationRouteDoesNotExist(t *testing.T) {
    srv := newTestServer(t, testConfig())
    req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{"username":"new"}`))
    rec := httptest.NewRecorder()
    srv.Handler().ServeHTTP(rec, req)
    if rec.Code != http.StatusNotFound { t.Fatalf("status=%d", rec.Code) }
}

func TestDisabledUserCannotLogin(t *testing.T) {
    srv := newTestServer(t, testConfig())
    userID := srv.store.createUser("teacher", "张老师", "ValidPass123!")
    srv.store.setEnabled(userID, false)
    rec := postForm(srv.Handler(), "/login", url.Values{"username":{"teacher"}, "password":{"ValidPass123!"}})
    if rec.Code != http.StatusUnauthorized { t.Fatalf("status=%d", rec.Code) }
}

func TestAuthorizationCodeCanBeExchangedOnlyOnce(t *testing.T) {
    srv := newTestServer(t, testConfig())
    code := srv.codes.issue("user-1", time.Now().Add(time.Minute))
    first := postJSON(srv.Handler(), "/api/token", map[string]string{"code": code})
    second := postJSON(srv.Handler(), "/api/token", map[string]string{"code": code})
    if first.Code != http.StatusOK || second.Code != http.StatusConflict {
        t.Fatalf("statuses=%d,%d", first.Code, second.Code)
    }
}
```

- [ ] **Step 2: Run tests and confirm the server/store types are missing**

Run: `go test ./... -v`

- [ ] **Step 3: Implement file-backed user storage with atomic replace**

```go
type user struct {
    ID string `json:"id"`
    Username string `json:"username"`
    DisplayName string `json:"display_name"`
    PasswordHash string `json:"password_hash"`
    Enabled bool `json:"enabled"`
}
```

- [ ] **Step 4: Implement ops sessions, user operations, authorization code exchange, and login pages**

The code exchange returns exactly:

```json
{
  "external_user_id": "stable-user-id",
  "display_name": "演示用户",
  "access_token": "opaque-prp-credential",
  "expires_at": "2026-07-30T12:05:00Z"
}
```

- [ ] **Step 5: Run demo tests**

Run: `go test ./... -v`

- [ ] **Step 6: Commit the identity demo checkpoint**

```powershell
git add sso-login-demo
git commit -m "feat: add identity login demo"
```

### Task 5: Implement the formal Site Portal service

**Files:**
- Create: `site-portal/go.mod`
- Create: `site-portal/main.go`
- Create: `site-portal/server.go`
- Create: `site-portal/claim_store.go`
- Create: `site-portal/cloud_client.go`
- Create: `site-portal/identity_client.go`
- Create: `site-portal/server_test.go`
- Create: `site-portal/Dockerfile`

**Interfaces:**
- Consumes Cloud context/login completion endpoints from Task 3.
- Consumes identity login/code/ops endpoints from Task 4.
- Produces: `GET /entry`, `GET /auth/callback`, `POST /api/claims/redeem`, and `/ops`.

- [ ] **Step 1: Write failing claim-store tests**

```go
func TestClaimCanBeRedeemedOnlyOnce(t *testing.T) {
    store := newClaimStore()
    code := store.create(claim{SitePortalCode:"official", NodeID:"edge-1", TerminalSessionID:"session-1", ExternalUserID:"user-1", ExpiresAt:time.Now().Add(time.Minute)})
    input := redeemClaimInput{Code:code, SitePortalCode:"official", NodeID:"edge-1", TerminalSessionID:"session-1"}
    if _, err := store.redeem(input, time.Now()); err != nil { t.Fatal(err) }
    if _, err := store.redeem(input, time.Now()); !errors.Is(err, errClaimUnavailable) {
        t.Fatalf("second redemption error=%v", err)
    }
}

func TestClaimRejectsWrongTerminalBinding(t *testing.T) {
    store := newClaimStore()
    code := store.create(claim{SitePortalCode:"official", NodeID:"edge-1", TerminalSessionID:"session-1", ExternalUserID:"user-1", ExpiresAt:time.Now().Add(time.Minute)})
    _, err := store.redeem(redeemClaimInput{Code:code, SitePortalCode:"official", NodeID:"edge-1", TerminalSessionID:"session-2"}, time.Now())
    if !errors.Is(err, errClaimBindingMismatch) { t.Fatalf("error=%v", err) }
}
```

- [ ] **Step 2: Write failing login-flow tests**

```go
func TestCloudRejectionDeletesUnpublishedClaim(t *testing.T) {
    cloud := &fakeCloudClient{completeErr: errors.New("rejected")}
    srv := newTestServer(t, testConfig(), cloud, successfulIdentityClient())
    state := srv.loginStates.create(validTerminalContext())
    rec := httptest.NewRecorder()
    srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/callback?state="+state+"&code=identity-code", nil))
    if rec.Code != http.StatusBadGateway { t.Fatalf("status=%d", rec.Code) }
    if srv.claims.count() != 0 { t.Fatalf("unpublished claims=%d", srv.claims.count()) }
}

func TestEntryRejectsTerminalContextThatCloudDoesNotValidate(t *testing.T) {
    cloud := &fakeCloudClient{contextErr: errors.New("invalid terminal")}
    srv := newTestServer(t, testConfig(), cloud, successfulIdentityClient())
    rec := httptest.NewRecorder()
    srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/entry?terminal_ticket=bad", nil))
    if rec.Code != http.StatusGone { t.Fatalf("status=%d", rec.Code) }
}
```

- [ ] **Step 3: Run tests and confirm missing service failures**

Run: `go test ./... -v`

- [ ] **Step 4: Implement in-memory OAuth state and claim storage**

```go
type claim struct {
    Code string
    SitePortalCode string
    NodeID string
    TerminalSessionID string
    ExternalUserID string
    DisplayName string
    AccessToken string
    AccessTokenExpiresAt time.Time
    ExpiresAt time.Time
}
```

- [ ] **Step 5: Implement user login pages and the ops proxy UI**

The Site Portal forwards ops calls to the identity service and keeps only an HttpOnly opaque ops session; it never persists passwords or password hashes.

- [ ] **Step 6: Run Site Portal tests**

Run: `go test ./... -v`

- [ ] **Step 7: Commit the Site Portal checkpoint**

```powershell
git add site-portal
git commit -m "feat: add site portal identity flow"
```

### Task 6: Bind the claimed identity to the Edge memory session

**Files (Edge repository):**
- Create: `portal_session.py`
- Create: `site_portal_client.py`
- Create: `tests/test_portal_session.py`
- Create: `tests/test_site_portal_client.py`
- Create: `tests/test_portal_identity_flow.py`
- Modify: `interactive_session.py`
- Modify: `main.py`
- Modify: `cloud_service.py`

**Interfaces:**
- Consumes `portal_session_ready`.
- Produces: `SitePortalClient.redeem(...)`.
- Produces: `PortalSessionManager.bind(...)`, `snapshot()`, and `clear()`.

- [ ] **Step 1: Write failing memory-session tests**

```python
def test_portal_session_binds_only_matching_terminal_session():
    manager = PortalSessionManager()
    assert manager.bind("session-1", {"terminal_session_id": "session-2"}) is False

def test_portal_session_snapshot_never_exposes_access_token():
    manager = PortalSessionManager()
    manager.bind("session-1", {
        "terminal_session_id": "session-1",
        "external_user_id": "user-1",
        "display_name": "用户一",
        "access_token": "secret-token",
    })
    assert "access_token" not in manager.snapshot()
```

- [ ] **Step 2: Run focused tests and confirm module import failures**

Run: `python -m unittest tests.test_portal_session -v`

- [ ] **Step 3: Implement the strict claim client**

```python
class SitePortalClient:
    def redeem(self, claim_base_url, claim_code, site_portal_code, node_id, terminal_session_id):
        response = self._session.post(
            claim_base_url.rstrip("/") + "/api/claims/redeem",
            json={
                "claim_code": claim_code,
                "site_portal_code": site_portal_code,
                "node_id": node_id,
                "terminal_session_id": terminal_session_id,
            },
            timeout=self._timeout,
        )
        response.raise_for_status()
        payload = response.json()
        required = {"external_user_id", "display_name", "access_token", "access_token_expires_at"}
        if not required.issubset(payload):
            raise SitePortalProtocolError("Site Portal 领取响应不完整")
        return payload
```

- [ ] **Step 4: Write and run a failing ready-message flow test**

```python
def test_ready_message_for_other_terminal_session_is_not_claimed(self):
    # Assert the HTTP client is not called and no portal session is stored.
```

- [ ] **Step 5: Implement ready-message handling and cleanup**

Register `portal_session_ready`, validate the active interactive session, redeem exactly once, bind the public identity to the interactive session, keep the credential only in `PortalSessionManager`, and clear both managers on logout/restart.

- [ ] **Step 6: Run focused and full Edge tests**

Run: `python -m unittest tests.test_portal_session tests.test_site_portal_client tests.test_portal_identity_flow -v`

Run: `python -m unittest discover -s tests -p 'test_*.py'`

- [ ] **Step 7: Commit the Edge checkpoint**

```powershell
git add portal_session.py site_portal_client.py interactive_session.py main.py cloud_service.py tests
git commit -m "feat: claim site portal identity on edge"
```

### Task 7: Add component configuration and protocol integration coverage

**Files (Cloud repository):**
- Create: `site-portal/config.example.env`
- Create: `sso-login-demo/config.example.env`
- Create: `docs/agent/site-portal-identity-protocol.md`
- Create: `site-portal/protocol_integration_test.go`

**Interfaces:**
- Produces official and private-demo component configuration without production addresses or credentials.
- Leaves the public integrated Docker Compose unchanged until Slice 6.

- [ ] **Step 1: Add explicit non-secret example variables**

```dotenv
SITE_PORTAL_CODE=official
SITE_PORTAL_CLOUD_API_BASE=http://127.0.0.1:8080
SITE_PORTAL_IDENTITY_BASE_URL=http://127.0.0.1:8081
SITE_PORTAL_PRP_BASE_URL=http://127.0.0.1:8082
SITE_PORTAL_API_TOKEN=replace-with-random-token
```

- [ ] **Step 2: Add a protocol integration test**

Use `httptest.Server` instances for the identity service and Cloud boundary, then drive Site Portal entry, login callback, Cloud completion, and claim redemption. Assert the identity access token appears only in the final claim response and never in the fake Cloud request body.

- [ ] **Step 3: Run all component tests**

Run from `api`: `go test ./...`

Run from `site-portal`: `go test ./...`

Run from `sso-login-demo`: `go test ./...`

- [ ] **Step 4: Commit integration configuration**

```powershell
git add site-portal sso-login-demo docs/agent
git commit -m "test: cover slice one identity protocol"
```

### Task 8: Verify the complete slice and update execution status

**Files:**
- Modify workspace root: `FlyPrint开发任务清单.md`
- Modify affected Cloud and Edge agent/developer documentation only where its current guidance contradicts the implemented protocol.

**Interfaces:**
- Produces verified Cloud and Edge feature branches ready for review.

- [ ] **Step 1: Run all Cloud-side tests**

```powershell
Set-Location api
go test ./...
Set-Location ..\site-portal
go test ./...
Set-Location ..\sso-login-demo
go test ./...
```

- [ ] **Step 2: Run all Edge tests**

```powershell
python -m unittest discover -s tests -p 'test_*.py'
```

- [ ] **Step 3: Run the minimum integration demonstration**

Start the test services, create an official user through `/ops`, scan a current Edge QR, complete login, verify the Cloud mapping query, and verify the Edge public session snapshot contains the user but no access token.

- [ ] **Step 4: Scan diffs and runtime output for private data**

Check for real IP addresses, passwords, API tokens, cookies, PRP access tokens, and real user data. Example files may contain placeholders only.

- [ ] **Step 5: Mark Slice 1 complete only after tests, integration, and commits are all confirmed**

Change Slice 1 from `[~]` to `[x]`; leave Slices 2–6 unchanged.
