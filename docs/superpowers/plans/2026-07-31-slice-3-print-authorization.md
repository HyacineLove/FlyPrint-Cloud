# Slice 3 Print Authorization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the unified Cloud authorization, quota, audit, direct Edge IPP print, and durable result-settlement path for official and private Site Portal users.

**Architecture:** Edge synchronously authorizes one user confirmation over the existing node-bound OAuth2 HTTP channel. Cloud atomically validates the Cloud-owned session identity and printer context, reserves quota, and returns an audit job ID without dispatching a file command. Edge reuses the canonical PDF created during preview, executes the existing IPP service once, and extends the existing durable `job_update` outbox with actual usage fields that Cloud settles idempotently.

**Tech Stack:** Go 1.24, Gin, PostgreSQL/sqlmock, React/TypeScript/Ant Design/Jest, Python 3.12/FastAPI/unittest, IPP/2.0, SQLite outbox.

## Global Constraints

- Cloud never receives file bytes, file paths, PRP credentials, login cookies, or authenticated PRP URLs.
- Production printing uses only Edge IPP; Cloud does not send a `print_job` command for unified jobs.
- One newly mapped Site Portal user receives one initial grant of 50 quota points.
- Black-and-white sheets cost 1 point; color sheets cost 2 points.
- Simplex sheets are `pages × copies`; duplex sheets are `ceil(pages / 2) × copies`.
- Confirmed unused reservation is refunded; an unconfirmed result keeps its reservation.
- Admin may add only a positive quota amount. There is no daily reset, self-service grant, automatic refill, or negative adjustment endpoint.
- No fallback identity, file, authorization, or print path may be introduced.
- Every production behavior starts with a failing test and a verified RED result.

---

### Task 1: Cloud quota arithmetic, schema, and initial grant

**Files:**
- Create: `api/internal/business/print_quota.go`
- Create: `api/internal/business/print_quota_test.go`
- Create: `api/internal/database/print_authorization_schema.go`
- Create: `api/internal/database/migrations/005_unified_print_authorization.sql`
- Modify: `api/internal/database/database.go`
- Modify: `api/internal/models/models.go`
- Modify: `api/internal/database/external_identity_repository.go`
- Modify: `api/internal/database/external_identity_repository_test.go`

**Interfaces:**
- Produces: `business.QuotaUsage(pageCount, copies int, duplexMode, colorMode string) (sheets int, points int, error error)`.
- Produces: `business.SettledQuotaUsage(pageCount, copies, impressions int, duplexMode, colorMode string) (sheets int, points int, error error)`.
- Produces: `models.User.PrintQuotaBalance int`.
- Produces: compatible schema for `print_quota_transactions`, session identity columns, and unified audit columns.

- [ ] **Step 1: Write failing table-driven quota tests**

```go
func TestQuotaUsageCountsPhysicalSheetsAndColorMultiplier(t *testing.T) {
    tests := []struct {
        name string
        pages, copies int
        duplex, color string
        sheets, points int
    }{
        {"simplex mono", 3, 2, "simplex", "mono", 6, 6},
        {"duplex odd mono", 3, 2, "longedge", "mono", 4, 4},
        {"duplex odd color", 3, 2, "shortedge", "color", 4, 8},
    }
    for _, test := range tests {
        t.Run(test.name, func(t *testing.T) {
            sheets, points, err := QuotaUsage(
                test.pages, test.copies, test.duplex, test.color,
            )
            if err != nil {
                t.Fatalf("QuotaUsage() error = %v", err)
            }
            if sheets != test.sheets || points != test.points {
                t.Fatalf(
                    "QuotaUsage() = sheets %d, points %d; want sheets %d, points %d",
                    sheets, points, test.sheets, test.points,
                )
            }
        })
    }
}

func TestSettledQuotaUsageDoesNotShareOddSheetAcrossCopies(t *testing.T) {
    sheets, points, err := SettledQuotaUsage(3, 2, 4, "longedge", "mono")
    // One complete 3-page copy uses two sheets; one remaining impression uses one.
    if err != nil {
        t.Fatalf("SettledQuotaUsage() error = %v", err)
    }
    if sheets != 3 || points != 3 {
        t.Fatalf(
            "SettledQuotaUsage() = sheets %d, points %d; want sheets 3, points 3",
            sheets, points,
        )
    }
}
```

- [ ] **Step 2: Run quota tests and verify RED**

Run: `go test ./internal/business -run 'TestQuotaUsage|TestSettledQuotaUsage' -count=1`

Expected: FAIL because the quota functions do not exist.

- [ ] **Step 3: Implement strict quota arithmetic**

```go
func QuotaUsage(pageCount, copies int, duplexMode, colorMode string) (int, int, error) {
    if pageCount < 1 || copies < 1 { return 0, 0, ErrInvalidPrintQuotaInput }
    sheetsPerCopy := pageCount
    if duplexMode == "longedge" || duplexMode == "shortedge" {
        sheetsPerCopy = (pageCount + 1) / 2
    } else if duplexMode != "simplex" {
        return 0, 0, ErrInvalidPrintQuotaInput
    }
    multiplier := 1
    if colorMode == "color" { multiplier = 2 } else if colorMode != "mono" {
        return 0, 0, ErrInvalidPrintQuotaInput
    }
    sheets := sheetsPerCopy * copies
    return sheets, sheets * multiplier, nil
}
```

`SettledQuotaUsage` must reject impressions outside `0..pageCount*copies`, compute complete copies and the remaining partial copy, and then apply the same color multiplier.

- [ ] **Step 4: Run quota tests and verify GREEN**

Run: `go test ./internal/business -run 'TestQuotaUsage|TestSettledQuotaUsage' -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing repository expectations for one-time mapped-user grant**

Extend `external_identity_repository_test.go` so a new mapping expects:

```sql
INSERT INTO users
    (username,email,password_hash,role,status,last_login,print_quota_balance)
VALUES ($1,$2,$3,'viewer','active',$4,50)
RETURNING id
```

and in the same transaction:

```sql
INSERT INTO print_quota_transactions
    (user_id,transaction_type,delta,balance_after)
VALUES ($1,'initial_grant',50,50)
```

Existing mappings must not insert another initial grant.

- [ ] **Step 6: Run repository tests and verify RED**

Run: `go test ./internal/database -run TestExternalIdentityRepository -count=1`

Expected: FAIL because schema/model/grant writes do not exist.

- [ ] **Step 7: Add compatible schema and initial-grant write**

The migration and `initPrintAuthorizationSchema` must both:

- add `users.print_quota_balance INTEGER NOT NULL DEFAULT 0 CHECK (print_quota_balance >= 0)`;
- create `print_quota_transactions`;
- add `site_portal_code` and `cloud_user_id` to `edge_terminal_sessions`;
- add unified audit and quota settlement columns to `print_jobs`;
- add a unique partial index on `(edge_node_id, confirmation_id)` when `confirmation_id IS NOT NULL`;
- grant 50 once to existing users referenced by `external_identities`, with an `initial_grant` transaction;
- leave non-mapped Cloud accounts at zero.

Update `CompleteLogin` so the new mapped user and initial ledger row are created in the same transaction.

- [ ] **Step 8: Run database and business tests**

Run: `go test ./internal/business ./internal/database -count=1`

Expected: PASS.

- [ ] **Step 9: Commit Task 1**

```powershell
git add api/internal/business api/internal/database api/internal/models/models.go
git commit -m "feat: add print quota model and initial grant"
```

---

### Task 2: Cloud atomic authorization and idempotent audit creation

**Files:**
- Create: `api/internal/models/print_authorization.go`
- Create: `api/internal/database/print_authorization_repository.go`
- Create: `api/internal/database/print_authorization_repository_test.go`
- Create: `api/internal/handlers/portal_print_handler.go`
- Create: `api/internal/handlers/portal_print_handler_test.go`
- Modify: `api/cmd/server/main.go`
- Modify: `api/internal/database/external_identity_repository.go`
- Modify: `api/internal/database/terminal_session_repository.go`

**Interfaces:**
- Consumes: `business.QuotaUsage`.
- Produces: `PrintAuthorizationRepository.Authorize(input models.PrintAuthorizationInput) (*models.PrintAuthorizationResult, error)`.
- Produces: authenticated `POST /api/v1/edge/:node_id/print-authorizations`.

- [ ] **Step 1: Write failing repository tests for authorization branches**

Cover literal SQL expectations for:

- matching node/session/Site Portal/user;
- active user and sufficient balance;
- printer belonging to the node and dispatchable;
- atomic balance decrement, `authorization_reserve` ledger insert, and `print_jobs` insert with empty `file_path` and `file_url`;
- insufficient quota producing no job insert;
- inactive user producing no job insert;
- same `(node_id, confirmation_id)` and same request hash returning the existing job;
- same key with a different request hash returning `ErrAuthorizationConflict`.

The successful result must literally assert `ReservedQuota=8` and `QuotaBalance=42` for a 3-page, two-copy, duplex color request.

- [ ] **Step 2: Run repository tests and verify RED**

Run: `go test ./internal/database -run TestPrintAuthorizationRepository -count=1`

Expected: FAIL because the repository does not exist.

- [ ] **Step 3: Implement the transaction**

Within one `FOR UPDATE` transaction:

1. load current `edge_terminal_sessions` identity by node and session;
2. compare the bound Site Portal;
3. lock the active user row;
4. load and validate the owned printer and Edge state;
5. calculate reserved quota;
6. load an existing idempotency row before deducting;
7. reject insufficient balance;
8. decrement `users.print_quota_balance`;
9. insert `authorization_reserve`;
10. insert a `pending` audit task with `edge_node_id`, session, Site Portal, local file ID, display name, page count, copies, options, request hash, and reserved quota;
11. commit and return the task ID and remaining balance.

- [ ] **Step 4: Run repository tests and verify GREEN**

Run: `go test ./internal/database -run TestPrintAuthorizationRepository -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing handler contract tests**

Use Gin and a repository interface fake to assert:

- malformed input returns `400 invalid_print_authorization`;
- repository quota denial returns `409 print_quota_insufficient`;
- session mismatch returns `409 terminal_session_invalid`;
- success returns `200` with `allowed`, `job_id`, `reserved_quota`, and `quota_balance`;
- denial responses do not expose a job ID.

- [ ] **Step 6: Run handler tests and verify RED**

Run: `go test ./internal/handlers -run TestPortalPrintHandler -count=1`

Expected: FAIL because the handler does not exist.

- [ ] **Step 7: Implement handler, route, and login-session binding**

Register:

```go
edgeGroup.POST(
    "/:node_id/print-authorizations",
    middleware.OAuth2ResourceServer("edge:printer"),
    middleware.EdgeNodeIdentityMatch(),
    middleware.EdgeNodeEnabledCheck(edgeNodeRepo),
    portalPrintHandler.Authorize,
)
```

When Site Portal login completes, update the matching current terminal session with `site_portal_code` and `cloud_user_id` before committing. Do not accept either identity field from the authorization request as authoritative.

- [ ] **Step 8: Run Cloud API tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 9: Commit Task 2**

```powershell
git add api
git commit -m "feat: authorize unified print confirmations"
```

---

### Task 3: Cloud terminal usage settlement and monotonic receipt

**Files:**
- Modify: `api/internal/websocket/message.go`
- Modify: `api/internal/websocket/file_transport_test.go`
- Modify: `api/internal/websocket/connection.go`
- Modify: `api/internal/operations/status_service.go`
- Modify: `api/internal/operations/status_service_test.go`

**Interfaces:**
- Extends `JobUpdateData` and `operations.TerminalJobUpdate` with `ImpressionsCompleted`, `SheetsCompleted`, and `QuotaConsumed`.
- Produces one atomic terminal transition, quota refund, quota ledger write, receipt write, and ACK decision.

- [ ] **Step 1: Write failing serialization and settlement tests**

Add protocol tests that deserialize:

```json
{
  "event_id": "11111111-1111-1111-1111-111111111111",
  "job_id": "22222222-2222-2222-2222-222222222222",
  "status": "failed",
  "impressions_completed": 4,
  "sheets_completed": 3,
  "quota_consumed": 6
}
```

Add status-service tests proving:

- reserved 8, consumed 6 refunds 2 exactly once;
- pre-submit failure consumed 0 refunds all;
- `unconfirmed` keeps all 8 reserved;
- a duplicate event ID does not refund again;
- a conflicting terminal event is rejected;
- reported consumption above reservation is rejected.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/websocket ./internal/operations -run 'JobUpdate|Quota|Terminal' -count=1`

Expected: FAIL because the usage fields and settlement do not exist.

- [ ] **Step 3: Implement atomic settlement**

For unified jobs only, lock the job and user rows. Validate that Edge-reported sheets and quota agree with the stored page count, copies, duplex, and color rules. For `completed`, require full expected consumption. For explicit `failed` or `canceled`, accept `0..reserved`. For `unconfirmed`, ignore consumption fields and retain the reservation.

When `refund = reserved - consumed` is positive:

```sql
UPDATE users SET print_quota_balance=print_quota_balance+$2 WHERE id=$1;
INSERT INTO print_quota_transactions
    (user_id,print_job_id,transaction_type,delta,balance_after)
VALUES ($1,$2,'print_refund',$3,$4);
```

Write the terminal receipt in the same transaction before returning an accepted result.

- [ ] **Step 4: Run focused and full Cloud tests**

Run:

```powershell
go test ./internal/websocket ./internal/operations -count=1
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```powershell
git add api/internal/websocket api/internal/operations
git commit -m "feat: settle quota from edge print results"
```

---

### Task 4: Edge authorization client and physical-sheet accounting

**Files (fly-print-edge worktree):**
- Create: `print_authorization_client.py`
- Create: `print_quota.py`
- Create: `tests/test_print_authorization_client.py`
- Create: `tests/test_print_quota.py`
- Modify: `cloud_service.py`

**Interfaces:**
- Produces: `PrintAuthorizationClient.authorize(payload: dict) -> dict`.
- Produces: `quota_usage(page_count, copies, duplex_mode, color_mode, impressions_completed=None) -> dict`.
- Consumes the existing `CloudAuthClient.get_auth_headers()`.

- [ ] **Step 1: Write failing quota and HTTP contract tests**

Literal cases must match Cloud:

```python
self.assertEqual(
    {"sheets": 4, "points": 8},
    quota_usage(3, 2, "longedge", "color"),
)
self.assertEqual(
    {"sheets": 3, "points": 6},
    quota_usage(3, 2, "longedge", "color", impressions_completed=4),
)
```

HTTP tests must assert the exact URL, node OAuth header, timeout, success response, stable denial code, and that credentials are never logged or copied into the body.

- [ ] **Step 2: Run tests and verify RED**

Run:

```powershell
& 'D:\HQIT-LAPTOP\FlyPrint\fly-print-edge\.venv\Scripts\python.exe' -m unittest tests.test_print_quota tests.test_print_authorization_client
```

Expected: FAIL because both modules do not exist.

- [ ] **Step 3: Implement strict client and matching arithmetic**

The client sends one POST to `/api/v1/edge/{node_id}/print-authorizations` and returns the Cloud response without changing its authorization decision. It may repeat only the same `confirmation_id`; it must never manufacture a new ID after an ambiguous response.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run the Step 2 command.

Expected: PASS.

- [ ] **Step 5: Commit Task 4**

```powershell
git add print_authorization_client.py print_quota.py cloud_service.py tests
git commit -m "feat: add edge print authorization client"
```

---

### Task 5: Edge direct canonical-PDF IPP orchestration

**Files (fly-print-edge worktree):**
- Create: `portal_print_service.py`
- Create: `tests/test_portal_print_service.py`
- Modify: `interactive_session.py`
- Modify: `print_runtime.py`
- Modify: `printing/domain.py`
- Modify: `printing/service.py`
- Modify: `cloud_websocket_client.py`
- Modify: `job_delivery_store.py`
- Modify: `main.py`
- Modify: `tests/test_user_preview_print_api.py`
- Modify: `tests/test_ipp_service.py`
- Modify: `tests/test_job_inbox.py`

**Interfaces:**
- Produces: `PortalPrintService.submit(session_snapshot, printer, options)`.
- Extends `PrintEvent` with `impressions_completed`.
- Extends durable terminal outbox payloads with actual usage fields.

- [ ] **Step 1: Write failing orchestration tests**

Cover:

- authorization denial leaves the session in a result state and never calls `IppPrintService.execute`;
- an allowed response binds exactly one Cloud job ID and executes IPP once;
- the print request resolves the preview canonical cache by `content_hash` and never invokes PRP download;
- a second click with the same session/file does not authorize or print again;
- a pre-submit IPP failure reports zero consumption;
- partial explicit failure reports the last IPP impression count and derived sheets/points;
- unconfirmed result reports no consumption and never retries IPP.

- [ ] **Step 2: Run orchestration tests and verify RED**

Run:

```powershell
& 'D:\HQIT-LAPTOP\FlyPrint\fly-print-edge\.venv\Scripts\python.exe' -m unittest tests.test_portal_print_service tests.test_user_preview_print_api
```

Expected: FAIL because PRP printing is currently rejected and the service does not exist.

- [ ] **Step 3: Implement the smallest direct-print service**

The service must:

1. take the already-bound session identity and file metadata;
2. calculate and send the authorization request;
3. bind the returned `job_id`;
4. construct `PrintRequest` with `content_hash`, `source_name`, `source_kind`, and a canonical-cache supplier that must hit the existing cache;
5. execute IPP in a worker thread;
6. forward progress locally;
7. persist one terminal outbox report with quota fields.

Remove only the `print_not_available_in_slice` rejection. Do not change legacy non-PRP dispatch behavior in this slice.

- [ ] **Step 4: Add failing IPP impression propagation tests**

Assert the service retains the last `job-impressions-completed` value for failed/canceled events and emits the full expected impression count for completed events.

- [ ] **Step 5: Implement impression propagation**

Add `impressions_completed` to `PrintEvent`, update it on each IPP poll, and include it in terminal callback events. No printer job query is repeated after a terminal or unconfirmed result.

- [ ] **Step 6: Extend outbox payload and tests**

Persist:

```json
{
  "impressions_completed": 4,
  "sheets_completed": 3,
  "quota_consumed": 6
}
```

for explicit results. Omit all three usage fields for `unconfirmed`.

- [ ] **Step 7: Run focused and full Edge tests**

Run:

```powershell
& 'D:\HQIT-LAPTOP\FlyPrint\fly-print-edge\.venv\Scripts\python.exe' -m unittest tests.test_portal_print_service tests.test_user_preview_print_api tests.test_ipp_service tests.test_job_inbox
& 'D:\HQIT-LAPTOP\FlyPrint\fly-print-edge\.venv\Scripts\python.exe' -m unittest discover -s tests -p 'test_*.py'
node --test tests/js/*.mjs tests_js/*.mjs
```

Expected: PASS.

- [ ] **Step 8: Commit Task 5**

```powershell
git add .
git commit -m "feat: print authorized portal files over ipp"
```

---

### Task 6: Cloud Admin quota grant and unified audit view

**Files (fly-print-cloud worktree):**
- Create: `api/internal/database/print_quota_repository.go`
- Create: `api/internal/database/print_quota_repository_test.go`
- Modify: `api/internal/handlers/user_handler.go`
- Modify: `api/internal/handlers/user_handler_test.go`
- Modify: `api/cmd/server/main.go`
- Modify: `admin/src/components/pages/Users.tsx`
- Modify: `admin/src/components/pages/Users.test.tsx`
- Modify: `admin/src/components/pages/PrintJobs.tsx`
- Modify: `admin/src/components/pages/PrintJobs.test.tsx`

**Interfaces:**
- Produces: `POST /api/v1/admin/users/:id/print-quota-grants` with `{ "amount": positive integer, "reason": string }`.
- Extends user JSON with `print_quota_balance`.
- Extends print-job list JSON with unified audit fields.

- [ ] **Step 1: Write failing repository and handler tests**

Assert:

- amount `0` and negative values are rejected;
- a positive amount atomically updates the balance and inserts `admin_grant`;
- the authenticated admin ID and reason are recorded;
- missing user returns not found;
- the user response includes the new balance.

- [ ] **Step 2: Run Go tests and verify RED**

Run: `go test ./internal/database ./internal/handlers -run 'PrintQuota|QuotaGrant' -count=1`

Expected: FAIL because the grant path does not exist.

- [ ] **Step 3: Implement the Admin-only grant path**

Register the route inside the existing `fly-print-admin` user-management group. Do not add PUT balance, decrement, reset, or public endpoints.

- [ ] **Step 4: Write failing React behavior tests**

Users tests must assert balance display, positive-number validation, POST payload, refreshed balance, and no decrement/reset control.

PrintJobs tests must assert unified rows display Site Portal, user, Edge, printer, filename, pages, copies, print options, reserved/consumed quota, status, and error without requiring Cloud file/provider/callback fields.

- [ ] **Step 5: Run React tests and verify RED**

Run:

```powershell
npm.cmd test -- --watchAll=false --runInBand
```

Expected: FAIL on the new UI behavior.

- [ ] **Step 6: Implement the two Admin views**

Use an Ant Design modal for positive quota grants and a read-only unified audit table. Preserve existing filters that still apply; remove only UI dependencies on file/provider/callback fields for the unified view.

- [ ] **Step 7: Run focused and full Cloud verification**

Run:

```powershell
Set-Location api
go test ./...
Set-Location ../admin
npm.cmd test -- --watchAll=false --runInBand
npm.cmd run build
```

Expected: PASS.

- [ ] **Step 8: Commit Task 6**

```powershell
git add api admin
git commit -m "feat: manage quota and unified print audits"
```

---

### Task 7: Cross-repository protocol regression and Demo acceptance

**Files:**
- Modify: `fly-print-cloud/docs/agent/architecture-and-protocols.md`
- Modify: `fly-print-edge/docs/agent/architecture-and-protocols.md`
- Do not mark root slice 3 `[x]` until merge and required real-print acceptance are complete.

**Interfaces:**
- Verifies the same authorization and settlement contract in both repositories.

- [ ] **Step 1: Run all Cloud Go modules and Compose validation**

```powershell
Set-Location fly-print-cloud/api
go test ./...
Set-Location ../integration-demo
go test ./...
Set-Location ../prp-demo
go test ./...
Set-Location ../site-portal
go test ./...
Set-Location ../sso-login-demo
go test ./...
Set-Location ..
docker compose config --quiet
```

- [ ] **Step 2: Run Cloud Admin tests and build**

```powershell
Set-Location fly-print-cloud/admin
npm.cmd test -- --watchAll=false --runInBand
npm.cmd run build
```

- [ ] **Step 3: Run complete Edge regression**

```powershell
Set-Location fly-print-edge
& 'D:\HQIT-LAPTOP\FlyPrint\fly-print-edge\.venv\Scripts\python.exe' -m unittest discover -s tests -p 'test_*.py'
node --test tests/js/*.mjs tests_js/*.mjs
```

- [ ] **Step 4: Update the integrated Cloud stack**

Run: `docker compose up --build -d`

Then run: `docker compose ps`

Expected: all required slice-3 services healthy or running.

- [ ] **Step 5: Perform real acceptance**

Verify in order:

1. a mapped user starts at 50 points;
2. official black-and-white simplex print reserves and settles the expected points;
3. private-domain duplex color print uses `ceil(pages/2) × copies × 2`;
4. explicit pre-submit failure refunds all reserved points;
5. quota denial, disabled user, and repeated confirm produce no new printer job;
6. Cloud Admin shows one audit row and the correct quota balance/ledger effect;
7. no Cloud table, directory, response, or log contains file bytes, PRP credentials, Cookie, or authenticated PRP URLs.

- [ ] **Step 6: Review both diffs and repository status**

```powershell
git -C fly-print-cloud status --short
git -C fly-print-cloud diff --check
git -C fly-print-edge status --short
git -C fly-print-edge diff --check
```

Expected: only intentional slice-3 changes and no whitespace errors.

- [ ] **Step 7: Commit any protocol-document corrections**

```powershell
git add docs
git commit -m "docs: record unified print authorization protocol"
```
