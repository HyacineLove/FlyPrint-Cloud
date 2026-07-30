# Site Portal 身份链路协议

## 边界

- Cloud 保存 Site Portal 配置、Edge 默认 Site Portal、外部身份映射和 Cloud 用户。
- 身份服务保存账号和密码哈希，签发一次性授权码与短期 PRP 访问凭证。
- Site Portal 在内存中保存登录状态、一次性领取码和 PRP 访问凭证。
- Edge 在当前进程的内存会话中保存领取结果。
- PRP 访问凭证、身份登录 Cookie 和用户密码不进入 Cloud。

## 时序

1. Cloud 为当前 Edge 终端会话签发扫码票据；公网入口将票据标记为已选择，但不消费，然后跳转默认 Site Portal。
2. Site Portal 调用 `POST /api/v1/site-portal/context` 校验票据、Site Portal、Edge 和终端会话绑定。
3. Site Portal 使用 `state` 发起身份登录；身份服务登录成功后返回一次性授权码。
4. Site Portal 后端调用身份服务 `POST /api/token` 单次交换授权码，得到稳定外部用户标识、显示名和短期 PRP 访问凭证。
5. Site Portal 在内存中建立与 Site Portal、Edge、终端会话和外部用户绑定的一次性领取码。
6. Site Portal 调用 `POST /api/v1/site-portal/login-completions`。Cloud 首次登录时静默创建用户，已有映射则复用；成功后消费扫码票据。
7. Cloud 通过 `portal_session_ready` 通知目标 Edge。消息包含 Site Portal、领取地址、领取码、终端会话、Cloud 用户和失效时间，不包含 PRP 访问凭证。
8. Edge 校验本机当前终端会话，调用 Site Portal `POST /api/claims/redeem` 原子领取；Site Portal 返回外部用户、显示名、PRP 地址、访问凭证和失效时间。

## Cloud 接口

Site Portal 请求统一携带：

- `X-FlyPrint-Site-Portal: <site_portal_code>`
- `Authorization: Bearer <site_portal_api_token>`

`POST /api/v1/site-portal/context`

```json
{"terminal_ticket":"opaque-ticket"}
```

成功响应：

```json
{
  "site_portal_code":"official",
  "node_id":"edge-id",
  "printer_id":"printer-id",
  "terminal_session_id":"terminal-session-id",
  "expires_at":"2026-07-30T12:05:00Z"
}
```

`POST /api/v1/site-portal/login-completions`

```json
{
  "terminal_ticket":"opaque-ticket",
  "external_user_id":"stable-user-id",
  "display_name":"演示用户",
  "claim_code":"one-time-claim-code",
  "claim_expires_at":"2026-07-30T12:05:00Z"
}
```

## Cloud 到 Edge 消息

```json
{
  "type":"portal_session_ready",
  "data":{
    "site_portal_code":"official",
    "claim_base_url":"https://portal.example.test",
    "claim_code":"one-time-claim-code",
    "terminal_session_id":"terminal-session-id",
    "cloud_user_id":"cloud-user-id",
    "expires_at":"2026-07-30T12:05:00Z"
  }
}
```

## Site Portal 领取接口

`POST /api/claims/redeem`

```json
{
  "claim_code":"one-time-claim-code",
  "site_portal_code":"official",
  "node_id":"edge-id",
  "terminal_session_id":"terminal-session-id"
}
```

成功响应：

```json
{
  "site_portal_code":"official",
  "external_user_id":"stable-user-id",
  "display_name":"演示用户",
  "prp_base_url":"https://prp.example.test",
  "access_token":"opaque-prp-credential",
  "access_token_expires_at":"2026-07-30T12:05:00Z"
}
```

领取码错误、过期或已经消费返回 `409`；绑定不匹配返回 `403`。Edge 不自动重试领取。

## 运维用户接口

Site Portal `/ops` 通过后端代理身份服务的以下接口：

- `POST /api/ops/login`
- `GET /api/ops/users?search=`
- `POST /api/ops/users`
- `DELETE /api/ops/users/{id}`
- `POST /api/ops/users/{id}/reset-password`

身份服务不提供公开注册入口，也不提供账号编辑或启停能力。官方账号创建不会同步到 Cloud；只有首次成功完成上述登录链路时才产生 Cloud 用户映射。删除账号不会级联删除 Cloud 已有映射。

## 组件配置

Cloud 在管理接口交付前使用 `site_portal_bootstrap` 初始化一个 Site Portal：

```yaml
site_portal_bootstrap:
  code: official
  display_name: FlyPrint
  entry_url: http://127.0.0.1:8082/entry
  claim_base_url: http://127.0.0.1:8082
  api_token: replace-with-random-token-at-least-32-chars
```

对应环境变量为：

- `FLY_PRINT_SITE_PORTAL_BOOTSTRAP_CODE`
- `FLY_PRINT_SITE_PORTAL_BOOTSTRAP_DISPLAY_NAME`
- `FLY_PRINT_SITE_PORTAL_BOOTSTRAP_ENTRY_URL`
- `FLY_PRINT_SITE_PORTAL_BOOTSTRAP_CLAIM_BASE_URL`
- `FLY_PRINT_SITE_PORTAL_BOOTSTRAP_API_TOKEN`

Site Portal 与身份服务的非敏感配置模板分别位于 `site-portal/config.example.env` 和 `sso-login-demo/config.example.env`。模板中的地址与凭证均为本机示例或占位值。
