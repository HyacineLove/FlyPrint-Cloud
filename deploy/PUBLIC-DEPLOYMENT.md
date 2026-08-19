# Cloud 公网部署

## IP 临时阶段

1. 在云安全组和主机防火墙仅放行 TCP 80；SSH 仅允许运维来源。禁止开放 5432、8080、9000、9001、Docker API 或调试端口。
2. 从 `.env.release.example` 复制 `.env`，使用强随机密钥，并把 `EXTERNAL_API_URL`、`ADMIN_CONSOLE_URL` 与 `ALLOWED_ORIGINS` 替换成同一公网 IP 的 `http://` Origin。将 `TRUSTED_PROXY_CIDRS` 与 `SESSION_FILE_TRUSTED_PROXY_CIDRS` 设置为仅由 Nginx 与后端服务共享的 Docker 网络 CIDR（例如 `172.18.0.0/16`），不得填写 `0.0.0.0/0`。已有部署可用 `docker network inspect <compose-network-name> --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}'` 获取该 CIDR。`SESSION_FILE_ALLOWED_CIDRS` 只填写获准调用临时文件服务的 Site Portal 固定出口 CIDR；`SESSION_FILE_SIGN_SECRET` 使用至少 32 字符的独立随机值，并与 Site Portal 的 `SITE_PORTAL_SESSION_FILE_SIGN_SECRET` 保持一致；MinIO 专用账号密钥不得复用根账号。
3. 仅在明确接受明文传输风险时设置 `ENTRY_COOKIE_SECURE=false`、`INSECURE_HTTP_MODE=true` 和未来 7 天内的 `INSECURE_HTTP_UNTIL`。到期后 API 自动退出且拒绝重启。
4. 在既有部署根目录（例如 `/prj`）解压扁平离线包，保留已有 `.env` 与命名卷，先执行 `sha256sum -c SHA256SUMS`，再执行 `docker load -i docker-images-linux-amd64.tar`，最后执行 `docker compose --env-file .env -f docker-compose.release.yml up -d --no-build`；不得执行 `docker compose down -v`。
5. 从外网确认仅 TCP 80 可达，`/health` 返回 200，且 5432、8080、9000、9001 不可达。使用获准出口的 Site Portal 完成一次上传、列表、下载与会话退出，确认 `/internal/session-files/` 的未签名请求被拒绝，退出后相同文件不可再次下载；不得把该内部路径配置给 Edge 或浏览器。

## 域名 HTTPS 阶段

1. 将 DNS A/AAAA 记录指向服务器，并保留 TCP 80 供 HTTP-01 验证。在 `nginx/conf.d-https/https.conf` 的两个 `server_name _;` 位置替换为同一个真实公网域名；不得保留通配默认值。把 `.env` 的三个公网 URL 和 CORS Origin 改为同一 `https://` 域名；设置 `ENTRY_COOKIE_SECURE=true`、`INSECURE_HTTP_MODE=false`。使用 Keycloak 时，`OAUTH2_REDIRECT_URI` 必须填写为相同公网 Origin 加 `/auth/callback`；本机地址、不同主机和非 HTTPS 回调均会被拒绝。
2. 在 IP 阶段 Nginx 仍运行时申请证书：

   ```bash
   docker compose --env-file .env -f docker-compose.release.yml -f docker-compose.certbot.yml run --rm certbot certonly --webroot -w /var/www/certbot --cert-name fly-print-cloud -d <PUBLIC_FQDN> --email <OPS_EMAIL> --agree-tos --no-eff-email
   ```

3. 启用 HTTPS，不删除容器或数据卷：

   ```bash
   docker compose --env-file .env -f docker-compose.release.yml -f docker-compose.https.yml up -d --no-build
   ```

4. 把每个 Edge 的 `cloud.base_url` 改为 `https://<PUBLIC_FQDN>`，确认其 WebSocket 自动使用 WSS，且 `verify_ssl=true`。
5. 把每个 Site Portal 的 `SITE_PORTAL_SESSION_FILE_BASE_URL` 设置为 `https://<PUBLIC_FQDN>/internal/session-files`；保持签名密钥一致，并确认 `SITE_PORTAL_SESSION_FILE_TTL` 不大于 Cloud 的 `SESSION_FILE_MAX_TTL`。
6. 每日执行 `certbot renew`，成功后仅 `docker compose exec -T nginx nginx -s reload`；不得执行 `down -v`。
