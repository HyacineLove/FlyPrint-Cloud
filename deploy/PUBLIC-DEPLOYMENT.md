# Cloud 公网部署

## IP 临时阶段

1. 在云安全组和主机防火墙仅放行 TCP 80；SSH 仅允许运维来源。禁止开放 5432、8080、9000、9001、Docker API 或调试端口。
2. 从 `.env.release.example` 复制 `.env`，使用强随机密钥，并把 `EXTERNAL_API_URL`、`ADMIN_CONSOLE_URL` 与 `ALLOWED_ORIGINS` 替换成同一公网 IP 的 `http://` Origin。将 `TRUSTED_PROXY_CIDRS` 设置为仅由 Nginx 与 API 共享的 Docker 网络 CIDR（例如 `172.18.0.0/16`），不得填写 `0.0.0.0/0`。已有部署可用 `docker network inspect <compose-network-name> --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}'` 获取该 CIDR。
3. 仅在明确接受明文传输风险时设置 `ENTRY_COOKIE_SECURE=false`、`INSECURE_HTTP_MODE=true` 和未来 7 天内的 `INSECURE_HTTP_UNTIL`。到期后 API 自动退出且拒绝重启。
4. 在既有部署根目录（例如 `/prj`）解压扁平离线包，保留已有 `.env` 与命名卷，先执行 `sha256sum -c SHA256SUMS`，再执行 `docker load -i docker-images-linux-amd64.tar`，最后执行 `docker compose --env-file .env -f docker-compose.release.yml up -d --no-build`；不得执行 `docker compose down -v`。
5. 从外网确认仅 TCP 80 可达，`/health` 返回 200，且 5432、8080、9000、9001 不可达。

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
5. 每日执行 `certbot renew`，成功后仅 `docker compose exec -T nginx nginx -s reload`；不得执行 `down -v`。
