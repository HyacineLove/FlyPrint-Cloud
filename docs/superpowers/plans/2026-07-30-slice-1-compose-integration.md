# Slice 1 Compose Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the existing Cloud Compose start the Slice 1 SSO Login Demo and Site Portal so a host-running Edge and a mobile browser can perform the real identity loop.

**Architecture:** Extend the existing `docker-compose.yml` directly. Containers use Compose service names for server-to-server calls, while configurable external base URLs are used in browser redirects and Edge claim messages.

**Tech Stack:** Docker Compose, Go 1.25 services, PostgreSQL, FlyPrint Cloud API, Windows-hosted FlyPrint Edge.

## Global Constraints

- Do not create a second Compose file or a fallback login path.
- Do not add upload, PRP file access, preview, or printing behavior.
- Do not store real IP addresses or credentials in the repository.
- Keep the HMAC third-party printing path unchanged.
- The default external URLs are for same-host browser testing only; LAN scanning must use explicitly configured reachable URLs.

---

### Task 1: Add Slice 1 services to the existing Compose

**Files:**
- Modify: `docker-compose.yml`
- Modify: `.env.example`

**Interfaces:**
- Consumes: the existing `api`, PostgreSQL, Redis, MinIO, and nginx services.
- Produces: Compose services `sso-login-demo` and `site-portal`, plus Cloud `site_portal_bootstrap` environment.

- [x] **Step 1: Verify the current Compose lacks the Slice 1 services**

Run:

```powershell
docker compose config --services
```

Expected: output does not contain `sso-login-demo` or `site-portal`.

- [x] **Step 2: Add the SSO Login Demo service**

Add a service built from `./sso-login-demo`, publish `${SSO_DEMO_PORT:-8081}:8080`, mount `sso_login_demo_data:/data`, configure the data file, initial operator, client secret, redirect allowlist, and `/health` healthcheck.

- [x] **Step 3: Add the Site Portal service**

Add a service built from `./site-portal`, publish `${SITE_PORTAL_PORT:-8082}:8080`, call Cloud through `http://api:8080`, call the identity API through `http://sso-login-demo:8080`, use external browser/callback/claim URLs from `.env`, and wait for `api` plus healthy `sso-login-demo`.

- [x] **Step 4: Bootstrap the Site Portal in Cloud**

Add these `api.environment` values:

```yaml
- FLY_PRINT_SITE_PORTAL_BOOTSTRAP_CODE=${SITE_PORTAL_CODE:-official}
- FLY_PRINT_SITE_PORTAL_BOOTSTRAP_DISPLAY_NAME=${SITE_PORTAL_DISPLAY_NAME:-FlyPrint}
- FLY_PRINT_SITE_PORTAL_BOOTSTRAP_ENTRY_URL=${SITE_PORTAL_PUBLIC_BASE_URL:-http://localhost:8082}/entry
- FLY_PRINT_SITE_PORTAL_BOOTSTRAP_CLAIM_BASE_URL=${SITE_PORTAL_PUBLIC_BASE_URL:-http://localhost:8082}
- FLY_PRINT_SITE_PORTAL_BOOTSTRAP_API_TOKEN=${SITE_PORTAL_API_TOKEN:-change-this-site-portal-token-32chars}
```

- [x] **Step 5: Add non-secret template variables and the SSO data volume**

Document `SITE_PORTAL_PUBLIC_BASE_URL`, `SSO_DEMO_PUBLIC_BASE_URL`, ports, operator username/password, Site Portal API token, identity client secret, and add `sso_login_demo_data`.

- [x] **Step 6: Verify the rendered Compose**

Run:

```powershell
docker compose config --quiet
docker compose config --services
```

Expected: exit zero; services include `sso-login-demo` and `site-portal`.

- [x] **Step 7: Commit**

```powershell
git add docker-compose.yml .env.example
git commit -m "feat: compose slice one identity services"
```

### Task 2: Document and run the real Slice 1 check

**Files:**
- Modify: `docs/部署与验证.md`
- Modify: `docs/agent/operations-and-verification.md`
- Modify: `docs/superpowers/plans/2026-07-30-slice-1-compose-integration.md`

**Interfaces:**
- Consumes: the Compose services from Task 1 and the Edge feature worktree.
- Produces: one operator-facing startup and verification sequence.

- [x] **Step 1: Add the exact LAN configuration rule**

Document that `EXTERNAL_API_URL`, `SITE_PORTAL_PUBLIC_BASE_URL`, and `SSO_DEMO_PUBLIC_BASE_URL` must use addresses reachable by the phone and Edge; `localhost` is valid only for a same-host browser.

- [x] **Step 2: Add the manual verification sequence**

Document:

1. `docker compose up --build -d`
2. `docker compose ps`
3. open `${SITE_PORTAL_PUBLIC_BASE_URL}/ops`
4. create an enabled user
5. start Edge with `cloud.base_url=${EXTERNAL_API_URL}`
6. scan, log in, and verify the Edge identity
7. repeat login and verify the Cloud user is reused
8. disable the user and verify login is rejected.

- [x] **Step 3: Run full component verification**

Run:

```powershell
docker compose config --quiet
go test ./...
```

Run the Go tests from `api`, `site-portal`, and `sso-login-demo`; run the full Edge unittest suite from the Edge worktree.

- [x] **Step 4: Build and start Compose**

Run:

```powershell
docker compose up --build -d
docker compose ps
```

Expected: `api`, `sso-login-demo`, `site-portal`, and nginx are running; healthchecked services become healthy.

- [x] **Step 5: Check health and logs**

Run:

```powershell
Invoke-WebRequest http://127.0.0.1:8012/api/v1/health -UseBasicParsing
Invoke-WebRequest http://127.0.0.1:8081/health -UseBasicParsing
Invoke-WebRequest http://127.0.0.1:8082/health -UseBasicParsing
docker compose logs --tail 100 api sso-login-demo site-portal
```

Expected: all health requests return 200 and logs contain no configuration or startup errors.

- [x] **Step 6: Scan the complete branch diff**

Inspect `main...HEAD` for real IP addresses, passwords, API tokens, cookies, and user data. Only loopback addresses, `example.test`, placeholders, and test fixtures are permitted.

- [x] **Step 7: Commit**

```powershell
git add docs/部署与验证.md docs/agent/operations-and-verification.md docs/superpowers/plans/2026-07-30-slice-1-compose-integration.md
git commit -m "docs: add slice one integration check"
```
