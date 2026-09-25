---
name: trim-user
description: 当任务涉及认证、用户、用户组、登录设备，或需要先确认当前账号与权限时使用
---

# trim-user

## 什么时候看这个 skill

- 用户要登录、登出或检查账号信息
- 用户要看用户列表、用户组、登录设备
- 你不确定当前账号权限是否足够支撑后续操作

## 先看哪里

- 用户模块 reference：`../reference/user.md`
- 真机验证 workflow：`../reference/workflows/device-validation.md`

## 核心提醒

- 登录是多数真实操作的前置条件
- 真机验证只使用当前 profile 的 OAuth session；需要切换身份时显式选择对应 profile
- 登录使用 OAuth PKCE；不要生成账号密码、2FA 或手工 access-token 登录命令
- 生产环境只运行一次交互式 `login`；用户自行在浏览器登录和授权，再把 code 粘贴回 CLI 提示
- 固定命令缺失的 `user.*` 端点使用 `user request`；用户和登录设备写操作默认需要确认

## 常用命令

```bash
./scripts/trim-cli --host <host> --port <port> login
./scripts/trim-cli logout
./scripts/trim-cli user request user.checkNewUser --json '{"user":"cli2"}' --yes
```
