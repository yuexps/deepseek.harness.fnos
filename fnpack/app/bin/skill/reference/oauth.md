# OAuth 参考

## 固定参数

- `client_id`: `YJNMPJUGA9`
- `scope`: `trim.user.all trim.file.read trim.file.write trim.download.all trim.appcenter.all trim.photo.all trim.system.all trim.log.all trim.storage.all trim.docker.all`；产品专属 scope 见对应 reference
- `code_challenge_method`: `S256`
- device name/model: `trim-cli` / `CLI`
- 授权 code 有效期：5 分钟

## 登录

```bash
trim-cli --profile home --host <host> --port <port> login
```

生产环境只使用这一条交互式流程：CLI 输出 `/signin` 授权链接并保持运行；用户自行在浏览器
输入账号密码、确认授权、复制页面显示的一次性 code，再粘贴到 CLI 的
`Authorization code` 提示。CLI 随后完成 token 交换。

Agent 不代填账号密码、不自动点击授权、不读取页面 code，也不把 code 放进命令参数。CLI 无法
自动打开浏览器时可添加 `--no-open`，但用户授权和交互式输入步骤不变。

授权 URL 包含 PKCE challenge、client、scope 和 device 参数，不包含 `redirect_uri`。pending
verifier 按 profile 隔离，过期后必须重新运行完整的交互式 `login`。授权 code 只能交换一次。
`trim.basic` 不属于当前可申请列表，CLI 不显式申请。旧的 `file photo` scope 或缺少当前模块
所需 scope 的 session 不再兼容，必须重新登录；refresh token 不能用来扩展原授权范围。

## Refresh

```bash
trim-cli --profile home login --refresh
```

命令开始前 access token 在 60 秒内过期会自动 refresh。代理 `/ogh/ac/w` 或原生 HTTP
`/ogh/ac/h/*` 返回 401 时，CLI 按 profile 加锁 refresh 并只重试一次；第二次仍 401 时停止。
refresh 返回新 refresh token 时替换旧值，未返回时保留旧值。刷新前 CLI 会把当前 session 原值
写回当前 profile，确认真实存储目标可写；预检失败时不会发送 refresh 请求，因此不会消耗可能轮换的 refresh token。
`ask-file` 的存储选择只会询问一次，并沿用到本次刷新提交。多进程获得锁后会重新读取 session，
已被其他进程刷新时直接使用新 token，不覆盖较新的 session。

存储仍可能在请求发出后失效。若 refresh 成功但最终 session 写入失败，不要反复使用旧 refresh
token；服务端可能已完成轮换，应重新执行交互式 OAuth 登录。

## Session 与 logout

session 保存 access/refresh token、过期时间、OAuth client/device/scope 和用户摘要。
`logout` 清理当前 profile 的 session 和 pending 文件，不承诺远端撤销令牌。

失败时重新执行交互式 OAuth 登录；不回退到 CLI 用户名密码、2FA 或旧 token 恢复流程。授权
code、code verifier、access token 和 refresh token 不应出现在命令参数、输出、日志或错误信息中。
