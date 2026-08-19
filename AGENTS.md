# FlyPrint Cloud — Agent

Go 控制面（Gin + PostgreSQL + WebSocket），独立 git 仓库。

## 常用验证

- 单元/handler/WebSocket/repository：`cd api && go test ./...`
- 静态检查：`cd api && go vet ./...`
- PostgreSQL 集成：`cd api && go test -tags integration ./...`（需要 `FLYPRINT_TEST_POSTGRES_DSN`；未配置时只能报告 skip，不能视为真实数据库验收）
- 管理端：`cd admin && npm test -- --watchAll=false`、`npm run build`
- 根文档校验：`python scripts/doccheck.py`

## 协议与业务边界

- WebSocket 权威消息定义在 `api/internal/websocket/message.go`；跨仓协议同步 `../../docs/protocol.md` 与 Edge consumer 测试。
- 外部接入边界只有 Site Portal。Identity 与长期文件 Provider 均为外部服务；Site Portal 和 Cloud 仓库都不内嵌其生产源码。相邻本地参考仓库只提供 Keycloak OIDC 演示，不提供 PRP。
- 官方 `session-file-service` 是 Cloud 发布体系内的独立临时文件数据面：源码位于 `services/session-file-service/`，独立 Go module/镜像，不得导入 Cloud API业务包，也不得读取旧 Cloud文件表；外部只能由 Site Portal经 Nginx受保护路径调用。
- OAuth 客户端类型只允许 `edge_node`、`site_portal`。Site Portal 使用 client credentials 获取 Bearer token。

## 数据库

- `InitTables` 负责全新数据库初始化与迁移；破坏性清理迁移必须明确删除旧第三方表、数据、字段和 OAuth 客户端。
- 修改 schema 时同步 migration、repository、handler 和测试。

## 云包

用户提出“云包”时，必须交付 Linux amd64 离线释放包：包含当前代码构建的应用镜像、PostgreSQL/MinIO/Nginx 等运行时镜像归档、Compose、Admin 静态文件和部署说明。目标机只执行 `docker load`，然后从既有部署根目录执行 `docker compose --env-file .env -f docker-compose.release.yml up -d --no-build`；保留 `.env` 与命名卷，禁止 `docker compose down -v`，不得要求目标机 `docker pull` 或重新 `build`。
