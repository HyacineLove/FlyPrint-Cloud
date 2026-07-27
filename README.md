# FlyPrint Cloud

FlyPrint Cloud 是 FlyPrint 的云端控制面和文件服务。它负责认证、文件上传与下载、打印任务管理、边缘节点与打印机管理、业务规则配置，以及通过 WebSocket 向 FlyPrint Edge 分发预览和打印任务。

Cloud 不直接访问现场打印机。打印机发现、文档预览和实际打印由 FlyPrint Edge 完成。

## 当前状态

当前已经具备以下主要能力：

- 内置账号或外部 OAuth2/Keycloak 认证；
- Edge 节点注册、启停和在线状态管理；
- 打印机注册、状态同步和管理；
- 文件上传、下载、有效期控制和文件内容哈希；
- 本地文件系统或 MinIO 对象存储；
- 打印任务创建、分发、ACK、状态回传和有限重试；
- Dashboard、Edge Nodes、Printers、Print Jobs、OAuth2 Clients 和 Business Settings 管理页面；
- 动态配置上传大小、文档页数、凭证有效期和允许的文件类型；
- Swagger 接口页面。

以下能力尚未完成或尚未形成可靠交付门禁：

- `Users` 和 `Settings` 页面仍是占位页面；
- Dashboard 请求失败时仍可能回退到模拟趋势数据；
- 没有持续集成流水线；
- Cloud 与 Edge 的真实端到端测试、断线恢复和升级兼容测试尚未成为自动发布门禁；
- 数据库结构变更由应用启动时执行，缺少带版本和回滚能力的迁移工具；
- MinIO 镜像当前使用 `latest`，生产部署前应固定版本。

因此，本仓库应视为“核心业务闭环已实现，工程化和交付验收仍需补齐”，而不是已完成生产认证的版本。

## 技术栈

- 后端：Go 1.25、Gin、PostgreSQL、Gorilla WebSocket、Viper、Zap；
- 认证：内置 JWT/OAuth2 或外部 Keycloak/OAuth2；
- 存储：本地文件系统或 MinIO；
- 前端：Node 18 基线、React 18、TypeScript、Ant Design、ECharts；
- 部署：Docker Compose、Nginx。

## 目录

```text
fly-print-cloud/
├── api/
│   ├── cmd/server/               # API 服务入口
│   ├── cmd/migrate-files/        # 文件存储迁移工具
│   ├── internal/handlers/        # HTTP 处理器
│   ├── internal/websocket/       # Cloud 与 Edge 长连接协议
│   ├── internal/database/        # 数据访问和启动时建表
│   ├── internal/storage/         # local/MinIO 存储实现
│   ├── internal/security/        # token 与文件访问安全
│   └── tests/                    # 运行中环境的 smoke/performance 脚本
├── admin/                        # React 管理端和公开上传页
├── nginx/                        # 统一入口和反向代理
├── docker-compose.yml
└── .env.example
```

## 快速开始（局域网主路径）

需要已安装 Docker Desktop（或等价 Docker Engine + Compose）。首次构建可能较慢。

```powershell
cd fly-print-cloud
docker compose up --build -d
```

不必先复制 `.env`：演示默认值已在 `docker-compose.yml` 中。  
默认会顺带启动 `integration-demo` 容器；**官方扫码打印不依赖它**，可不配置。

浏览器打开：`http://127.0.0.1:8012`  

| 项 | 默认值 |
|----|--------|
| 登录邮箱 | `admin@flyprint.local` |
| 密码 | `admin123` |

健康检查：`GET /health`、`GET /api/v1/health`。

**推荐阅读：**

- 系统说明（产品架构与边界）：[`docs/系统说明.md`](docs/系统说明.md)
- 部署与验证（默认 HTTPS；局域网见文内其他形态）：[`docs/部署与验证.md`](docs/部署与验证.md)
- 第三方对接：[`docs/第三方接入指南.md`](docs/第三方接入指南.md)
**以上默认密钥/密码仅供本机或局域网演示，禁止用于公网或生产。** 要改端口、密码或密钥时再执行 `Copy-Item .env.example .env` 后编辑。第三方对接见 [`docs/第三方接入指南.md`](docs/第三方接入指南.md)。

## Docker Compose 启动（可选定制）

### 1. 可选：准备 `.env`

```powershell
Copy-Item .env.example .env
```

按需修改端口、管理员密码等。局域网演示**不必**为了启动而改 `EXTERNAL_API_URL` / `ALLOWED_ORIGINS`。

公网或生产部署前必须轮换：

- `POSTGRES_PASSWORD`、`MINIO_*`、`DEFAULT_ADMIN_PASSWORD`
- `OAUTH2_JWT_SIGNING_SECRET`、`FILE_ACCESS_SECRET`、`OAUTH_CLIENT_SECRET_ENCRYPTION_KEY`、`REDIS_PASSWORD`
- 以及对外域名相关的 `EXTERNAL_API_URL`、`ADMIN_CONSOLE_URL`、`ALLOWED_ORIGINS`

### 2. 启动

```powershell
docker compose up --build -d
docker compose ps
```

默认统一入口为 `http://127.0.0.1:8012`。

常用检查地址：

- 基础健康检查：`GET /health`；
- 详细健康检查：`GET /api/v1/health`；
- Swagger：`/swagger/index.html`；
- 管理端：`/`。
- 第三方 Demo（**可选**）：`/integration-demo/`（对接契约见 [`docs/第三方接入指南.md`](docs/第三方接入指南.md) 第 8 节）。

查看日志：

```powershell
docker compose logs -f api nginx
```

停止服务：

```powershell
docker compose down
```

不要在保留数据的环境中随意执行 `docker compose down -v`，该命令会删除命名卷中的数据库和文件数据。

## 独立开发

### API

复制 `api/config.example.yaml` 为 `api/config.yaml`，按实际环境配置 PostgreSQL、认证和存储。

```powershell
Set-Location api
go run ./cmd/server
go test ./...
```

环境变量以 `FLY_PRINT_` 开头，并覆盖 YAML 中的同名配置。Compose 部署主要通过根目录 `.env` 和 `docker-compose.yml` 注入环境变量。

### 管理端

```powershell
Set-Location admin
npm ci
npm start
npm test -- --watchAll=false
npm run build
```

Node 和 npm 版本应在 CI 建立后固定；在此之前不要用新的 `package-lock.json` 覆盖未验证的生产依赖树。

## 存储和数据生命周期

- `STORAGE_PROVIDER` 支持 `local` 和 `minio`；
- `STORAGE_DOWNLOAD_MODE` 当前交付建议保持 `proxy`；
- 上传大小、最大页数、允许类型、上传凭证 TTL 和下载凭证 TTL 可在 Business Settings 中动态维护；
- Cloud 保存文件元数据和内容哈希，并向 Edge 下发短期下载凭证；
- 后台任务会清理过期 token、超时任务和满足清理条件的文件；当前实现包含约 24 小时文件清理逻辑，正式部署前应结合业务留存要求复核；
- `api/cmd/migrate-files` 用于存储后端迁移，但生产迁移前必须备份数据库与文件数据，并在隔离环境验证。

PostgreSQL 和 MinIO 数据均应纳入备份。只有备份文件而不备份数据库，或只备份数据库而不备份对象存储，都不能完整恢复打印文件关系。

## Cloud 与 Edge 的边界

### 认证

Edge 使用 OAuth2 `client_credentials` 获取访问令牌。实际 scope 由 Cloud 客户端配置和接口校验共同决定，至少涉及节点注册、心跳、打印机和文件读取能力。

内置账号使用邮箱作为唯一登录标识。官方注册只需要邮箱和密码，注册用户固定为 `viewer`；管理端的用户管理可按邮箱创建、编辑、停用账号和修改密码。旧 `users.username` 字段仅为数据库兼容保留，不再作为内置账号登录名。官方上传页提供“退出账号”，退出后会回到登录页并保留当前扫码地址。

### REST 摘要

- `POST /api/v1/edge/register`：注册 Edge 节点；
- `POST /api/v1/edge/{node_id}/printers`：注册打印机；
- `DELETE /api/v1/edge/{node_id}/printers/{printer_id}`：删除打印机；
- `POST /api/v1/edge/{node_id}/printers/status`：批量同步打印机状态；
- `/api/v1/files`：上传、下载、上传策略和预检；
- `/api/v1/print-jobs`：创建和查询打印任务；
- `/api/v1/admin/*`：管理端接口。

### WebSocket 摘要

连接入口：`GET /api/v1/edge/ws?node_id=...`。

Edge 上行的主要消息包括：

- `edge_heartbeat`；
- `job_update`；
- `submit_print_params`；
- `request_upload_token`；
- `ack`。

Cloud 下行的主要消息包括：

- `preview_file`；
- `print_job`；
- `upload_token`；
- `node_state`；
- `error`。

文件消息会包含 `content_hash`。当前 Edge 校验其格式并把它作为本地缓存键；尚未对下载字节重新计算 SHA-256。后续补充内容复算前，不能把该字段当作完整的端到端完整性校验。

接口的当前权威来源是 Go 路由、请求模型和 WebSocket 消息类型。现有 Swagger 尚未完整覆盖最新 Business Settings 等接口，修正生成源并加入 CI 后才能作为完整契约使用。新增或修改云边协议时，应同时增加 Cloud provider test 与 Edge consumer test；不再维护独立的手写全量协议 Markdown。

## 测试

后端单元测试：

```powershell
Set-Location api
go test ./...
```

前端测试：

```powershell
Set-Location admin
npm test -- --watchAll=false
```

已启动 Cloud 和 Edge 后，可运行：

```powershell
python api/tests/cloud_smoke_test.py
python api/tests/cloud_api_perf.py
```

相关环境变量：

- `FLYPRINT_BASE_URL`；
- `FLYPRINT_ADMIN_USERNAME`；
- `FLYPRINT_ADMIN_PASSWORD`；
- `FLYPRINT_EDGE_CLIENT_ID`；
- `FLYPRINT_EDGE_CLIENT_SECRET`。

Smoke/performance 脚本会读取同级工作区中的 `fly-print-edge/config.json`。它们不是可替代单元测试的离线测试。

## 发布前最低检查

1. 所有默认密码和签名密钥已替换（含 `OAUTH_CLIENT_SECRET_ENCRYPTION_KEY`；勿沿用局域网演示默认）；
2. Go 和前端测试通过；
3. 数据库与对象存储已经备份；
4. Cloud 可以看到 Edge 在线和打印机可用；
5. 完成一次扫码、上传、预览、打印和状态回传；
6. 验证 Edge 重连、重复消息和重复文件场景；
7. 验证当前 Cloud 与 Edge 版本兼容；
8. 确认 Users、Settings 等未完成功能没有被当成交付能力宣传。

## 总体文档

- **系统说明**：[`docs/系统说明.md`](docs/系统说明.md)
- **部署与验证**：[`docs/部署与验证.md`](docs/部署与验证.md)
- **第三方接入指南（接口契约）**：[`docs/第三方接入指南.md`](docs/第三方接入指南.md)
- 发版勾选：[`docs/agent/release-plan.md`](docs/agent/release-plan.md)

跨仓开发计划、当前进度和非技术使用说明位于工作区根目录：

- `FlyPrint开发计划.md`；
- `FlyPrint任务清单.md`；
- `FlyPrint总开发计划.md`；
- `FlyPrint后续开发方案.md`；
- `FlyPrint目前进度汇总.md`；
- `FlyPrint使用说明书.md`。
