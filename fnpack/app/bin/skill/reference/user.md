# 用户与认证参考

## OAuth 登录

trim-cli 使用 OAuth 2.0 Authorization Code + PKCE 建立代理 session，不使用 NAS 用户名密码、
2FA 登录或旧 token 恢复链。CLI 按用户、文件读写、下载、应用中心、相册、系统、日志、存储和
Docker 模块申请 `trim.*` scope；完整列表见 `oauth.md`。

生产环境只使用交互式登录：

```bash
trim-cli --profile home --host <host> --port <port> login
```

CLI 输出授权链接并在当前 profile 保存一次性 PKCE verifier，然后保持运行并等待
`Authorization code`。用户自行在浏览器输入账号密码、确认授权、复制页面显示的 code，再粘贴
到 CLI 提示。不要自动填写账号密码、点击授权或读取页面 code，也不要把 code 放进命令参数。
无法自动打开浏览器时添加 `--no-open`。授权 URL 不包含 `redirect_uri`，不要自行追加；token
交换成功后一次性 PKCE 文件会被删除。

Session 保存以下认证信息：

| Field | Meaning |
| --- | --- |
| `accessToken` | 代理业务请求的 Bearer token |
| `refreshToken` | access token 刷新凭据 |
| `accessTokenExpiresAt` | access token 的 Unix 秒过期时间 |
| `oauthClientId` | CLI 使用的 OAuth client id |
| `oauthDeviceId` | 本机持久化的 OAuth device id |
| `oauthScope` | 已授权 scope |

普通已认证命令会在 access token 临近过期时自动调用 refresh。也可以显式执行：

```bash
trim-cli --profile home login --refresh
```

refresh 响应没有返回新 refresh token 时，CLI 保留已有 refresh token。刷新前会先确认当前
profile 的 session 存储可写；预检失败时停止当前命令，不发送 refresh，也不静默切换到用户名
密码登录。多个进程同时恢复时，CLI 会复用已写入的新 session，不覆盖较新的 token。

`logout` 只清除当前 profile 的本地 session，不表示服务端 token 已撤销：

```bash
trim-cli --profile home logout
```

## 当前用户与列表

```bash
trim-cli user info
trim-cli user list
trim-cli user list --mode group --group Users
trim-cli user request user.list --json '{"limit":100,"offset":0}'
```

| Command | Endpoint | Important output |
| --- | --- | --- |
| `user info` | `user.info` | `userInfo.user`、`uid`、`admin` |
| `user list` | `user.list` | 用户数组、总数和分页字段 |
| `user list --mode group` | `user.listUG` | 指定组中的用户 |

`user request` 的 endpoint 必须以 `user.` 开头；`--json` 必须是 JSON object，且不能手工提供
`req` 或 `reqid`。通用 `user request` 无法从 endpoint 名称可靠判断读写属性，因此默认要求确认；
自动化时需明确传 `--yes`。已封装的 `user info`、`user list` 等专用只读命令不需要该参数。

## 用户写操作

```bash
trim-cli user add <user> [--password <initial-password>] [--groups Users] [--set-admin] --yes
trim-cli user mod <user> [--new-name <name>] [--password <new-password>] [--groups Users] --yes
trim-cli user del <user> --yes
trim-cli user change-password <user> --yes
trim-cli user unfreeze <user> --yes
trim-cli user set-admin <user> --admin true --yes
```

| Command | Endpoint | Main request fields |
| --- | --- | --- |
| `user add` | `user.add` | `user`、`password`、`groups`、可选联系信息和管理员标记 |
| `user mod` | `user.mod` | `user` 加显式提供的变更字段 |
| `user del` | `user.del` | `user` |
| `user change-password` | `user.changePassword` | `user`、交互输入的新密码 |
| `user unfreeze` | `user.unfreeze` | `user` |
| `user set-admin` | `user.setAdmin` | `user`、`admin` |

这里的 `--password` 是创建用户或修改目标用户密码，不是 CLI 登录凭据。不要把用户管理密码
改写成登录参数。密码不要出现在回复、日志或长期保存的命令记录中；省略可交互输入的密码参数。

删除用户、修改管理员权限、改密码和解冻属于写操作，未得到用户明确授权时不要执行。

## 用户组

```bash
trim-cli user group list
trim-cli user group info <group>
trim-cli user group users <group>
trim-cli user group add <group> [--comment <text>] --yes
trim-cli user group mod <group> [--new-name <name>] [--comment <text>] --yes
trim-cli user group del <group> --yes
trim-cli user group set-users <group> --users <user> --yes
trim-cli user group add-users <group> --users <user> --yes
trim-cli user group del-users <group> --users <user> --yes
```

| Command | Endpoint | Main request fields |
| --- | --- | --- |
| `group list` | `user.groupList` | 无 |
| `group info` | `user.groupInfo` | `group` |
| `group users` | `user.groupUsers` | `group` |
| `group add` | `user.groupAdd` | `group`、可选 `comment` |
| `group mod` | `user.groupMod` | `group`、可选新名称和备注 |
| `group del` | `user.groupDel` | `group` |
| `group set-users` | `user.groupSetUsers` | `group`、`users` |
| `group add-users` | `user.groupAddUsers` | `group`、`users` |
| `group del-users` | `user.groupDelUsers` | `group`、`users` |

`--users` 和 `--groups` 支持重复参数或逗号分隔值。空列表、控制字符和明显非法名称会在发请求
前被拒绝。

## 登录设备

```bash
trim-cli user login-devices
trim-cli user request user.listLoginDevice
```

登录设备列表是用户管理数据，不参与 CLI 的 OAuth PKCE 登录。不要根据设备列表尝试旧的
trusted-device/2FA 登录流程。

## 错误处理

- `saved proxy session is required`：先完成 OAuth 登录。
- OAuth pending 缺失或过期：重新运行完整的交互式 `login`，不要单独拼装兑换命令。
- refresh 失败或 refresh token 缺失：重新运行 OAuth 登录，不回退到账号密码。
- refresh 返回后 session 最终写入失败：不要盲目复用旧 refresh token；它可能已被轮换，应重新登录。
- 权限错误：先用 `user info` 确认 `admin`，不要自动提升权限。
- 写操作未确认：向用户说明目标和影响，得到授权后再传 `--yes`。
- 响应含业务错误码时原样报告错误语义，不要只依据 HTTP 状态判断成功。
