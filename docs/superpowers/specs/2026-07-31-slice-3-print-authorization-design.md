# 切片 3：额度授权、审计任务与 IPP 打印闭环设计

## 目标

官方或私有域用户在 Edge 完成本地预览并确认打印参数后，由 Cloud 校验当前用户、终端会话、Edge、打印机和额度。Cloud 允许后创建不依赖 Cloud 文件记录的审计任务；Edge 直接使用已经预览过的标准 PDF 执行 IPP 打印，并把实际消耗与终态持久上报 Cloud。

本切片只建立统一主链，不新增 Cloud 文件上传、文件下载、文件分发、替代打印协议或失败后的自动重打路径。

## 组件边界

### Edge

- 保存当前 Site Portal 用户、PRP 文件选择和标准 PDF 的会话绑定。
- 在用户确认时生成一次性 `confirmation_id`，同步请求 Cloud 授权。
- 授权拒绝时不调用 IPP；授权允许时只使用本机标准 PDF。
- 从 IPP `job-impressions-completed` 获取已完成印面数，按单双面和份数换算实际纸张数。
- 将打印终态、实际印面数、实际纸张数和实际额度消耗写入既有本地 `job_update` outbox，再等待 Cloud 持久化回执。
- 打印结果不明时不自动重打，也不伪造实际消耗。

### Cloud

- 使用 Edge 现有节点绑定 OAuth2 身份接收授权请求。
- 从 Cloud 已绑定的终端会话读取 Site Portal 与 Cloud 用户，不信任 Edge 自报的用户身份。
- 原子校验用户状态、Edge 状态、终端会话、打印机归属与状态、打印参数和可用额度。
- 按 `node_id + confirmation_id` 幂等创建一条统一审计任务并预扣额度。
- 不创建 `files` 记录、文件接管工作项、预览分发工作项、Cloud-to-Edge `print_job` 命令或第三方回调工作项。
- 在接收终态 `job_update` 时原子结算额度，并沿用现有单调终态和事件回执机制。

### Cloud Admin

- 用户页展示当前额度余额，并只向管理员提供正数额度增加操作。
- 打印任务页展示 Site Portal、用户、Edge、打印机、文件展示名、页数、份数、单双面、颜色、预扣额度、实际消耗、状态和错误。
- 不提供普通用户自助增加额度、周期重置、自动充值或负数扣减入口。

## 授权协议

Edge 调用：

`POST /api/v1/edge/{node_id}/print-authorizations`

该接口沿用节点绑定 OAuth2 凭证、`edge:printer` scope、节点身份匹配和节点启用检查。

请求字段：

```json
{
  "confirmation_id": "Edge 当前会话内生成的唯一标识",
  "terminal_session_id": "当前终端会话标识",
  "site_portal_code": "Edge 当前展示的 Site Portal，仅用于与 Cloud 绑定值比对",
  "local_file_id": "PRP 文件在当前 Edge 会话中的非秘密标识",
  "file_display_name": "管理端审计使用的文件展示名",
  "page_count": 3,
  "copies": 2,
  "paper_size": "A4",
  "color_mode": "mono",
  "duplex_mode": "longedge",
  "printer_id": "Cloud 打印机标识"
}
```

Cloud 不接收文件路径、文件 URL、文件字节、PRP 凭证、Cookie 或用户密码。

允许响应：

```json
{
  "allowed": true,
  "job_id": "Cloud 审计任务标识",
  "reserved_quota": 4,
  "quota_balance": 46
}
```

拒绝响应包含稳定错误码和用户可显示消息，不返回可执行任务标识。重复的同一 `confirmation_id` 返回第一次创建的相同任务；相同标识携带不同请求内容时明确拒绝。

## 会话和身份绑定

Site Portal 登录完成时，Cloud 在同一事务中把 `site_portal_code` 和 `cloud_user_id` 写入当前 `edge_terminal_sessions` 记录。Edge 后续授权请求必须满足：

- OAuth2 凭证绑定的节点与路径 `node_id` 相同；
- `terminal_session_id` 与该节点当前会话相同；
- 会话已绑定 Site Portal 和 Cloud 用户；
- 请求中的 `site_portal_code` 与 Cloud 会话绑定值相同；
- Cloud 用户仍为启用状态；
- 打印机存在、启用、属于该节点，且状态允许接收任务。

## 额度模型与计算

额度单位为“点”。静默映射的新用户只在首次创建时获得一次 50 点初始额度，不按日或其他周期恢复。

每张实体纸的额度倍率：

- 黑白：1 点；
- 彩色：2 点。

授权预扣纸张数：

- 单面：`page_count × copies`；
- 双面：`ceil(page_count ÷ 2) × copies`。

授权预扣额度：

`预扣纸张数 × 颜色倍率`

部分打印时，Edge 使用 IPP 的 `job-impressions-completed` 计算实际用纸。对每份文件分别计算，避免奇数页的上一份与下一份错误共用一张纸：

```text
完整份数 = 已完成印面数 ÷ 每份页数
剩余印面数 = 已完成印面数 % 每份页数

单面实际纸张 = 已完成印面数
双面实际纸张 =
  完整份数 × ceil(每份页数 ÷ 2)
  + ceil(剩余印面数 ÷ 2)
```

实际消耗额度为 `实际纸张数 × 颜色倍率`，且不得超过该任务预扣额度。

结算规则：

- IPP 明确完成：按完整任务实际纸张数结算，返还预扣差额；
- IPP 提交前明确失败：实际消耗为 0，全额返还；
- IPP 打印中明确失败或取消，且存在已完成印面数：扣除实际消耗，返还其余额度；
- 结果不明或打印中失败但无法取得已完成印面数：保持预扣，不自动返还；管理员核对后可通过增加额度补偿；
- 相同终态事件重复到达只结算一次；
- 相互冲突的终态沿用现有单调终态规则拒绝。

## 数据模型

### 用户额度

`users` 增加非负 `print_quota_balance`，数据库默认值为 0。静默映射创建用户时在同一事务内发放 50 点；迁移时只为已经存在于 `external_identities` 的映射用户补发一次 50 点，普通 Cloud 管理账号不自动获得打印额度。

`print_quota_transactions` 保存：

- 用户；
- 关联任务（可空）；
- 类型：`initial_grant`、`admin_grant`、`authorization_reserve`、`print_refund`；
- 正负变动值；
- 变动后余额；
- 管理员标识和备注（仅管理员增加时使用）；
- 创建时间。

余额变更和流水写入必须位于同一数据库事务。

### 统一审计任务

`print_jobs` 保留现有主表和状态处理，增加：

- `site_portal_code`
- `terminal_session_id`
- `confirmation_id`
- `local_file_id`
- `quota_reserved`
- `quota_consumed`
- `impressions_completed`
- `sheets_completed`

统一任务的 `file_path`、`file_url` 和 Cloud 文件关联保持为空。`node_id + confirmation_id` 建立唯一约束。

## Edge 本地执行

预览生成的标准 PDF 已存在 `DocumentPipeline` canonical cache。授权允许后，Edge 按当前 `content_hash`、文件展示名和打印参数重新取得同一个 canonical cache 项，生成本次 IPP 作业 PDF；不得重新向 PRP 或 Cloud 下载文件。

Edge 使用新的本地编排模块完成：

1. 锁定当前会话的单次确认；
2. 请求 Cloud 授权；
3. 将返回的 `job_id` 绑定当前会话；
4. 调用现有 `IppPrintService`；
5. 将进度发送给本地页面；
6. 将终态和额度结算字段写入既有持久化 outbox；
7. 等待 Cloud `job_update_ack` 后删除 outbox 事件。

## 错误处理

- 授权参数错误、会话错误、用户禁用、额度不足、Edge/打印机不可用均返回稳定错误码，Edge 显示结果且不进入 IPP。
- Cloud 授权响应丢失时，Edge 以同一个 `confirmation_id` 查询同一结果，不生成第二个确认标识。
- Edge 获得允许结果后，任何无法确定是否已提交 IPP 的情况进入 `unconfirmed`，不自动重打。
- Cloud 无法持久化终态时不发送接受回执，Edge outbox 保留事件并按既有投递规则重送；这只重送审计事件，不重送打印任务。

## 测试与验收

Cloud 自动化测试覆盖：

- 新静默用户获得一次 50 点；
- Admin 只能增加正数额度并留下流水；
- 黑白/彩色、单面/双面和奇数页多份预扣计算；
- 额度允许、额度不足、禁用用户、错误 Edge、错误会话、错误 Site Portal 和错误打印机；
- 同一确认幂等、冲突确认拒绝；
- 创建任务时没有 Cloud 文件记录和文件工作项；
- 完成、提交前失败、部分失败和结果不明的额度结算；
- 重复终态不重复返还，冲突终态不改写结果。

Edge 自动化测试覆盖：

- 授权拒绝不调用 IPP；
- 授权允许只提交一次；
- 使用预览阶段同一 canonical PDF，不重新下载；
- 单双面、多份和部分印面的实际纸张换算；
- 明确失败上报实际消耗，结果不明不自动重打；
- 终态先落 outbox，Cloud 回执后再清理。

联调分别完成一次官方和私有域成功打印，并验证额度不足、用户禁用、重复确认、明确失败返还和 Cloud Admin 审计展示。
