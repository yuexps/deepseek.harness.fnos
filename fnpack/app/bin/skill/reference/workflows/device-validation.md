# 真机验证 workflow

这个 workflow 统一真机验证时的 OAuth 流程、最小探测步骤和写后回归口径。

## 1. OAuth 授权

生产环境真机验证只使用交互式 OAuth PKCE 登录：

1. 在交互终端运行 `trim-cli --profile <profile> --host <host> --port <port> login`
2. CLI 输出授权链接并保持等待
3. 用户自行打开链接，在浏览器输入账号密码并确认授权
4. 用户复制页面显示的一次性 code，粘贴到 CLI 的 `Authorization code` 提示
5. CLI 完成 token 交换并保存当前 profile 的 session

Agent 不索取或代填账号密码，不自动点击授权，不读取浏览器页面上的 code，也不把 code 放进
命令参数。CLI 无法自动打开浏览器时添加 `--no-open`，其他步骤不变。授权 URL 不追加
`redirect_uri`。

## 2. 固定先做的最小探测

开始任何写操作前，先做最小只读探测：

1. 确认上述交互式 `login` 已成功建立目标 profile 的 session；不要在验证流程中预置或轮换账号
2. 按目标模块执行 1 到 2 个只读命令确认连接和权限
3. 如果是文件相关，优先用 `file ls` 或对应只读搜索
4. 如果是存储相关，优先用 `storage pools` / `storage disks`
5. 如果是 Docker 相关，优先用 `docker stats` 或 `docker container ls`

目的：

- 确认连接目标正确
- 确认 session 已建立
- 确认当前账号权限是否符合本次操作

## 3. 写操作执行顺序

建议顺序：

1. 先做与目标最接近的只读探测
2. 再做单一、可解释、可回滚的写操作
3. 写完立即补一条只读命令确认结果
4. 如有临时资源，按需要清理

不要在同一轮验证里串多个不相关写操作。

## 4. 写后回归

至少补以下一种回归：

- 文件：重新 `ls` / `search` / `share list`
- 存储：重新 `pools` / `disks` / `removable`
- Docker：重新 `image ls` / `container ls` / `compose ls`
- 用户：重新 `user list` / `group list`

如果写前和写后状态无法客观对比，不应宣称验证通过。

## 5. 什么时候必须停下来确认

- OAuth 登录、授权或 code 交换失败
- 用户没给目标主机，但要求真机验证
- 写操作可能影响生产数据或正在运行的服务
- 实际返回与文档约束明显不一致，怀疑设备版本差异

## 6. 下一步

- 验证命令方向已明确：回对应模块 reference 看字段和端点
- profile、权限或设备状态不明：先停下来补信息，不要继续猜
