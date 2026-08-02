# FlyPrint Cloud — Agent

按需加载（勿整仓通读）：

| 任务 | 文档 |
|------|------|
| **全局计划（先读）** | 工作区根目录 `../FlyPrint总开发计划.md`、`../FlyPrint开发任务清单.md` |
| 差距对照 / 定稿数据流 | `../docs/02-私有域接入-现状与目标差距清单.md`、`../docs/diagrams/refactoring-change-map.drawio.png` |
| 现状基线 | `../docs/01-现状系统说明.md`、`../docs/diagrams/current-architecture.drawio.png`、`current-dataflow.drawio.png` |
| 协议 / 目录 / 第三方与 Demo | `docs/agent/architecture-and-protocols.md` |
| 启动 / 路由 / 测试命令 | `docs/agent/operations-and-verification.md` |
| Site Portal 身份协议 | `docs/agent/site-portal-identity-protocol.md` |
| 测试组织规则（新建测试先读） | `api/TESTING.md` |
| 人类部署入口 | `README.md`；细节见 `docs/03-部署与验证.md` |
| 历史归档 | `../FlyPrint-archive/README.md`（工作区外，默认不读，不参与完成判定） |

`../FlyPrint-archive/`（工作区外）是历史归档，**默认不读取、不参与范围与完成判定**；只有当前任务明确要求核对历史背景时才按需读取。旧私有域 `/Auth` / `target-dataflow` 口径见 `../FlyPrint-archive/workspace/superseded-private-domain-2026-07-30/`。

## 交付文档地图（关联放这里，勿回写进交付正文）

可对外 / 可交接的正文彼此**独立成册**，不要在交付文档里堆「相关文档」表或互相跳转。需要对照时由 Agent 按本表加载。

| 文档 | 读者 / 用途 | 与其它册的分工（仅 Agent 对照） |
|------|-------------|--------------------------------|
| `README.md` | 项目概览：L0～L3、能力边界、运行组成、当前状态 | 不写接口字段 → 第三方指南；不写安装步骤 → 部署与验证；L4+ 源码不入此册 |
| `docs/01-使用指南.md` | 用户 / 管理端 / Edge 本机：怎么点界面 | 不讲职责与派单 → 运维指南 |
| `docs/02-运维指南.md` | 四方角色、监控派单、报障时机 | 不讲逐步点屏 → 使用指南；现场截图式手册可并存 |
| `docs/03-部署与验证.md` | 安装、配置、公网 :80 / 局域网、检查单与排障 | 环境变量与网络；第三方接入填值细节 → 第三方指南 §8；产品边界 → README |
| `docs/04-第三方接入指南.md` | 已实现 HMAC 第三方接入的对接契约 | Demo `code`=`livacloud-demo`；部署变量 / 三类地址 → 部署与验证、README；Site Portal 身份链路 → `docs/agent/site-portal-identity-protocol.md` |
| 公网发布包 | 由交付方通过受控渠道提供；本仓不存放内部打包、安装和凭证工具 | — |

**改交付文档时：** 只改该册正文；跨册关联只更新本表（及 Edge `AGENTS.md` 中指向 Cloud 的条目），不要在交付 `.md` 末尾加「见某某文档」索引。

## 硬规则

- 改前定位：路由 → 请求模型 → handler → repository → 前端 → Cloud-Edge 全链路。
- 禁止未确认的兜底、替代链路或协议分支；改方案先对话确认。
- 可先写小 demo；合入后不得保留重复实现。
- 改 schema：在 `InitTables` 兼容旧实例，并补 repository/handler/测试/清理。
- 改 Cloud-Edge 协议：同步 `message.go`、序列化测试、Cloud provider test（`api/TESTING.md`）、Edge consumer test（`tests/README.md`）。协议以 Go 源码为准，Swagger 不完整。
- 保留工作区已有改动；禁止 `docker compose down -v`（删卷）。
- 不提交密码、JWT/文件访问/MinIO 密钥或生产配置；`.env.example` 仅模板。
- 提交前检查 `git status --short`、相关 diff 与测试；源码变则更新受影响说明。
- **完成态**：`[x]` 仅表示已合入（及该项验收所要求的打包/预演）；「代码/单测通过」最多 `[~]`。细则见根目录 `../FlyPrint开发任务清单.md`「用法」第 4 条。
- **交付收口**：本轮 Cloud 有改动时，全部改完后 `docker compose up --build -d`（update），禁止 `docker compose down -v`（删卷）；仅需 API 时可 `up --build -d api`。Edge 有改动时再 bump 并打安装包（见 Edge `AGENTS.md`）。
