# Cloud 架构与协议

## 定位

云端控制面与文件服务；不直连现场打印机。边界协议权威：`api/internal/websocket/message.go` 及 Go 路由/模型（Swagger 不全）。

```text
api/cmd/server/          入口、路由、后台任务
api/internal/handlers/   HTTP
api/internal/database/   PG、InitTables、repository
api/internal/websocket/  云边长连接
api/internal/storage/    local / MinIO
admin/                   React 管理端 + 公共上传
nginx/ + docker-compose.yml
```

栈：Go 1.25 / Gin / PG / WS；认证 `builtin`|`keycloak`；存储 `local`|`minio`（下载建议 `proxy`）；前端 React 18 + Ant Design；Compose 入口默认 `:8012`。

## 终端结果协议

- `print_job` 目标：`job_id` + `printer_id` + `file_url` + `content_hash`。`printer_name` 不在线契约、禁止作路由回退。
- 投递 ACK（最多 3 次）= Edge 已持久化收件，≠ 打印完成。
- 终态（`completed/failed/canceled/unconfirmed`）须带稳定 UUID `event_id`；`processing` 为尽力而为。
- Cloud 校验节点拥有目标打印机后，终态与 `edge_job_update_receipts` 同事务；`job_update_ack/accepted` 允许 Edge 清 outbox；`rejected` 为协议错，Edge 保留可见故障且不重试。
- 同 `event_id` 幂等接受；同 ID 不同 node/job/status/payload hash → reject。终态单调；例外：`unconfirmed/dispatch_ack_timeout` 可被真实终态替换。
- Site Portal 直打任务的明确终态同时携带 `impressions_completed`、`sheets_completed`、`quota_consumed`。Cloud 按任务页数、份数、单双面和彩色倍率复核，原子写入终态、实际消耗、未使用额度返还和回执；`unconfirmed` 不结算、不返还。

## 节点删除与预览绑定

- 节点删除=软删节点与打印机；历史任务/票据/集成请求/回调保留。同事务取消活动票据与未终态集成请求；清 ephemeral 映射后断 WS。禁止硬删打印机（`terminal_tickets.printer_id` FK 保留）。
- `waiting_terminal` 预览：`node_id`+`terminal_session_id` 匹配且 Edge ticket hash 仍为 NULL 时可首次绑定；绑定后后续须 hash 精确匹配。绑定后上报一次 `terminal_session_state`；用户确认参数前不建 `print_job`。

## 二维码入口

- 仅 Edge `/api/qr_code`。Cloud 回相对 `/entry?token=...`；Edge 用 `cloud.base_url` 拼接，并对 `localhost`/`127.0.0.1` **无条件**改写局域网 IP（代码不区分 http/https；HTTPS 应直接配证书域名）。`cloud.base_url` 支持 http(s)，WS 为 ws(s)。不依赖 `EXTERNAL_API_URL` 绝对地址。
- `/entry` 校验二维码入口凭证后签发独立 `terminal_ticket`；成功后下行 `terminal_occupied`（`msg_id` + Edge ACK；断线 pending，重连靠 `terminal_session_state` 补发）。随后按 Edge 配置的默认 Site Portal 跳转，Site Portal 在登录完成后才通知 Cloud 消费票据。
- 当前正式流程为每终端唯一登录源，不提供用户侧入口重选。Edge 刷新会话作废未完成 ticket；官方上传/`verify` 须 `edge_terminal_sessions.Matches`；`preview_file` 须带 `terminal_session_id` + `terminal_ticket_hash`。
- Site Portal 通过认证接口校验票据并报告外部身份。Cloud 首次登录静默创建用户映射，随后只向目标 Edge 下发 `portal_session_ready` 领取信息；PRP 访问凭证不进入 Cloud。协议见 `docs/agent/site-portal-identity-protocol.md`。

## Site Portal 打印授权与额度

- Edge 对已预览的 PRP 文件调用 `POST /api/v1/edge/:node_id/print-authorizations`。Cloud 只接收文件显示名、`content_hash` 对应的本地文件标识、打印参数与终端上下文，不接收文件体或 PRP 凭证。
- Cloud 以当前 `edge_terminal_sessions` 绑定的 Site Portal 和 Cloud 用户为准，校验用户、节点与打印机状态。`(edge_node_id, confirmation_id)` 保证重复请求只返回同一审计任务；不同请求体复用确认 ID 会被拒绝。
- 静默映射用户首次获得 50 点。预占按实体纸张计算：单面 `页数×份数`，双面 `ceil(页数/2)×份数`；彩色每张 2 点，黑白每张 1 点。额度没有每日重置；仅 `fly-print-admin` 可正向增加并留下管理员与原因审计。
- 授权成功只创建无文件体的统一 `print_jobs` 审计记录并预占额度，不向 Edge 下发 `print_job`。Edge 使用预览阶段已有的标准 PDF 直接执行 IPP，随后通过既有终态回执结算。

## 部署边界

- Cloud = 受控 Linux 上的认证/任务/文件/对象存储/云边通信。
- Edge = 一体机；Cloud 用独立可验证设备身份绑定 `node_id`，禁止用 MAC/共享 Client/请求参数推断身份。
- Edge 扩大暴露面（非回环等）须先确认并补鉴权。

## WebSocket（摘要）

Cloud→Edge：`print_job`、`preview_file`、`upload_token`、`terminal_occupied`、`portal_session_ready`、`node_state`、`config_update`、`report_status`、`error`
Edge→Cloud：`edge_heartbeat`、`job_update`、`submit_print_params`、`request_upload_token`、`terminal_session_state`、`ack`  
文件 payload 带 `content_hash` + 短期 `file_access_token`；Edge 校验 `content_hash` 格式并以其作为标准 PDF 缓存键。缓存未命中时，`DocumentPipeline.resolve_canonical` 对 `source_supplier` 提供的源文件计算 SHA-256，必须与 `content_hash` 一致后才进行标准化；缓存命中时直接复用已校验生成的标准 PDF，不重新获取源文件。

## 第三方交互式打印与 Demo

- 文件接管后不下发 `print_job`；`integration/terminal_dispatcher.go` 先发标准 `preview_file`（可选三项集成上下文 + 建议 `print_options`）。
- HMAC 第三方任务的用户确认仍通过 `submit_print_params` 回传上下文；Cloud 同事务校验后**每个集成请求仅一个**标准任务。Site Portal/PRP 打印改走独立的原子授权接口，不混用第三方任务确认。
- `allow_private_file_hosts` 默认关；开启后仍仅 `allowed_file_hosts` 精确主机，并拒绝环回/链路本地等。禁止当全局私网放行。
- Demo：`integration-demo/`，接入代码=`livacloud-demo`，路径 `/integration-demo/`。模拟 SSO/HMAC/callback；禁止在核心链路加入接入方专属分支。密钥粘贴到 `/integration-demo/setup`，不回显、不落日志。
- HMAC 第三方打印与 Site Portal 身份链路是两套独立边界：前者维持现有功能，后者负责官方与私有域的统一登录、Cloud 静默用户映射和 Edge 凭证领取。

## 已知缺口（勿当已交付）

Users/Settings/OAuth2Clients 未挂管理端菜单；无独立运维迁移 CLI（启动时 `InitTables` + `migrations.Run`）；MinIO 常用 `latest`；E2E/断线/升级兼容未成门禁。
