# Official Site Portal Thin User Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace account enable/disable management with hard deletion while retaining list, create, and password reset.

**Architecture:** SSO Login Demo remains the owner of account records and exposes a hard-delete operation. Site Portal proxies that operation and presents only create, delete, and password-reset controls; Cloud mappings remain outside this lifecycle.

**Tech Stack:** Go, `net/http`, JSON persistence, Go `testing`, Docker Compose

## Global Constraints

- Account deletion is permanent in SSO Login Demo and does not delete Cloud mappings.
- No account edit, enable, or disable capability remains.
- Responses must not expose passwords, password hashes, or login credentials.

---

### Task 1: SSO Login Demo hard deletion

**Files:**
- Modify: `sso-login-demo/store.go`
- Modify: `sso-login-demo/server.go`
- Test: `sso-login-demo/server_test.go`

**Interfaces:**
- Consumes: authenticated operator session and user ID path parameter.
- Produces: `DELETE /api/ops/users/{id}` returning `{"success":true}` or `404 {"error":"user_not_found"}`.

- [ ] **Step 1: Write the failing test**

Add a test that creates a real user, deletes it through the HTTP handler, verifies the delete response contains no sensitive fields, and verifies a later authorization attempt for that account returns unauthorized.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sso-login-demo -run TestOpsDeleteRemovesUserAndPreventsLogin -v`

Expected: FAIL because `DELETE /api/ops/users/{id}` is not registered.

- [ ] **Step 3: Write minimal implementation**

Add `deleteUser(id string) error` to `identityStore`, register `DELETE /api/ops/users/{id}`, and implement an operator-authenticated handler. Remove the enable/disable route, handler, store method, and `Enabled` persistence/public fields. Newly created users remain immediately usable.

- [ ] **Step 4: Run focused tests**

Run: `go test ./sso-login-demo -v`

Expected: PASS.

### Task 2: Site Portal proxy and thin UI

**Files:**
- Modify: `site-portal/server.go`
- Test: `site-portal/server_test.go`

**Interfaces:**
- Consumes: SSO Login Demo delete endpoint from Task 1.
- Produces: proxied `DELETE /api/ops/users/{id}` and UI controls for delete and reset password only.

- [ ] **Step 1: Write the failing test**

Add a portal handler test whose real portal router receives `DELETE /api/ops/users/user-1` and whose identity-boundary fake records the forwarded method and path. Assert the returned status and body.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./site-portal -run TestOpsDeleteUserProxiesToIdentityService -v`

Expected: FAIL because the portal delete route is not registered.

- [ ] **Step 3: Write minimal implementation**

Replace the enable/disable proxy route with `DELETE /api/ops/users/{id}`. Update the user card to show username and display name with only “删除账户” and “重置密码”; require browser confirmation before deletion and refresh the list after success.

- [ ] **Step 4: Run focused tests**

Run: `go test ./site-portal -v`

Expected: PASS.

### Task 3: Slice regression and delivery review

**Files:**
- Modify if required: `docs/superpowers/plans/2026-07-30-thin-official-user-management.md`

**Interfaces:**
- Consumes: Tasks 1 and 2.
- Produces: tested, reviewable Cloud repository commit.

- [ ] **Step 1: Run all Go tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Rebuild Demo services**

Run: `docker compose up -d --build sso-login-demo site-portal`

Expected: both containers become healthy.

- [ ] **Step 3: Verify live behavior**

Create a temporary account from Site Portal, log in once, delete it, and confirm the same credentials no longer authorize. Confirm the Edge identity-ready page and reset-to-QR action still work.

- [ ] **Step 4: Review**

Run `git diff --check`, inspect the complete branch diff, and scan changed files for IP addresses, credentials, tokens, private keys, and environment-specific paths.

- [ ] **Step 5: Commit**

Commit the tested Cloud changes and the pending Edge identity-ready changes as separate repository commits. Do not push in this step.
