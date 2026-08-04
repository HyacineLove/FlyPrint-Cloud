# FlyPrint Cloud — Agent

Go 控制面（Gin + PostgreSQL + WebSocket）。独立 git 仓库。

## 本仓规则

- 测试：`cd api && go test ./...`；真实 PostgreSQL 集成测试：`go test -tags integration ./...`（需 `FLYPRINT_TEST_POSTGRES_DSN`，未配置自动 skip）。
- 协议：WebSocket 消息权威在 `api/internal/websocket/message.go`；跨仓索引见根 `../docs/protocol.md`。
- 改协议：同步 `message.go` + provider 测试 + Edge consumer 测试 + `../docs/protocol.md`。
- 改 schema：`InitTables` 兼容旧实例 + 迁移 + repository/handler/测试。
- 完成态与交付收口见根 `../AGENTS.md`。
- 云包交付：用户提出“云包”时，必须交付 Linux amd64 离线释放包。包内需包含当前代码构建出的业务镜像、PostgreSQL/Redis/MinIO/Nginx 等所需运行时镜像归档、Compose 配置和部署说明；目标机只需 `docker load`，再执行 `docker compose up -d --no-build`，不得要求目标机 `docker build` 或启动时 `docker pull`。仅含预编译二进制的联网包不得称为云包。
