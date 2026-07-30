# Cloud 运行、接口与验证

## 启动

入口 `api/cmd/server/main.go`：配置校验 → PG → `InitTables` → 可选默认管理员 → services → 后台任务 → WS manager → Gin。

- 配置：当前目录 / `./configs` / `/etc/fly-print-cloud` 的 `config.yaml`；`FLY_PRINT_*` 覆盖 YAML。Compose 主要靠根目录 `.env`。
- Schema：无独立迁移目录；改表须幂等兼容旧实例。表含 users、edge_nodes、printers、print_jobs、files、oauth2_clients、token_usage_records、system_settings 等。
- 后台：小时清过期 token 记录；小时清 >24h 合格文件；30min 将超时「打印中」标失败；5min 对 >3min pending 在在线且未超 `MaxRetries` 时重发。

## 路由权威

`setupRoutes`（`main.go`）。`/api/v1`：限流 10r/s + CORS + 安全头。

| 区域 | 要点 |
|------|------|
| 健康 | `/health`；`/api/v1/health` |
| 认证 | `/auth/*`（builtin token；Keycloak 走 login/callback） |
| Admin | dashboard/users/profile/business-settings/edge-nodes/printers/print-jobs/oauth2-clients；角色见中间件 |
| 第三方 | `POST|GET /api/v1/print-jobs`（`print:submit`）；`GET /api/v1/printers` |
| Edge | `POST .../edge/register`；`.../edge/:node_id/printers*`；`GET .../edge/ws?node_id=` |
| 文件 | `/api/v1/files/*`（upload-policy、verify、preflight、上传、下载） |

OAuth2：`api/internal/middleware/oauth2.go`；多 scope = AND；`admin` 拥有全部 scope。改认证须重核签名/issuer/audience 与角色映射。

## 文件与打印链路（摘要）

公共上传：Edge 要一次性 upload token → `/upload?...` → policy/verify → preflight → 上传。下载凭证短期签名、按资源绑定，经 `token_usage_records` 预登记/撤销/一次性消费。

打印：创建 → 校验打印机/节点/能力 → pending/dispatched + `print_job` → ACK/`job_update`；ACK 失败由后台重试。取消/禁用时注意 token 撤销与状态一致。存储迁移工具：`api/cmd/migrate-files`（须先备份 DB + 对象）。

## 前端

路由：`admin/src/App.tsx`；API：`services/api.ts`；路径：`config.ts`。Compose 同域默认 `REACT_APP_API_BASE_PATH=/api/v1`、`REACT_APP_AUTH_BASE_PATH=/auth`。改反代前缀须同步 `.env.example`、Compose build args、`config.ts`、`nginx/conf.d/admin.conf`。

## 命令

> **交付收口：** 本轮 Cloud 有改动时，全部改完后执行下方 `docker compose up --build -d`（update）。Edge 改动另打安装包，见工作区 `.cursor/rules/delivery-artifacts.mdc`。

```powershell
Copy-Item .env.example .env   # 必须改密钥后再起
docker compose up --build -d
docker compose ps
docker compose logs -f api nginx sso-login-demo site-portal
# docker compose down     # 保留数据；禁止 down -v

Set-Location api; go run ./cmd/server; go test ./...
Set-Location admin
npm.cmd ci
npm.cmd test -- --watchAll=false --runInBand
npm.cmd run build

# 依赖运行中 Cloud（非离线单测）
python api/tests/cloud_smoke_test.py
python api/tests/cloud_api_perf.py
```

## 传输 scheme

- Provider `entry_url` / `callback_base_url` 与 `file.url` 支持 **http** 与 **https**（禁止 userinfo；正式对接推荐 https；不支持自签）。
- Edge `cloud.base_url` 同样可为 http(s)；Edge 侧自动映射 ws/wss。局域网演示常用 `http://<LAN-IP>:8012`。
- 切片一联调时，`EXTERNAL_API_URL`、`SITE_PORTAL_PUBLIC_BASE_URL` 与 `SSO_DEMO_PUBLIC_BASE_URL` 必须分别设置为手机和 Edge 可访问的 `8012`、`8082`、`8081` 地址；容器间仍使用 Compose 服务名。

## Site Portal 身份联调检查

1. `docker compose up --build -d` 后确认 `api`、`sso-login-demo`、`site-portal` 和 `nginx` 正常。
2. 检查 `/api/v1/health`、SSO `:8081/health`、Site Portal `:8082/health`。
3. 从 Site Portal `/ops` 创建启用用户，确认 Cloud 尚无外部身份映射。
4. Edge 指向 `EXTERNAL_API_URL`，扫码并完成登录，确认首次生成映射且 Edge 只展示公共身份。
5. 新会话再次登录，确认复用同一 Cloud 用户；禁用用户后确认登录被拒绝。
6. 检查 Cloud 响应、数据库与日志不含用户密码、登录 Cookie 或 PRP 访问凭证。

发布前最少：健康、登录、扫码/上传、预览、打印、状态回传、Edge 重连、重复消息/文件。与源码冲突时以源码为准并回写本文。
