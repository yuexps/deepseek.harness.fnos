---
name: trim-shared
description: 当任务涉及登录、连接目标、session 回落、wrapper 用法、真机验证或通用安全规则时使用
---

# trim-shared

## 什么时候看这个 skill

- 用户要登录、登出，或排查为什么连不上 NAS
- 用户没有显式给 `--host` / `--port` / `--scheme` / TLS 相关参数，需要判断 session 回落逻辑
- 用户要做真机验证
- 用户的问题跨多个模块，需要先确认通用规则

## 先看哪里

- 任务总入口：`../SKILL.md`
- 任务索引：`../reference/_index.md`
- 真机验证：`../reference/workflows/device-validation.md`

## 核心规则

- 默认连接目标是 loopback `http://localhost:5666`
- 可通过全局 `--profile <name>` 为命令选择独立 session profile
- `--scheme auto` 且未显式传 `--port` 时，loopback 默认 `http:5666`，远程 IP 默认 `https:5667`，远程域名默认 `https:443`
- 显式传 `--port` 时端口优先，CLI 只解析协议选择
- 未显式传连接参数时，可回落到本地已保存 session 的 `host`、`port`、`scheme` 和 TLS 设置
- 传 `--profile <name>` 时，session 回落、登录写入和登出清理都只作用于该 profile
- 内网或自签证书 HTTPS 目标可能需要显式传 `--tls-insecure`
- 远程明文 `http://` 需要显式传 `--allow-insecure-http`
- session 默认使用平台安全存储；只有在测试或 CI 隔离时才显式使用 `TRIM_CLI_SESSION_STORAGE=file`
- 如需在安全存储写失败时人工确认低信任降级，可显式使用 `TRIM_CLI_SESSION_STORAGE=ask-file`
- DSH 沙箱环境下默认仅允许写入当前工作区；全局凭据统一由 `$HOME/.config/trim-cli` 托管，**严禁覆盖 `TRIM_CLI_CONFIG_DIR`**。若执行写操作或文件下载遇到沙箱访问拒绝（`[sandbox: file access denied]`），可通过 `sandbox_permissions="danger-full-access"` 申请用户单次授权重试。
- 登录使用 OAuth PKCE 授权，不要索取或猜测 NAS 账号密码
- 生产环境只运行一次交互式 `login`：用户自行打开授权链接、登录并授权，再把一次性 code 粘贴
  回 CLI 提示；不要自动填写账号密码、点击授权或读取页面 code
- access token 临近过期时会自动 refresh；必要时可执行 `login --refresh`
- 自动 refresh 前会先确认当前 profile 的 session 存储可写；探测失败时保留旧 session，不发送
  refresh。并发进程会按 profile 复用较新的 session，不覆盖已轮换的 token
- 连接失败时，应明确提示正在尝试的 `host:port`
- profile 名必须非空，且只能包含 ASCII 字母、数字、`_`、`-`、`.`
- 输入 guard 遇到控制字符、空值或过长值时会直接拒绝，不会静默改写

## 常用命令

```bash
trim-cli --profile home --host <host> --port <port> login
trim-cli --profile home --host <host> --port <port> login --no-open
trim-cli --host <host> --scheme https --tls-insecure login
trim-cli --profile home system info
trim-cli --profile home raw appcgi.system.getInfo --json '{"language":"zh-CN"}'
trim-cli --profile home raw appcgi.network.ssh.switch --json '{"enable":true}' --yes
trim-cli --profile home logout
```

`raw` 采用默认保护策略：只有 CLI 内置、已审计的只读端点可以省略 `--yes`；其他端点及
未知端点都必须显式传 `--yes`。不要根据 endpoint 名称猜测它是否只读。
