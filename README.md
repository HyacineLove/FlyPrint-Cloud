# FlyPrint Cloud

云端控制面：认证、终端会话、用户映射、打印额度、任务审计。Go + PostgreSQL + WebSocket。

Cloud 发布体系同时包含独立的 `services/session-file-service` 临时文件数据面。它不导入 Cloud API 业务包，通过专用 MinIO Bucket 保存仅在打印会话 TTL 内有效的文件；外部只允许 Site Portal 经 Nginx 的 `/internal/session-files/` 路径调用。Edge 与浏览器不直接访问该服务或 MinIO。

Agent 入口见 `AGENTS.md`。
