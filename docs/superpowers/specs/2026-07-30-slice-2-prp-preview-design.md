# 切片二：PRP 文件上传、列表与 Edge 本地预览设计

## 目标

切片二建立不经过 Cloud 的统一文件链路：

1. 官方用户登录 Site Portal 后，由浏览器将文件直接上传至官方 PRP。
2. 私有域用户使用组织 PRP 中属于自己的文件。
3. Edge 使用当前 Site Portal 会话中的 PRP 访问凭证查询、下载文件。
4. Edge 复用既有文档标准化与预览能力，在本地生成标准 PDF 和预览。

本切片不请求 Cloud 打印授权，不创建审计打印任务，也不调用 IPP。

## 实施顺序

采用端到端纵向推进：

1. 先完成 PDF 的登录凭证、浏览器直传、分页列表、Edge 下载和本地预览。
2. 在同一接口上补充图片和 DOCX。
3. 补齐容量限制、过期清理、最久未使用淘汰和异常恢复。
4. 完成官方与私有域两套配置联调。

不建立内存文件列表或 Cloud 下载等临时替代路径。

## 组件职责

### PRP Demo

PRP Demo 是单机、可完整演示的文件服务，负责：

- 验证 PRP 用户访问凭证和短期上传上下文；
- 接收浏览器直传；
- 使用 SQLite 保存文件元数据；
- 使用 Docker Volume 保存文件；
- 按用户隔离文件；
- 提供分页列表和受控下载；
- 管理上传临时文件；
- 执行容量限制、过期清理和最久未使用淘汰；
- 保护正在上传、下载及本次新上传的文件；
- 启动时清理残留临时文件并核对元数据与磁盘文件。

PRP Demo 不提供公开匿名文件地址。

### Site Portal

Site Portal 负责：

- 在身份登录成功后建立短期浏览器会话；
- 官方实例提供独立上传页面；
- 使用当前用户的 PRP 访问凭证向 PRP 换取短期上传上下文；
- 将 PRP 公共地址和上传上下文交给浏览器；
- 展示上传结果。

Site Portal 后端不读取、缓存或转发文件体。私有域实例可通过配置关闭上传入口。

### Edge

Edge 负责：

- 从当前 Site Portal 内存会话取得 PRP 地址和访问凭证；
- 分页查询当前用户文件；
- 向用户端展示列表、翻页、加载态和明确错误；
- 下载选中文件并校验响应；
- 将远端文件绑定为当前终端会话的本地 PRP 文件；
- 复用既有标准化、缓存和预览管线；
- 在换选、退出、下载失败或预览失败时清理未确认绑定和临时源文件。

### Cloud

Cloud 不新增文件列表、上传、下载、临时文件、对象存储或预览入口。Cloud 不接触 PRP 访问凭证、上传上下文和文件字节。

## Demo 认证边界

### 用户 PRP 访问凭证

SSO Login Demo 使用 HMAC-SHA256 签发短期 PRP Token。Token 至少包含：

- 版本；
- 签发者；
- 受众；
- Site Portal code；
- 稳定外部用户标识；
- 允许的 PRP 权限；
- 签发时间；
- 过期时间；
- 随机令牌标识。

PRP Demo 使用部署时配置的共享 Demo 密钥离线验证签名、受众、Site Portal code、权限和有效期。每个 PRP 实例只接受自身配置对应的 Site Portal code。

共享密钥只存在于 SSO Login Demo 与 PRP Demo 的运行环境中，不进入浏览器、Edge、Site Portal、Cloud、日志或仓库示例。

Edge 将 PRP Token 视为不透明 Bearer Token，不解析其格式。生产对接可以替换为 OAuth/OIDC Token、公私钥签名或 Token introspection，而不改变 Site Portal、Edge 和 Cloud 的协议边界。

### 浏览器上传上下文

浏览器不获得 Edge 使用的完整 PRP Token。

Site Portal 使用当前用户的 PRP Token 调用 PRP Demo 的上传上下文接口。PRP Demo 返回短期、仅允许上传、绑定用户的一次性上下文。Site Portal 将该上下文和 PRP 公共地址返回浏览器，浏览器直接向 PRP 上传。

上传上下文成功使用后立即失效，过期或重复使用返回明确错误。

## PRP API

### 创建上传上下文

`POST /api/v1/upload-contexts`

认证：`Authorization: Bearer <PRP Token>`

成功响应：

```json
{
  "upload_context": "opaque-one-time-context",
  "expires_at": "2026-07-30T12:05:00Z",
  "upload_url": "https://prp.example.test/api/v1/files"
}
```

### 上传文件

`POST /api/v1/files`

认证：`Authorization: Bearer <upload_context>`。该接口只接受短期上传上下文，不接受普通 PRP Token。请求使用 `multipart/form-data`，只允许一个文件字段。服务端以流式方式写入 `.part` 文件，同时计算大小和 SHA-256。

成功响应：

```json
{
  "file": {
    "id": "prp-file-id",
    "name": "document.pdf",
    "media_type": "application/pdf",
    "size": 12345,
    "sha256": "lowercase-hex-sha256",
    "created_at": "2026-07-30T12:00:00Z",
    "expires_at": "2026-08-06T12:00:00Z"
  }
}
```

### 查询文件列表

`GET /api/v1/files?page=1&page_size=20`

认证：`Authorization: Bearer <PRP Token>`

响应只包含当前用户文件：

```json
{
  "items": [],
  "page": 1,
  "page_size": 20,
  "total": 0
}
```

文件项包含 `id`、`name`、`media_type`、`size`、`sha256`、`created_at`、`expires_at` 和 `last_downloaded_at`。列表不返回带认证参数的下载地址。

### 下载文件

`GET /api/v1/files/{id}/content`

认证：`Authorization: Bearer <PRP Token>`

PRP 校验文件属于当前用户后流式返回内容，并通过 `Content-Disposition`、`Content-Type`、`Content-Length` 和 `X-Content-SHA256` 返回文件名、类型、长度和 SHA-256。只有完整传输成功后才更新 `last_downloaded_at`。

## 元数据与文件状态

SQLite 文件记录包含：

- 文件 ID；
- 所有者外部用户标识；
- 原始文件名；
- 媒体类型；
- 大小；
- SHA-256；
- 相对存储路径；
- 创建时间；
- 过期时间；
- 最后成功下载时间。

上传先写入独立临时目录。完成大小、允许类型和基础内容校验后，文件原子移动到正式目录，再提交可用元数据。未完成上传不会出现在列表中。

支持的基础演示类型：

- PDF；
- JPEG；
- PNG；
- DOCX。

服务不只依赖文件扩展名，至少联合校验声明类型、扩展名和基础文件签名。

## 容量与清理

PRP Demo 通过配置管理：

- 单文件最大大小；
- 单用户最大文件数；
- 单用户总容量；
- 文件保存时间；
- 全局总容量；
- 清理周期。

上传前与定时任务均可触发清理：

1. 删除过期文件；
2. 若当前用户容量仍不足，按最后成功下载时间淘汰该用户最久未使用文件；
3. 若全局容量仍不足，按相同规则从全局候选中淘汰；
4. 排除正在上传、正在下载和本次新上传的文件；
5. 清理后仍不足时拒绝上传。

没有下载记录的文件使用创建时间参与排序。元数据删除和磁盘删除失败必须返回或记录明确状态，不静默改走其他存储。

启动时删除残留 `.part` 文件，并处理元数据存在但磁盘文件缺失、磁盘正式文件无元数据的异常残留。

Docker Volume 只负责持久化目录；本阶段的容量上限由 PRP Demo 应用层执行。

演示环境默认值为：

- 单文件最大 50 MiB；
- 单用户最多 20 个文件；
- 单用户总容量 200 MiB；
- 全局总容量 2 GiB；
- 文件保存 7 天；
- 清理周期 5 分钟；
- 列表默认每页 20 条、最大每页 50 条。

所有数值均可通过环境变量调整。非正数或相互矛盾的配置导致服务启动失败，不静默替换为其他值。

## Site Portal 浏览器会话

身份回调成功后，Site Portal 创建随机浏览器会话标识，以 HttpOnly Cookie 返回。服务端内存会话保存：

- 外部用户标识；
- 显示名称；
- PRP 地址；
- PRP Token；
- Token 过期时间；
- 浏览器会话过期时间。

Cookie 不包含 PRP Token。浏览器会话与 Edge 一次性领取码相互独立，领取成功不会删除浏览器上传会话。服务重启后浏览器会话失效，用户需重新扫码登录。

浏览器会话 Cookie 使用 HttpOnly、SameSite=Lax，并在 HTTPS 部署时启用 Secure。Site Portal 创建上传上下文的接口只接受同源请求。

PRP Demo 的浏览器上传 CORS 仅允许配置中的 Site Portal 公共 Origin，允许上传所需的 `POST`、`OPTIONS`、`Authorization` 和 `Content-Type`，不使用通配 Origin。Edge 列表与下载不依赖 CORS。

## Edge 文件与预览流

### PRP 客户端

新增独立 `prp_client.py`，提供：

- `list_files(access_context, page, page_size)`；
- `download_file(access_context, file_id, destination)`。

客户端严格校验 PRP 基础地址、HTTP 状态、分页字段、文件 ID、文件名、媒体类型、大小和 SHA-256。PRP 基础地址只允许绝对 HTTP(S) 地址，不允许 userinfo、query 或 fragment；文件接口路径由客户端固定构造。认证只放在 Authorization 请求头中。

### 用户端接口

Edge 提供绑定当前终端会话的薄接口：

- 查询 PRP 文件列表；
- 选择并下载一个 PRP 文件；
- 查询或生成该本地文件的预览；
- 取消当前选择并清理。

浏览器不获得 PRP Token。每次操作必须匹配当前活动终端会话和 Site Portal 会话。

### 本地文件标识

下载成功后，Edge 创建不依赖 Cloud `file_id` 的本地 PRP 文件标识。该标识绑定：

- 当前终端会话；
- Site Portal；
- 远端 PRP 文件 ID；
- 文件 SHA-256；
- 本地临时源文件；
- 标准 PDF 或预览缓存。

文件标准化继续使用既有 `DocumentPipeline`、LibreOffice 转换和预览缓存，不建立第二套转换实现。

## 错误处理

PRP 和 Edge 使用稳定机器错误码与中文用户提示分离：

| 错误码 | 含义 |
|---|---|
| `auth_required` | 缺少凭证 |
| `token_invalid` | Token 签名、受众、Site Portal 或权限无效 |
| `token_expired` | Token 已过期 |
| `upload_context_invalid` | 上传上下文不存在或绑定错误 |
| `upload_context_expired` | 上传上下文已过期 |
| `upload_context_consumed` | 上传上下文已使用 |
| `unsupported_file_type` | 文件类型不支持或基础签名不匹配 |
| `file_too_large` | 文件超过单文件上限 |
| `user_file_limit` | 用户文件数量已达上限 |
| `user_capacity_exceeded` | 用户容量清理后仍不足 |
| `global_capacity_exceeded` | 全局容量清理后仍不足 |
| `file_not_found` | 文件不存在或不属于当前用户 |
| `invalid_pagination` | 分页参数无效 |
| `content_length_mismatch` | Edge 下载长度不一致 |
| `content_hash_mismatch` | Edge 下载 SHA-256 不一致 |
| `conversion_failed` | Edge 文档标准化失败 |
| `preview_failed` | Edge 本地预览失败 |

不添加 Cloud 文件下载、匿名下载、备用 PRP 地址或自动换源等兜底。

## 验证

### PRP Demo 自动化

- PRP Token 签发与篡改、错误受众、错误权限、过期验证；
- 上传上下文单次消费、过期和用户绑定；
- PDF、JPEG、PNG、DOCX 上传与下载；
- 大小、数量、用户容量和全局容量限制；
- 用户级及全局最久未使用淘汰；
- 正在上传和下载的文件保护；
- 过期清理和启动残留清理；
- 不同用户列表与下载隔离；
- 分页边界；
- 响应和日志不泄漏凭证。

### Site Portal 自动化

- 登录成功建立浏览器会话；
- Cookie 不包含 PRP Token；
- 未登录或会话过期不能创建上传上下文；
- 上传入口可按配置关闭；
- Site Portal 后端不接收文件体。

### Edge 自动化

- PRP 分页结构和地址校验；
- 凭证只进入 Authorization 请求头；
- 下载长度、大小、类型和 SHA-256 校验；
- PDF、图片和 DOCX 标准化；
- 换选、失败和退出清理；
- 浏览器状态、响应和日志不含 PRP Token；
- 切片一身份闭环回归。

### 联调

- 官方：运维创建用户 → 登录 → 浏览器直传 → Edge 列表 → 下载 → 本地预览；
- 私有域：组织登录 → Edge 列表 → 下载 PRP Demo 预置文件 → 本地预览；
- 检查 Cloud 和 Site Portal 后端没有文件内容、文件临时副本或带认证参数的完整地址。

## 非目标

- Cloud 打印授权和审计任务；
- IPP 打印；
- 多 Site Portal 选择；
- 旧打印链路删除；
- 对象存储、多实例协调、集群、高可用和生产级身份适配；
- 用户自行注册；
- Cloud 或 Site Portal 后端保存文件。
