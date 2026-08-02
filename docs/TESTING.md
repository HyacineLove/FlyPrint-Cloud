# Cloud API 测试组织与规则

Go 惯例：测试文件与被测代码**同包放置**（`_test.go`），不建独立 `tests/` 目录。测试按**运行层级**区分，目录即层级。

## 分层（按运行方式）

| 层 | 放进什么 | 运行方式 |
|----|---------|---------|
| **单元**（默认） | 各包 `*_test.go`：单模块行为，`go-sqlmock` 模拟 DB、mock 存储/网络边界 | `go test ./...`（门禁默认） |
| **集成** | 需要真实外部组件（PostgreSQL）的测试：文件名 `*_integration_test.go` 且文件头 `//go:build integration`；未配置 `FLYPRINT_TEST_POSTGRES_DSN` 时必须 `t.Skip` | `go test -tags integration ./...` |
| **契约** | 云边协议 provider 端测试（WebSocket 消息生成/解析），位于 `internal/websocket/`；消息样本放 `internal/websocket/testdata/*.json` | `go test ./internal/websocket/...`（默认） |

## 硬规则

1. **一个文件一个维度**：一个 `_test.go` 只测一个 repository / handler / 协议消息类型的公共行为；不测私有实现。
2. **协议两端成对**：改 Cloud–Edge 协议时，本仓 provider 测试与 Edge 仓 consumer 测试**必须同时更新**，优先复用 `internal/websocket/testdata/*.json` 样本（与 Edge `tests/contract/messages/` 对应），不各自硬编码新字段。
3. **进程内优先**：DB 访问用 `go-sqlmock`，存储用 `tempdir`；只有"验证与真实 PostgreSQL 协同"这一目的本身（如 schema 迁移、真实额度 SQL）才写集成测试。
4. **命名**：`<被测对象>_test.go`；用例名 `Test<被测对象><行为>`；行为用业务语义描述，不写实现细节。
5. **保持快速**：单元测试应 < 1s 级；需要真实 DB/慢资源的用例进集成层并打 build tag。
6. 新增集成测试 = `_integration_test.go` 命名 + `//go:build integration` + DSN skip 三件套缺一不可。

## 常用命令

```powershell
go test ./...                          # 门禁：全部单元 + 契约
go test -tags integration ./...        # 含真实 PostgreSQL 集成测试（需 FLYPRINT_TEST_POSTGRES_DSN）
go test ./internal/websocket/... -run TestJobUpdate -v   # 单测过滤
```
