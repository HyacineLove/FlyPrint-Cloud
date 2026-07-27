# 发版计划 P0/P1

> **全局计划：** 工作区根目录 `FlyPrint开发计划.md` + `FlyPrint任务清单.md`；全量归档 `FlyPrint总开发计划.md`  
> 目标：2026-07-22 演示收口 · 更新：2026-07-27 · 与 `fly-print-edge/docs/agent/release-plan.md` 同步  
> **M0 已归档（07-22 verify 通过）。** 当前焦点 **M1 收口**（M1-1 已合入 `main`；当前工作区仍有 Cloud 交付与清理改动待提交/验证）。本文件保留 M0 勾选历史。  
> 交付：Cloud + Edge + 可选 Compose Demo + 系统说明 / 部署与验证 / 第三方 Guide  
> 文档主路径见 `docs/系统说明.md` + `docs/部署与验证.md`；**P0 预演（官方 + Demo）已于 07-22 verify 通过**

### M1 进度（摘要）

- [x] M1-1a～e：联系人 CRUD/绑定/Edge 页脚 + Admin 关联列与实体互跳/筛选排序/三方任务计数（已合入 `main`；Edge 安装包 **1.0.44**；Cloud 已 compose 预演）
- [ ] M1-2：横竖打；当前 Cloud 侧无对应实现，Edge 侧待开发
- [~] M1-3：触屏守卫已有；OS 手册步骤已写入部署文档，待现场验证
- [ ] M1-4：`toner_low` 黄警；当前改动未发现本项新增实现
- [~] M1-5～M1-6：文案与运维文档有工作区改动，待统一检查、定稿并合入

| 产出 | 通过标准 |
|------|----------|
| Compose | `docker compose up --build -d` 后 `:8012` 管理端可登录（Demo 容器可忽略） |
| Edge exe | 激活、连 Cloud、默认 IPP、**官方扫码**可打至 `completed` |
| 交接 / Guide | 系统说明 + 部署与验证（正式技术文档语气）；对接契约见第三方接入指南 |
| Demo 流（可选） | 扫码→SSO→提交→终端确认→打印→Demo `completed` |

工作方式：选未完成 P0（无则 P1）→ 对话复述完成定义 → 实现+测试 → 勾选 → 新风险写入「明确不做」或升为 P0/P1。  
状态：`[ ]` 未做 · `[~]` 进行中 · `[x]` 完成（`[x]` 细则见根目录 `FlyPrint任务清单.md`「用法」第 4 条：单测通过 ≠ 已合入 ≠ 已打包/已预演）

## P0

### P0-1 Edge：重启 unconfirmed 补终端上下文

- [x] `_recover_inbox_jobs` 对 `processing` 中断上报带回 `terminal_session_id` / `terminal_ticket_hash` / `integration_request_id`
- [x] 单测：集成中断恢复后 Cloud 不因 `terminal_context_mismatch` 拒绝
- [x] 合入后打安装包（建议 bump 版本）→ `dist/flyprint-edge-setup-1.0.38.exe`（产物不入库；P0-1 历史产物）。当前 dual-compat 分支安装包为 **1.0.39**；占用 UX 收口 **1.0.43**

**完成：** 第三方打印中 Edge 重启后终态可被 Cloud 接受；Demo 不永久卡 dispatched/printing。  
**文件：** Edge `cloud_websocket_client.py`、`job_delivery_store.py`、相关 tests。

### P0-2 演示环境就绪清单（写入 Guide）

- [x] `.env` 密钥替换说明
- [x] Compose 健康检查 `/health`、`/api/v1/health`
- [x] 管理端确认 `livacloud-demo`（entry/callback/CIDR/file host/`allow_private_file_hosts`）
- [x] HMAC 密钥粘贴 `/integration-demo/setup`
- [x] Edge `cloud.base_url` = 手机可达局域网 IP（禁 localhost）
- [x] Edge 激活 + 默认 IPP

**完成：** 干净环境按清单可到 Demo 提交页（清单见 Guide；现场实跑属 P0-4）。

### P0-3 第三方接入指南（对接契约）

- [x] 边界：第三方不直连打印机；须终端确认
- [x] 入口：扫码→`/entry`→ticket→provider `entry_url`
- [x] `POST /api/v1/integrations/{code}/print-requests` + HMAC
- [x] 文件主机白名单与私网精确主机
- [x] 状态机与 callback
- [x] 一次交换清单 + 局域网特例（原简要/公网文已合并为 `docs/第三方接入指南.md`）
- [x] README/AGENTS 链到指南

**完成：** 对方可凭对接契约独立开发；运维侧一次 Provider + HMAC 互配。

### P0-4 产物与预演

- [x] Compose up，Demo 健康
- [x] P0-1 后出 Edge 安装包（历史 `1.0.38`；dual-compat `1.0.39`；占用 UX 收口 `1.0.43`，本地 `dist/`）
- [x] 官方扫码打印 1 次（2026-07-22 verify）
- [x] Demo 全流程到完成 1 次（2026-07-22 verify）
- [x] 交付：Compose 说明 + exe + Guide + 本文件勾选结果

## P1（有时间再做）

- [x] P1-1 票据时效提示（ticket ~5min vs Demo 文件 URL ~10min）
- [x] P1-2 Demo setup 未配置/成功态更醒目
- [x] P1-3 管理端「设置」隐藏或标明未开放（无占位入口；下拉已清理）
- [x] P1-4 交付说明写清端口 8012、Demo 路径、默认管理员来源（现见 `docs/部署与验证.md` / `docs/系统说明.md`）
- [x] P1-5 http(s)/ws(s) 双兼容：第三方 URL + Edge `cloud.base_url` / WS 映射；已合入 `main`
- [x] P1-6 一机占用：进门遮挡二维码 + 刷新回收 + 官方/三方上传会话校验（WS `terminal_occupied` + ACK；已合入 `main`；安装包 **1.0.43**）

## 明确不做（本发版日 / M0）

以下 **M0 当天不做**；长期归属见根目录 `FlyPrint开发计划.md`：CI→M3；MinIO 钉版本→M3；`content_hash` 重算→M5；Users/Settings→M2；UNCONFIRMED 静默重打→禁止（策略产品化→M5）；生产强制 HTTPS/Keycloak→M2（**双兼容已做**，强制策略另议）。

CI；版本化 DB 迁移（文档或已过时，以代码 migrations 为准）；钉死 MinIO 非 latest；`content_hash` 端到端复算；Users/Settings 完整页；UNCONFIRMED 自动解锁（运维 `clear-unconfirmed`）；生产 HTTPS/Keycloak（演示用 builtin）。
