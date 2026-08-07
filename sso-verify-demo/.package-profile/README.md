# SSO UAT 快速验证 Demo

此 Demo 只验证 Site Portal 作为 OAuth 保密客户端完成以下流程：

1. 生成一次性 state。
2. 跳转到 SSO 授权端点。
3. 用 authorization code、client_id、client_secret 和完全一致的 redirect_uri 换取 access_token。
4. 可选地使用该 access_token 调用 UserInfo 端点。
5. 页面只返回 Token 是否取得、Token 类型、有效期和用户信息校验结果，不显示或记录完整 Token、授权码和 client_secret。

本 Demo 的边界必须与完整打印链路区分：它只验证“Site Portal → SSO → access_token → UserInfo”，不实现 Cloud、Edge、PRP 或一次性 Claim 兑换，也不会把 Token 交给 Edge 或 PRP。因此，Demo 验证成功只能证明 Site Portal 能取得并使用 SSO Token，不能证明 PRP 已经能够校验或接受该 Token。

登录成功后，页面只展示 UserInfo 中的白名单个人信息：`sub/id`、用户名、邮箱、名字、姓氏和显示名。其他 UserInfo 字段会被丢弃；完整 `access_token`、授权码和 `client_secret` 不会展示、写入页面或普通日志。

## UAT 端点是否需要更换

需要。文档中的 https://sso.ecnu.edu.cn/... 只能作为当前文档所列环境的端点参考；UAT 应使用 SSO/Keycloak 管理方提供的 UAT Realm/Issuer 和对应端点。不能把生产端点、生产 client_id 或生产 client_secret 直接用于 UAT。

UAT 至少需要一套独立配置：

- UAT authorization endpoint
- UAT token endpoint
- UAT UserInfo 或 Token Introspection endpoint
- UAT 专用 client_id/client_secret
- UAT 允许的精确回调地址
- UAT 允许的 Scope

如果 UAT 使用 Keycloak，端点通常与 UAT Realm/Issuer 绑定，不能只替换域名后自行猜测路径；请以 SSO 管理方提供的 Discovery 文档或正式端点清单为准。

## 本地运行

PowerShell 示例：

~~~powershell
$env:SSO_VERIFY_AUTHORIZATION_URL = "https://sso-uat.example.edu/oauth2.0/authorize"
$env:SSO_VERIFY_TOKEN_URL = "https://sso-uat.example.edu/oauth2.0/accessToken"
$env:SSO_VERIFY_USERINFO_URL = "https://sso-uat.example.edu/oauth2.0/profile"
$env:SSO_VERIFY_CLIENT_ID = "replace-with-uat-client-id"
$env:SSO_VERIFY_CLIENT_SECRET = "replace-with-uat-client-secret-at-least-32-chars"
$env:SSO_VERIFY_SCOPE = "ECNU-Basic"
$env:SSO_VERIFY_REDIRECT_URI = "http://127.0.0.1:8090/callback"
go run .
~~~

浏览器打开：

~~~text
http://127.0.0.1:8090/
~~~

## UAT 子路径部署

本 Demo 可以作为临时 Site Portal 挂载在已有 UAT 域名的子路径下。以本项目的部署位置为例：

~~~text
https://uat.021hqit.com/fly-print-site-portal/
https://uat.021hqit.com/fly-print-site-portal/callback
~~~

Demo 进程继续只监听服务器回环地址 `127.0.0.1:8090`，由 Nginx 终止 HTTPS 并去掉外部路径前缀后转发：

~~~nginx
location = /fly-print-site-portal {
    return 308 /fly-print-site-portal/;
}

location ^~ /fly-print-site-portal/ {
    proxy_pass http://127.0.0.1:8090/;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
}
~~~

对应的 OAuth 回调配置必须是完整地址：

~~~text
SSO_VERIFY_REDIRECT_URI=https://uat.021hqit.com/fly-print-site-portal/callback
~~~

Nginx 配置应以现有 UAT 配置为基准，仅新增上述精确 `location`，不得覆盖其他服务的 `server` 或 `location`。

本地回调使用 http://127.0.0.1 仅用于快速验证；UAT 部署环境必须使用 HTTPS 回调地址，并在 SSO/Keycloak 中按字符精确登记。

## 安全边界

- SSO、Token 和 UserInfo 请求只允许使用 HTTPS；HTTP 客户端默认校验证书，不提供跳过 TLS 校验的配置。
- client_secret 只从服务端环境变量读取，不进入授权 URL、HTML、日志或响应。
- state 在服务端内存中生成、限时、一次性消费，回放会被拒绝。
- authorization code 只发送给 Token 端点，access_token 只发送给 UserInfo 端点。
- Demo 不持久化 Token，不将 Token 放入 Cookie、URL 或页面。
- 生产 UAT 部署时还应通过反向代理提供 HTTPS，并限制 Demo 的网络访问范围。
