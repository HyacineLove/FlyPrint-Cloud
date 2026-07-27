# User Management Operations Implementation Plan

> For agentic workers: REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复用户停用错误，并实现邮箱不可变、用户名可编辑、停用/恢复、受保护的用户删除、打印人信息展示以及用户管理与打印任务互跳。

**Architecture:** 保留现有 users.id 作为数据库内部主键，邮箱作为不可变业务用户标识和页面互跳条件。后端在 UserRepository 中实现用户筛选、Toggle 和带活动任务检查的删除事务；Admin 页面调用这些接口，打印任务查询通过 users.id::text = print_jobs.user_id 补齐当前邮箱和用户名。

**Tech Stack:** Go、Gin、PostgreSQL、sqlmock、React 18、TypeScript、Ant Design、React Testing Library。

## Global Constraints

- 邮箱是登录唯一标识，也是不可变的业务用户标识；创建后任何 Admin 接口都不得修改邮箱。
- 用户名是独立的可编辑字段；编辑只修改用户名和角色，不包含状态或邮箱。
- pending、dispatched、processing 是活动打印任务；存在任一活动任务时拒绝删除用户。
- 无活动任务时，用户删除事务级联删除该用户打印任务及其告警，再删除用户记录。
- 不删除用户文件，不修改真实 SSO、丽娃文件接口、D2-SSO 或 HMAC 逻辑。
- 不创建新的用户状态表；停用/恢复继续映射到 users.status，通过 Toggle 暴露。
- 不修改部署工具箱；本轮只修改当前 Cloud worktree。
- 每个生产行为变更先有失败测试，再写最小实现；只有代码发生修改才运行对应测试。

---

### Task 1: 用户 Repository 的筛选、不可变邮箱与删除事务

**Files:**
- Modify: api/internal/database/user_repository.go
- Test: api/internal/database/user_repository_test.go
- Reference: api/internal/models/models.go

**Interfaces:**
- Consumes: existing database.DB.BeginTx, models.User, users, print_jobs, operational_alerts tables.
- Produces:
  - type UserListFilter struct { Search, Role, Status, SortBy, SortOrder string; Offset, Limit int }
  - ListUsers(filter UserListFilter)
  - GetUserByIDAnyStatus(id string)
  - UpdateEnabled(id string, enabled bool)
  - ErrUserHasActivePrintJobs
  - DeleteUserWithPrintJobs(id string)

- [ ] **Step 1: Write failing repository tests for all-status listing and immutable-field update**

Add sqlmock tests expecting ListUsers with Search, Role, Status, SortBy=email, SortOrder=asc, Offset=0, Limit=20 to query active/inactive rows, apply keyword/role/status filters, and use ORDER BY email ASC. Add tests that UpdateUser changes only username and role, and that EmailExists and UsernameExists count inactive rows too.

~~~go
func TestUserRepositoryListUsersIncludesInactiveAndAppliesFilters(t *testing.T) {
    db, mock, closeDB := newSQLMockDB(t)
    defer closeDB()
    mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM users")).
        WithArgs("%alice%", "viewer", "inactive").
        WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
    mock.ExpectQuery(regexp.QuoteMeta("SELECT id, username, email, role, status, last_login, created_at, updated_at")).
        WithArgs("%alice%", "viewer", "inactive", 20, 0).
        WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email", "role", "status", "last_login", "created_at", "updated_at"}).
            AddRow("u-1", "alice", "alice@example.com", "viewer", "inactive", nil, time.Now(), time.Now()))

    users, total, err := NewUserRepository(db).ListUsers(UserListFilter{
        Search: "alice", Role: "viewer", Status: "inactive", SortBy: "email", SortOrder: "asc", Limit: 20,
    })
    if err != nil || total != 1 || len(users) != 1 || users[0].Status != "inactive" {
        t.Fatalf("ListUsers() = users=%v total=%d err=%v", users, total, err)
    }
    if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
}
~~~

- [ ] **Step 2: Run focused tests and verify they fail**

Run: go test ./internal/database -run 'TestUserRepository(ListUsersIncludesInactiveAndAppliesFilters|UpdateUser|EmailExists|UsernameExists)' -count=1

Expected: FAIL because the current repository signature only accepts offset/limit, filters active users, and updates email/status.

- [ ] **Step 3: Implement the list filter and safe sort allowlist**

Change ListUsers to build WHERE clauses for search against id::text, username, and LOWER(email), optional role/status, and pagination. Map only id, username, email, role, status, last_login, and created_at to SQL expressions; reject unknown sort fields by falling back to created_at, and normalize direction to ASC or DESC. Do not concatenate user input outside that allowlist.

- [ ] **Step 4: Implement immutable email semantics and the enabled Toggle repository method**

Make UpdateUser update only username and role. Keep email normalization and uniqueness checks for create, but make EmailExists and UsernameExists include both active and inactive rows. Add GetUserByIDAnyStatus without the active-status predicate and UpdateEnabled using status = CASE WHEN enabled THEN active ELSE inactive END, returning the complete user row.

- [ ] **Step 5: Add the atomic user deletion method and red/green tests**

Write tests for these transaction branches before implementation:

~~~go
func TestDeleteUserWithPrintJobsRejectsActiveJobsAndRollsBack(t *testing.T) {
    db, mock, closeDB := newSQLMockDB(t)
    defer closeDB()
    mock.ExpectBegin()
    mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM users WHERE id = $1 FOR UPDATE")).
        WithArgs("u-1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("u-1"))
    mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM print_jobs WHERE user_id = $1 AND status IN")).
        WithArgs("u-1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
    mock.ExpectRollback()

    err := NewUserRepository(db).DeleteUserWithPrintJobs("u-1")
    if !errors.Is(err, ErrUserHasActivePrintJobs) { t.Fatalf("error = %v", err) }
    if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
}
~~~

Add a success test expecting BEGIN, locked user, active count 0, delete alerts by the selected job IDs, delete all print_jobs for the user, delete users, and COMMIT. Add a failure test expecting rollback after a deletion error.

- [ ] **Step 6: Implement the transaction**

Use BeginTx and defer Rollback. Lock the user row with FOR UPDATE; return a not-found error when absent. Count only pending, dispatched, and processing. Return ErrUserHasActivePrintJobs before any delete if count is nonzero. Delete operational_alerts whose job_id belongs to the user, delete all of the user’s print_jobs, then delete the user record and commit. Do not delete files.

- [ ] **Step 7: Run repository tests and commit**

Run: go test ./internal/database -count=1

Expected: PASS, including existing repository tests.

~~~bash
git add api/internal/database/user_repository.go api/internal/database/user_repository_test.go
git commit -m "feat: add user status and deletion repository operations"
~~~

### Task 2: User handler API, error codes and route registration

**Files:**
- Modify: api/internal/handlers/user_handler.go
- Modify: api/internal/handlers/errors.go
- Modify: api/cmd/server/main.go
- Test: api/internal/handlers/user_handler_test.go

**Interfaces:**
- Consumes: Task 1 repository methods and existing OAuth2ResourceServer("fly-print-admin") route middleware.
- Produces:
  - PATCH /api/v1/admin/users/:id/enabled with { "enabled": boolean };
  - PUT /api/v1/admin/users/:id with { "username": string, "role": string };
  - DELETE /api/v1/admin/users/:id with conflict response for active jobs;
  - error code ErrCodeUserHasActivePrintJobs and message 用户存在打印中的任务，无法删除.

- [ ] **Step 1: Write handler tests for the current-user bug, Toggle, immutable email and delete rules**

Create user_handler_test.go with Gin httptest requests and a SQL-mock repository database. Set external_id to admin-1 in the delete context and assert deleting user-2 reaches the repository; assert deleting admin-1 returns 400 rather than “未找到当前用户信息”. Add tests that Toggle accepts only a boolean, update accepts username/role, an update payload containing email returns 400, active-job deletion returns 409 with the stable Chinese message, and successful deletion returns 200.

~~~go
func TestUserHandlerDeleteUsesExternalIDForCurrentAdmin(t *testing.T) {
    // Configure sqlmock for target lookup, zero active jobs, task cleanup, user delete and commit.
    c, _ := gin.CreateTestContext(httptest.NewRecorder())
    c.Params = gin.Params{{Key: "id", Value: "user-2"}}
    c.Set("external_id", "admin-1")
    h := NewUserHandler(NewUserRepository(db))
    h.DeleteUser(c)
    if c.Writer.Status() != http.StatusOK { t.Fatalf("status = %d", c.Writer.Status()) }
}
~~~

- [ ] **Step 2: Run focused handler tests and verify they fail**

Run: go test ./internal/handlers -run 'TestUserHandler(DeleteUsesExternalIDForCurrentAdmin|Toggle|Update|Delete)' -count=1

Expected: FAIL because the current handler reads user_id, requires email/status on update, has no Toggle handler, and maps DELETE to soft deletion.

- [ ] **Step 3: Implement request/response contracts and handler behavior**

Require username on create and keep email required and immutable after creation. Change update validation to username and role only; if an email field is supplied, return ErrCodeBadRequest without changing the stored email. Add UpdateEnabled, converting the JSON boolean to the repository status. In DeleteUser, read and type-check external_id, reject self-delete, call DeleteUserWithPrintJobs, map ErrUserHasActivePrintJobs to HTTP 409 and the new error code, and map missing users to the existing user-not-found response.

- [ ] **Step 4: Register the PATCH route and preserve Admin scope protection**

Register userManagementGroup.PATCH("/:id/enabled", userHandler.UpdateEnabled) next to the existing GET/POST/PUT/DELETE routes. Keep all routes behind the existing Admin scope middleware; do not add viewer access.

- [ ] **Step 5: Run all Cloud Go tests and commit**

Run: go test ./...

Expected: PASS with all existing HMAC and OAuth2 tests unchanged.

~~~bash
git add api/internal/handlers/user_handler.go api/internal/handlers/user_handler_test.go api/internal/handlers/errors.go api/cmd/server/main.go
git commit -m "feat: expose user enable and protected deletion APIs"
~~~

### Task 3: Print job user email API and filtering

**Files:**
- Modify: api/internal/models/models.go
- Modify: api/internal/database/print_job_repository.go
- Modify: api/internal/handlers/print_job_handler.go
- Test: api/internal/database/print_job_repository_test.go
- Test: api/internal/handlers/print_job_handler_test.go

**Interfaces:**
- Consumes: existing print_jobs.user_id, print_jobs.user_name, immutable users.email and users.username.
- Produces:
  - models.PrintJob.UserEmail serialized as user_email;
  - user_email query filter on the Admin print-job list;
  - existing user_id filter remains supported;
  - print-job list/detail rows expose current matched user email and username.

- [ ] **Step 1: Write failing repository tests for user enrichment and email filtering**

Add UserEmail to the expected result shape in tests and expect the list query to left join users with u.id::text = pj.user_id, return u.email, and filter with LOWER(u.email) = LOWER($n) when user_email is provided. Include a row where no user matches and assert the existing pj.user_name remains available without a user email.

- [ ] **Step 2: Run focused print-job tests and verify they fail**

Run: go test ./internal/database ./internal/handlers -run 'PrintJob.*User|UserEmail|user_email' -count=1

Expected: FAIL because PrintJob has no UserEmail and the repository/handler do not accept the user_email filter.

- [ ] **Step 3: Implement the model and repository joins without changing the schema**

Add UserEmail string json:"user_email,omitempty". In list and detail queries, use LEFT JOIN users u ON u.id::text = pj.user_id; select u.email as user_email and COALESCE(NULLIF(u.username, ''), pj.user_name, '') as the displayed name. Keep unmatched third-party jobs readable using their stored user_name. Add a user_email argument through list and count methods and use case-insensitive equality against the immutable email.

- [ ] **Step 4: Implement handler query parsing and tests**

Read user_email := c.Query("user_email") in ListPrintJobs and pass it through both count and list repository calls. Keep existing user_id, printer, node, status, initiator and date filters unchanged. Test that the query reaches the repository path and appears in the response.

- [ ] **Step 5: Run Cloud Go tests and commit**

Run: go test ./...

Expected: PASS, including existing print-job and integration tests.

~~~bash
git add api/internal/models/models.go api/internal/database/print_job_repository.go api/internal/database/print_job_repository_test.go api/internal/handlers/print_job_handler.go api/internal/handlers/print_job_handler_test.go
git commit -m "feat: expose print job user email and filter"
~~~

### Task 4: Admin user-management interaction

**Files:**
- Modify: admin/src/components/pages/Users.tsx
- Test: admin/src/components/pages/Users.test.tsx

**Interfaces:**
- Consumes: Task 2 API contracts and existing Ant Design patterns from EdgeNodes.tsx, Printers.tsx, and OpsContacts.tsx.
- Produces: user list with immutable email links, username inline editor, role edit form, status Switch, delete button, filters, sorting and pagination.

- [ ] **Step 1: Write failing React tests**

Create tests with MemoryRouter, apiService.getToken = jest.fn().mockResolvedValue('admin-token'), and a fetch stub for the user list. Cover these observable behaviors:

~~~tsx
test('shows email and username separately and links email to filtered print jobs', async () => {
  render(<Users />);
  expect(await screen.findByText('alice@example.com')).toHaveAttribute('href', expect.stringContaining('/print-jobs?user_email='));
  expect(screen.getByText('Alice')).toBeInTheDocument();
});

test('edits username inline and cancels when clicking outside', async () => {
  render(<Users />);
  await userEvent.click(await screen.findByText('Alice'));
  expect(screen.getByRole('textbox')).toHaveValue('Alice');
  await userEvent.click(document.body);
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
});
~~~

Also assert that no status form field exists, the Switch calls PATCH /admin/users/:id/enabled, the delete button calls DELETE, active-job errors are displayed, and keyword/role/status filters change visible rows.

- [ ] **Step 2: Run the new tests and verify they fail**

Run: npm test -- --watchAll=false --runInBand src/components/pages/Users.test.tsx

Expected: FAIL because the current component has no username field, no email link, uses a status form selector, uses DELETE for stop, and has no filtering state.

- [ ] **Step 3: Implement data loading and query-state controls**

Add username and created_at to ManagedUser. Read email from useSearchParams as the initial keyword filter. Load active and inactive users with search, role, status, page, page_size, sort_by, and sort_order. Keep pagination total from the API and reset to page 1 when search/filter changes.

- [ ] **Step 4: Implement the table interactions**

Render the email as a link to /print-jobs?user_email=...; render username as a single value that enters an inline editor on click. The editor must use a class such as .inline-username-editor, save username only, and close without saving when the document mousedown target is outside that class. Remove the status column and status form item. Keep email read-only in the table and out of the edit submission, allow role changes, and require username for create.

- [ ] **Step 5: Implement Toggle, delete and error handling**

Render Switch checked={user.status === 'active'} in an 启用 column with per-user loading. Call PATCH /admin/users/:id/enabled with {enabled} and reload the current page. Use a danger delete button with Modal.confirm; call DELETE only after confirmation and display the server’s active-job conflict message through mapApiError.

- [ ] **Step 6: Run React tests and build, then commit**

Run: npm test -- --watchAll=false --runInBand src/components/pages/Users.test.tsx and npm run build.

Expected: PASS for the focused tests and a successful production build.

~~~bash
git add admin/src/components/pages/Users.tsx admin/src/components/pages/Users.test.tsx
git commit -m "feat: improve admin user management operations"
~~~

### Task 5: Print-job user display and bidirectional navigation

**Files:**
- Modify: admin/src/components/pages/PrintJobs.tsx
- Test: admin/src/components/pages/PrintJobs.test.tsx

**Interfaces:**
- Consumes: Task 3 user_email, user_name, and user_email query filter.
- Produces: a 打印人 column with email first, grey name second, and email navigation to User Management.

- [ ] **Step 1: Write failing React tests**

Create PrintJobs.test.tsx with a fetch response containing { user_email: 'alice@example.com', user_name: 'Alice' }. Assert the email is rendered, the name is rendered in a grey secondary element, and the email link points to /users?email=alice%40example.com. Add a test that user_email from the URL is included in the Admin print-jobs fetch URL.

- [ ] **Step 2: Run the tests and verify they fail**

Run: npm test -- --watchAll=false --runInBand src/components/pages/PrintJobs.test.tsx

Expected: FAIL because PrintJob has no user fields, the page has no user-email URL filter, and the table has no 打印人 column.

- [ ] **Step 3: Implement the user filter and display**

Read user_email from useSearchParams, pass it from listJobs to the API, include it in keyword matching, and add a sortable 打印人 column. Render the email as a link to /users?email=...; render user_name below it with color #8c8c8c and fontSize 12. When no email exists, render the stored name without a link.

- [ ] **Step 4: Run focused tests and build, then commit**

Run: npm test -- --watchAll=false --runInBand src/components/pages/PrintJobs.test.tsx and npm run build.

Expected: PASS and successful build.

~~~bash
git add admin/src/components/pages/PrintJobs.tsx admin/src/components/pages/PrintJobs.test.tsx
git commit -m "feat: link print jobs to user accounts"
~~~

### Task 6: Documentation and release verification

**Files:**
- Modify: README.md
- Modify: docs/使用指南.md
- Reference: docs/superpowers/specs/2026-07-27-user-management-operations-design.md

- [ ] **Step 1: Update operator documentation**

Document that email is the immutable login/user identifier, username is editable, status is controlled by the Enable Switch, deletion is blocked by active jobs, and print-job user emails link back to User Management. Include the exact user-management API paths and the stable Chinese deletion error.

- [ ] **Step 2: Run the complete verification suite**

Run from api: go test ./...

Run from admin: npm test -- --watchAll=false --runInBand and npm run build.

Expected: Go tests and the Admin test command pass; the Admin production build completes successfully. If the existing CRA test discovery issue recurs, record the exact command/output and do not silently delete tests.

- [ ] **Step 3: Inspect changes and privacy boundary**

Run:

~~~powershell
git status --short
git diff --check
git diff origin/main...HEAD --stat
git diff origin/main...HEAD -- api admin README.md docs
rg -n --hidden -g '!admin/node_modules/**' -g '!admin/build/**' '(?i)(password|secret|token|authorization|Bearer|private key|BEGIN [A-Z ]+ KEY|\b(?:\d{1,3}\.){3}\d{1,3}\b)' api admin README.md docs
~~~

Review matches manually; do not add deployment .env, IPs, tokens or toolbox files. Do not push in this task.

- [ ] **Step 4: Commit documentation and final local state**

~~~bash
git add README.md docs/使用指南.md
git commit -m "docs: document user and print job operations"
git status --short --branch
~~~

Expected: the worktree is clean, the branch contains only the intended local commits, and the branch is ready for the later prebuild step.
