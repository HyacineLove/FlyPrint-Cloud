# 切片一 Compose 联调设计

## 目标

直接扩展现有 `docker-compose.yml`，让 Cloud、SSO Login Demo 与 Site Portal 可在同一 Compose 网络中启动，并允许 Windows 主机上的 Edge 完成切片一真实扫码联调。该修改是当前开发基线，后续切片继续覆盖演进，不建立第二套 Compose。

## 组成与地址

- `api` 保持现有服务，通过环境变量初始化一个默认 Site Portal。
- `sso-login-demo` 使用独立数据卷保存运维账号和演示用户，对主机发布 `8081`。
- `site-portal` 依赖 `api` 与 `sso-login-demo`，对主机发布 `8082`。
- 容器间调用使用 Compose 服务名。
- 手机浏览器与主机 Edge 使用可配置的外部基址；默认值仅适合本机浏览器，局域网扫码时由操作者填写实际可访问地址，仓库不保存真实 IP。
- PRP 地址在切片一只作为领取结果中的配置值，不启动 PRP 服务，也不触发文件请求。

## 启动与验证入口

- 在 `.env.example` 增加切片一所需的非敏感模板项。
- 在现有部署说明中增加启动命令、外部地址填写规则和人工检查步骤。
- `docker compose up --build -d` 启动全部组件。
- 通过 Compose 状态和服务健康接口确认 Cloud、SSO Login Demo、Site Portal 已就绪。
- Edge 仍从功能分支工作区在 Windows 主机运行，连接 Compose 暴露的 Cloud 地址。

## 联调数据流

1. 运维访问 Site Portal `/ops`，登录并创建用户。
2. Edge 生成二维码，手机进入 Cloud H5。
3. Cloud 自动跳转默认 Site Portal。
4. Site Portal 跳转 SSO Login Demo，用户登录后以一次性授权码返回。
5. Site Portal 向 Cloud 报告非私密身份与领取码。
6. Cloud 静默创建或复用用户，并向目标 Edge 下发 `portal_session_ready`。
7. Edge 向 Site Portal 原子领取身份和 PRP 访问凭证，公共会话只展示身份。

## 失败处理与边界

- 缺失必要外部地址或凭证时由组件配置校验明确失败，不增加备用地址或备用登录链路。
- Site Portal、SSO Login Demo 和 Cloud 的真实凭证只放在本地 `.env`；示例文件只含占位值。
- 不实现上传、PRP 文件列表、文件下载、预览或打印。
- 不改变 HMAC 第三方打印链路。

## 验证

- Compose 配置解析成功。
- 三项 Cloud 侧 Go 测试与构建通过。
- Edge 全量测试通过。
- 人工联调检查首次登录创建映射、重复登录复用映射、禁用用户拒绝、领取码单次消费，以及 Cloud/公共会话不出现 PRP 访问凭证。
