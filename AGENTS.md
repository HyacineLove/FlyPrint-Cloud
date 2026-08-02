# FlyPrint Cloud — Agent

Go 控制面（Gin + PostgreSQL + WebSocket）。独立 git 仓库。

## 本仓规则

- 测试：`cd api && go test ./...`；真实 PostgreSQL 集成测试：`go test -tags integration ./...`（需 `FLYPRINT_TEST_POSTGRES_DSN`，未配置自动 skip）。
- 协议：WebSocket 消息权威在 `api/internal/websocket/message.go`；跨仓索引见根 `../docs/protocol.md`。
- 改协议：同步 `message.go` + provider 测试 + Edge consumer 测试 + `../docs/protocol.md`。
- 改 schema：`InitTables` 兼容旧实例 + 迁移 + repository/handler/测试。
- 完成态与交付收口见根 `../AGENTS.md`。
