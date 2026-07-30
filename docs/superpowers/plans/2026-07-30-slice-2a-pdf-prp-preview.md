# Slice 2A PDF PRP Preview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the first runnable Slice 2 path in which an authenticated official user uploads one PDF directly to PRP Demo, Edge lists and downloads it with the current PRP credential, and the existing Edge pipeline renders a local preview without file bytes entering Site Portal or Cloud.

**Architecture:** SSO Login Demo signs an opaque-to-consumers HMAC PRP Token. PRP Demo validates that token, owns SQLite metadata and volume files, and issues a one-use browser upload context. Site Portal keeps a short browser session and brokers only the upload context; Edge keeps the PRP credential in its existing process-memory portal session, downloads the selected PDF into a session-bound local selection store, and reuses `DocumentPipeline`.

**Tech Stack:** Go 1.25, `net/http`, HMAC-SHA256, `modernc.org/sqlite`, Docker Compose, Python 3, FastAPI, requests, browser ES modules, Node built-in test runner.

## Global Constraints

- This plan intentionally ends at the PDF end-to-end checkpoint. JPEG, PNG, DOCX, eviction, quota enforcement, periodic cleanup, and startup reconciliation belong to the following Slice 2B plan.
- Browser file bytes go directly to PRP Demo. Site Portal and Cloud must not receive request bodies containing file bytes.
- PRP credentials and upload contexts must not enter Cloud, browser session storage, Edge public snapshots, logs, URLs, or repository examples.
- Edge treats the PRP Token as opaque and sends it only in the `Authorization` header.
- No Cloud file list, download, preview, object-storage, anonymous-download, alternate-PRP, or retry fallback is added.
- No print authorization or IPP call is added.
- Each production change follows a witnessed RED → GREEN test cycle.

---

### Task 1: Sign verifiable PRP Tokens in SSO Login Demo

**Files:**
- Create: `sso-login-demo/prp_token.go`
- Create: `sso-login-demo/prp_token_test.go`
- Modify: `sso-login-demo/main.go`
- Modify: `sso-login-demo/server.go`
- Modify: `sso-login-demo/server_test.go`
- Modify: `sso-login-demo/config.example.env`

**Interfaces:**
- Consumes: `externalUserID string`, configured `issuer`, `audience`, `sitePortalCode`, HMAC secret, scopes, issue and expiry times.
- Produces: `signPRPToken(config prpTokenConfig, subject string, issuedAt, expiresAt time.Time) (string, error)`.
- Token wire format: `base64url(header).base64url(claims).base64url(hmacSHA256(signingInput))`.
- Claims: `iss`, `aud`, `site_portal_code`, `sub`, `scope`, `iat`, `exp`, `jti`.

- [x] **Step 1: Write failing signer tests**

Add literal contract assertions:

```go
func TestSignPRPTokenProducesThreeSegmentsAndPublicClaims(t *testing.T) {
	token, err := signPRPToken(testPRPTokenConfig(), "user-1",
		time.Unix(1000, 0).UTC(), time.Unix(1300, 0).UTC())
	if err != nil { t.Fatal(err) }
	segments := strings.Split(token, ".")
	if len(segments) != 3 { t.Fatalf("segments=%d", len(segments)) }
	claims := decodeTestClaims(t, segments[1])
	if claims["sub"] != "user-1" || claims["aud"] != "flyprint-prp-demo" ||
		claims["site_portal_code"] != "official" {
		t.Fatalf("claims=%#v", claims)
	}
}
```

Also assert that the signing secret never appears in the serialized token. The public-claims test independently proves that the requested subject is encoded.

- [x] **Step 2: Run RED**

Run from `sso-login-demo`:

```powershell
go test . -run TestSignPRPToken -v
```

Expected: compile failure because `signPRPToken` and `prpTokenConfig` do not exist.

- [x] **Step 3: Implement signer and configuration**

Add required environment variables:

```text
SSO_PRP_TOKEN_SECRET
SSO_PRP_TOKEN_ISSUER
SSO_PRP_TOKEN_AUDIENCE
SSO_SITE_PORTAL_CODE
```

Reject secrets shorter than 32 characters. Use `crypto/rand` for `jti`, canonical JSON structs, `base64.RawURLEncoding`, and `hmac.Equal` only in verifier code.

- [x] **Step 4: Replace random access token**

In `exchangeCode`, sign a token for `grant.ExternalUserID` with scopes:

```text
files:list files:download upload-context:create
```

Keep the existing response fields `external_user_id`, `display_name`, `access_token`, and `expires_at` unchanged.

- [x] **Step 5: Run GREEN**

```powershell
go test . -v
```

Expected: all SSO Login Demo tests pass and response tests still observe a non-empty credential without printing it.

- [x] **Step 6: Commit Cloud repository**

```powershell
git add sso-login-demo
git commit -m "feat: sign demo PRP access tokens"
```

---

### Task 2: Create PRP Demo authentication and health skeleton

**Files:**
- Create: `prp-demo/go.mod`
- Create: `prp-demo/main.go`
- Create: `prp-demo/config.go`
- Create: `prp-demo/token.go`
- Create: `prp-demo/token_test.go`
- Create: `prp-demo/server.go`
- Create: `prp-demo/server_test.go`
- Create: `prp-demo/Dockerfile`
- Create: `prp-demo/config.example.env`

**Interfaces:**
- Consumes: SSO Token wire contract from Task 1.
- Produces: `verifyPRPToken(raw string, now time.Time) (accessClaims, error)`.
- Produces: `GET /health` returning `{"status":"ok"}`.
- `accessClaims` exposes only `Subject`, `SitePortalCode`, `Scopes`, `ExpiresAt`, and `TokenID`.

- [x] **Step 1: Write independent verifier tests**

Use a fixed known HMAC secret and independently constructed literal tokens. Cover:

```go
func TestVerifyPRPTokenRejectsTamperedPayload(t *testing.T)
func TestVerifyPRPTokenRejectsWrongAudience(t *testing.T)
func TestVerifyPRPTokenRejectsWrongSitePortal(t *testing.T)
func TestVerifyPRPTokenRejectsExpiredToken(t *testing.T)
func TestVerifyPRPTokenRequiresScope(t *testing.T)
```

The test must not call the SSO signer to build its expected claims.

- [x] **Step 2: Run RED**

Run from `prp-demo`:

```powershell
go test . -run TestVerifyPRPToken -v
```

Expected: compile failure because the verifier is absent.

- [x] **Step 3: Implement configuration and verifier**

Required configuration:

```text
PRP_DATA_DIR
PRP_DATABASE_FILE
PRP_TOKEN_SECRET
PRP_TOKEN_ISSUER
PRP_TOKEN_AUDIENCE
PRP_SITE_PORTAL_CODE
PRP_ALLOWED_UPLOAD_ORIGINS
PRP_PUBLIC_BASE_URL
PRP_MAX_FILE_SIZE_BYTES
```

Reject incomplete URLs, wildcard origins, secrets under 32 characters, non-positive file limits, and database paths outside `PRP_DATA_DIR`. The 2A default file limit is 50 MiB.

- [x] **Step 4: Implement server skeleton**

Create a `server` with dependency-injected clock and verifier. Add JSON error responses with `Cache-Control: no-store`, security headers, and a health route. Do not add file routes yet.

- [x] **Step 5: Run GREEN**

```powershell
go test . -v
```

- [x] **Step 6: Commit Cloud repository**

```powershell
git add prp-demo
git commit -m "feat: add authenticated PRP demo skeleton"
```

---

### Task 3: Add one-use upload context and PDF persistence

**Files:**
- Create: `prp-demo/upload_context_store.go`
- Create: `prp-demo/upload_context_store_test.go`
- Create: `prp-demo/file_store.go`
- Create: `prp-demo/file_store_test.go`
- Create: `prp-demo/file_types.go`
- Modify: `prp-demo/config.go`
- Modify: `prp-demo/server.go`
- Modify: `prp-demo/server_test.go`
- Modify: `prp-demo/go.mod`
- Create: `prp-demo/go.sum`

**Interfaces:**
- Produces: `POST /api/v1/upload-contexts`.
- Produces: `POST /api/v1/files` accepting one multipart field named `file`.
- Produces: `GET /api/v1/files?page=&page_size=`.
- Produces: `GET /api/v1/files/{id}/content`.
- SQLite driver: `modernc.org/sqlite`.

- [x] **Step 1: Write failing upload-context tests**

Cover one-use and binding behavior:

```go
func TestUploadContextCanBeConsumedOnlyOnce(t *testing.T)
func TestUploadContextRejectsExpiredValue(t *testing.T)
func TestUploadContextCarriesOnlySubjectAndExpiry(t *testing.T)
```

Run:

```powershell
go test . -run TestUploadContext -v
```

Expected: compile failure because the store is absent.

- [x] **Step 2: Implement upload-context store**

Use a mutex-protected in-memory map keyed by 32 random bytes encoded as lowercase hex. `consume` deletes before checking expiry so expired or attempted contexts cannot be retried.

- [x] **Step 3: Write failing real-store HTTP test**

Start the real handler with a temporary SQLite database and volume directory. Create an upload context, upload the literal bytes of a valid one-page PDF fixture, list as the same user, download, and compare:

```go
if gotSHA256 != "literal-fixture-sha256" { t.Fatalf(...) }
if list.Total != 1 || list.Items[0].Name != "sample.pdf" { t.Fatalf(...) }
if !bytes.Equal(download.Body.Bytes(), pdfFixture) { t.Fatal("download changed bytes") }
```

Also verify another subject sees an empty list and receives `404 file_not_found` for the first user's file.

- [x] **Step 4: Run RED**

```powershell
go test . -run TestPDFUploadListDownloadIsUserIsolated -v
```

Expected: route returns 404 because file APIs are absent.

- [x] **Step 5: Implement SQLite schema and PDF upload**

Create the table:

```sql
CREATE TABLE IF NOT EXISTS files (
  id TEXT PRIMARY KEY,
  owner_subject TEXT NOT NULL,
  original_name TEXT NOT NULL,
  media_type TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  last_downloaded_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_files_owner_created
  ON files(owner_subject, created_at DESC, id DESC);
```

For 2A accept only `.pdf`, `application/pdf`, and `%PDF-` file signature. Enforce `PRP_MAX_FILE_SIZE_BYTES` while streaming. Sanitize the display name with `filepath.Base`; generate storage paths from server IDs, never from user names. Stream to `/data/tmp/{generated-id}.part`, hash while copying, `fsync`, atomically rename to `/data/files/{generated-id}.pdf`, then insert metadata.

- [x] **Step 6: Implement list and download**

Validate `page >= 1`, `1 <= page_size <= 50`. List only the authenticated subject. Download by `(id, owner_subject)`, set `Content-Disposition`, `Content-Type`, `Content-Length`, and `X-Content-SHA256`, and update `last_downloaded_at` only after `io.Copy` succeeds.

- [x] **Step 7: Implement exact CORS**

For upload routes, reflect only a configured exact Origin and handle `OPTIONS`. Allow `POST`, `OPTIONS`, `Authorization`, and `Content-Type`; never emit `Access-Control-Allow-Origin: *`.

- [x] **Step 8: Run GREEN**

```powershell
go test . -v
```

- [x] **Step 9: Commit Cloud repository**

```powershell
git add prp-demo
git commit -m "feat: add isolated PDF storage to PRP demo"
```

---

### Task 4: Add Site Portal browser session and direct-upload page

**Files:**
- Create: `site-portal/browser_session_store.go`
- Create: `site-portal/browser_session_store_test.go`
- Create: `site-portal/prp_client.go`
- Create: `site-portal/prp_client_test.go`
- Modify: `site-portal/main.go`
- Modify: `site-portal/server.go`
- Modify: `site-portal/server_test.go`
- Modify: `site-portal/config.example.env`

**Interfaces:**
- Consumes: PRP `POST /api/v1/upload-contexts`.
- Produces: HttpOnly Cookie `flyprint_site_portal_user`.
- Produces: `GET /files` upload page.
- Produces: `POST /api/files/upload-context`.
- Site Portal has no route that accepts multipart file bytes.

- [x] **Step 1: Write failing browser-session tests**

Test that a session stores `ExternalUserID`, `DisplayName`, `PRPBaseURL`, `AccessToken`, `AccessTokenExpiresAt`, and session expiry in process memory, while the cookie value is only a random lookup key.

```go
func TestLoginCallbackSetsOpaqueBrowserSessionCookie(t *testing.T)
func TestBrowserSessionExpiryRemovesPRPCredential(t *testing.T)
```

- [x] **Step 2: Run RED**

```powershell
go test . -run 'Test(LoginCallbackSetsOpaque|BrowserSessionExpiry)' -v
```

Expected: failure because no user browser session exists.

- [x] **Step 3: Implement browser sessions**

On successful identity callback, create the browser session independently from the Edge claim. Set `HttpOnly`, `SameSite=Lax`, `Path=/`, and conditional `Secure`. Do not delete it when the Edge redeems its claim. After Cloud accepts the login completion, redirect the browser to `/files`; the Edge notification remains unchanged.

- [x] **Step 4: Write failing PRP boundary test**

Use an HTTP test server that records the request. Assert:

```go
if request.Header.Get("Authorization") != "Bearer private-prp-token" { t.Fatal(...) }
if strings.Contains(request.URL.String(), "private-prp-token") { t.Fatal(...) }
```

Return a complete upload-context response and assert Site Portal forwards only `upload_context`, `expires_at`, and the configured public `upload_url`.

- [x] **Step 5: Implement PRP client and routes**

Add configuration:

```text
SITE_PORTAL_UPLOAD_ENABLED
SITE_PORTAL_PRP_API_BASE_URL
SITE_PORTAL_USER_SESSION_TTL
```

`POST /api/files/upload-context` requires a valid browser session, checks the PRP Token has not expired, calls the internal PRP API, and returns no access token.

`GET /files` renders the logged-in display name, one PDF file input, progress/result text, and browser JavaScript that:

1. obtains an upload context from Site Portal;
2. sends `multipart/form-data` directly to the returned PRP URL;
3. never stores either credential in local or session storage.

When upload is disabled, both routes return 404.

- [x] **Step 6: Prove Site Portal cannot receive files**

Add a handler test that posts multipart bytes to `/api/v1/files` and expects 404. Assert no Site Portal test fake receives the file payload.

- [x] **Step 7: Run GREEN**

```powershell
go test . -v
```

- [x] **Step 8: Commit Cloud repository**

```powershell
git add site-portal
git commit -m "feat: add direct PRP upload page"
```

---

### Task 5: Compose the official PRP Demo

**Files:**
- Modify: `docker-compose.yml`
- Modify: `.env.example`
- Modify: `docs/部署与验证.md`

**Interfaces:**
- Produces: `prp-demo` container on configurable public port `8083`.
- Produces: named volumes for SQLite and PRP files.
- Internal Site Portal URL uses `http://prp-demo:8080`; browser and Edge use `PRP_PUBLIC_BASE_URL`.

- [x] **Step 1: Add PRP service and health dependency**

Build from `prp-demo/`, mount one `/data` volume, expose only the configured public demo port, and add a health check. Configure identical non-secret placeholder values for SSO signer and PRP verifier through environment interpolation; `.env.example` must require operators to replace them.

- [x] **Step 2: Validate Compose**

```powershell
docker compose -p fly-print-cloud config --quiet
docker compose -p fly-print-cloud up -d --build sso-login-demo prp-demo site-portal
docker compose -p fly-print-cloud ps sso-login-demo prp-demo site-portal
```

Expected: all three services become healthy.

- [x] **Step 3: Run live HTTP PDF upload**

Create a named Slice 2 Demo user through `/ops`, complete an SSO code exchange, create an upload context, upload a generated non-sensitive one-page PDF directly to port 8083, then list and download it. Keep this account and file for Task 9 browser verification; both remain bounded by the configured Demo TTL. Print only status codes, file IDs, sizes, and hashes; never print credentials.

- [x] **Step 4: Commit Cloud repository**

```powershell
git add docker-compose.yml .env.example docs/部署与验证.md
git commit -m "feat: compose official PRP demo"
```

---

### Task 6: Add strict Edge PRP client

**Files:**
- Create: `prp_client.py`
- Create: `tests/test_prp_client.py`
- Modify: `edge_limits.py`

**Interfaces:**
- Produces: `PRPClient.list_files(access_context, page, page_size) -> dict`.
- Produces: `PRPClient.download_file(access_context, file_id, destination) -> dict`.
- `access_context` is returned only by `PortalSessionManager.get_access_context(session_id)`.

- [x] **Step 1: Write failing client tests**

Use a real local HTTP test server. Verify:

```python
def test_list_uses_authorization_header_and_never_query_token(self): ...
def test_list_rejects_invalid_pagination_shape(self): ...
def test_download_rejects_wrong_length_and_removes_partial_file(self): ...
def test_download_rejects_wrong_sha256_and_removes_partial_file(self): ...
def test_base_url_rejects_userinfo_query_and_fragment(self): ...
```

Expected values must use literal JSON and literal SHA-256 fixtures.

- [x] **Step 2: Run RED**

```powershell
py -m unittest tests.test_prp_client -v
```

Expected: import failure because `prp_client.py` does not exist.

- [x] **Step 3: Implement client**

Use a bounded `requests.Session`, explicit connect/read timeouts, `stream=True`, fixed client-constructed paths, and `Authorization: Bearer`. Write to a sibling `.part`, enforce Edge's configured maximum while streaming, compare `Content-Length` and `X-Content-SHA256`, then `os.replace` into the destination.

- [x] **Step 4: Run GREEN**

```powershell
py -m unittest tests.test_prp_client -v
```

- [x] **Step 5: Commit Edge repository**

```powershell
git add prp_client.py edge_limits.py tests/test_prp_client.py
git commit -m "feat: add strict PRP client"
```

---

### Task 7: Bind a downloaded PRP PDF to the Edge preview pipeline

**Files:**
- Create: `prp_file_selection.py`
- Create: `tests/test_prp_file_selection.py`
- Modify: `interactive_session.py`
- Modify: `portal_session.py`
- Modify: `main.py`
- Modify: `tests/test_interactive_session.py`
- Modify: `tests/test_user_session_snapshot_api.py`
- Modify: `tests/test_user_preview_print_api.py`

**Interfaces:**
- Produces: `PRPFileSelectionManager` owning session-scoped downloaded source paths.
- Produces: `InteractiveSessionManager.bind_prp_file(session_id, metadata)`.
- Produces: `GET /api/prp/files`.
- Produces: `POST /api/prp/files/{file_id}/select`.
- Existing `POST /api/preview` accepts a PRP-bound file without a Cloud `file_url`.

- [x] **Step 1: Write failing selection-manager tests**

Cover:

```python
def test_bind_replaces_and_deletes_previous_unconfirmed_source(self): ...
def test_clear_session_deletes_source_and_empty_directory(self): ...
def test_public_snapshot_never_exposes_local_path_or_access_token(self): ...
```

- [x] **Step 2: Run RED**

```powershell
py -m unittest tests.test_prp_file_selection -v
```

Expected: import failure because the manager is absent.

- [x] **Step 3: Implement session-bound selection store**

Store source files under the existing portable temp root in a per-session directory. Keep local paths only in `PRPFileSelectionManager`; interactive and public snapshots store:

```text
source_origin=prp
file_id
file_name
file_type
content_hash
size
```

- [x] **Step 4: Write failing endpoint tests**

With real `PortalSessionManager` and faked network boundary below `PRPClient`, verify:

```python
def test_list_requires_current_portal_session(self): ...
def test_select_downloads_and_binds_only_current_session(self): ...
def test_select_failure_keeps_identity_ready_and_deletes_partial_source(self): ...
def test_preview_uses_bound_local_source_without_cloud_download(self): ...
def test_print_rejects_prp_source_before_cloud_or_ipp(self): ...
```

- [x] **Step 5: Implement thin endpoints and preview source selection**

`GET /api/prp/files` requires `session_id`, obtains the private access context, and returns only public metadata.

`POST /api/prp/files/{file_id}/select` downloads and validates the file, binds the selection, changes the interactive state to `preview_ready`, and returns public file state.

In `/api/preview`, choose the source supplier from the interactive binding:

- PRP source: take the session-bound local path;
- existing Cloud/integration source: retain `_download_preview_file`.

This is an explicit source type branch, not a retry or fallback. After canonical PDF creation, release the consumed source-path record.

In `/api/print`, reject `source_origin=prp` before Cloud submission or IPP. Return `print_not_available_in_slice` so the PDF checkpoint cannot accidentally print.

- [x] **Step 6: Run GREEN and Edge regression**

```powershell
py -m unittest tests.test_prp_file_selection tests.test_interactive_session tests.test_user_session_snapshot_api tests.test_user_preview_print_api -v
```

- [x] **Step 7: Commit Edge repository**

```powershell
git add prp_file_selection.py interactive_session.py portal_session.py main.py tests
git commit -m "feat: bind PRP files to local preview"
```

---

### Task 8: Add the Edge PRP file-list view

**Files:**
- Create: `static/user/modules/views/files-view.js`
- Create: `static/user/modules/app/prp-files.js`
- Create: `static/user/css/files.css`
- Create: `tests/js/prp-files.test.mjs`
- Modify: `static/user/Index.html`
- Modify: `static/user/modules/shared/api.js`
- Modify: `static/user/modules/app/app-controller.js`
- Modify: `static/user/modules/shared/session-state.js`
- Modify: `static/user/modules/views/preview-view.js`

**Interfaces:**
- Produces: `normalizePRPFilePage(payload)`.
- Produces: files view with greeting, pagination, loading, empty, error, and selection states.
- Selecting one item calls the Edge select endpoint and routes to existing preview.

- [x] **Step 1: Write failing pure-state tests**

```javascript
test("normalizes a valid literal PRP page", () => {
  const page = normalizePRPFilePage({
    items: [{id:"file-1",name:"sample.pdf",media_type:"application/pdf",size:12,
      sha256:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      created_at:"2026-07-30T12:00:00Z",expires_at:"2026-08-06T12:00:00Z",
      last_downloaded_at:null}],
    page:1,page_size:20,total:1
  });
  assert.equal(page.items[0].name, "sample.pdf");
});
```

Also reject credentials, malformed hashes, invalid pagination, and items without IDs.

- [x] **Step 2: Run RED**

```powershell
node --experimental-default-type=module --test tests/js/prp-files.test.mjs
```

Expected: module-not-found failure.

- [x] **Step 3: Implement state and view**

Register the `files` route. After `portal_session_ready` and an `identity_ready` snapshot, route to files. The view fetches page 1, shows the public display name, and exposes one select action per file. Do not render raw HTML from file names; use `textContent`.

- [x] **Step 4: Connect selection to preview**

After a successful select response, set:

```javascript
session.file = {
  file_id, file_name, file_type, content_hash,
  source_origin: "prp", file_url: null,
  page_count: 1, page_index: 0, print_options: {}
};
```

Update preview preconditions so PRP files require `source_origin === "prp"` instead of `file_url`. Existing Cloud/integration preview still requires `file_url`. For PRP files, hide or disable the print-confirm action and display “打印将在下一切片开放”.

- [x] **Step 5: Run GREEN**

```powershell
node --experimental-default-type=module --test tests/js/identity-session.test.mjs tests/js/prp-files.test.mjs
py -m unittest discover -s tests -p 'test_*.py'
```

- [x] **Step 6: Commit Edge repository**

```powershell
git add static/user tests/js
git commit -m "feat: select PRP files on edge"
```

---

### Task 9: Verify the PDF vertical checkpoint and update execution state

**Files:**
- Modify: `docs/agent/site-portal-identity-protocol.md`
- Create: `docs/agent/prp-file-protocol.md`
- Modify: `docs/agent/architecture-and-protocols.md`
- Modify: `docs/部署与验证.md`
- Modify outside repositories: `D:/HQIT-LAPTOP/FlyPrint/FlyPrint开发任务清单.md`

**Interfaces:**
- Consumes: Tasks 1–8.
- Produces: repeatable PDF integration record and Slice 2 marked in progress.

- [x] **Step 1: Run complete automated verification**

Cloud modules:

```powershell
Set-Location api; go test ./...
Set-Location ../integration-demo; go test ./...
Set-Location ../sso-login-demo; go test ./...
Set-Location ../site-portal; go test ./...
Set-Location ../prp-demo; go test ./...
```

Edge:

```powershell
node --experimental-default-type=module --test tests/js/*.test.mjs
py -m unittest discover -s tests -p 'test_*.py'
```

- [ ] **Step 2: Run live PDF path**

Through the browser:

1. scan Edge QR;
2. log in;
3. open Site Portal upload page;
4. upload a generated non-sensitive one-page PDF directly to PRP;
5. verify the Edge file list shows it;
6. select it;
7. verify Edge renders the local preview;
8. stop before print confirmation.

- [ ] **Step 3: Check security boundaries**

Search Cloud, Site Portal, PRP, and Edge logs for the exact test token marker and PDF marker. Confirm:

- token marker appears nowhere in logs;
- PDF marker appears only in PRP storage and Edge temporary/canonical files;
- Cloud database and upload directories contain no new test file;
- frontend state-contract tests prove no PRP credential is written to browser session state.

- [x] **Step 4: Update current documentation**

Document the exact PRP contract and commands. In the root execution checklist:

- mark Slice 1 complete;
- replace obsolete enable/disable wording with create/delete/reset;
- mark Slice 2 in progress;
- leave Slice 2 completion unchecked until 2B types and governance pass.

- [x] **Step 5: Review each repository**

For Cloud and Edge:

```powershell
git status --short
git diff --check
git diff e653e75...HEAD   # Cloud
git diff 03458ce...HEAD   # Edge
```

Scan additions for private IPs, secrets, tokens, cookies, credentials, real user data, and environment-specific absolute paths.

- [x] **Step 6: Commit documentation**

Cloud:

```powershell
git add docs
git commit -m "docs: describe PRP PDF preview flow"
```

Do not push. Slice 2B planning starts only after this checkpoint passes.
