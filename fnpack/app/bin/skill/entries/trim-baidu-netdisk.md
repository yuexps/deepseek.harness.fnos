---
name: trim-baidu-netdisk
description: 涉及百度网盘授权状态、空间、云端文件、分享链接保存、NAS 传输任务或同步任务时使用
---

# trim-baidu-netdisk

## 什么时候看这个 skill

- 检查 NAS 上的百度网盘应用是否已经完成百度账号授权
- 查看空间配额、浏览或搜索云端文件、读取文件元数据
- 创建、删除、重命名、移动或复制百度网盘文件
- 把百度分享链接中的文件保存到 NAS
- 在 NAS 与百度网盘之间创建上传、下载任务并管理任务状态
- 创建或管理定时同步任务

## 先看哪里

- 完整命令、字段和错误处理：`../reference/baidu-netdisk.md`
- fnOS 登录、profile 和连接问题：`trim-shared.md`

## 核心提醒

- 先运行 `baidu-netdisk status`。未授权时让用户在 NAS 的百度网盘应用登录页完成授权；CLI 不办理百度 OAuth。
- 百度网盘路径以 `/` 开头；NAS 路径必须是具体 `/vol{n}/...` 路径，两类路径不能混用。
- 文件变更、传输任务变更和同步任务变更都是写操作，必须显式传 `--yes`。
- `upload` 成功只表示上传任务已提交；接口正常返回 `data: null`。先查 `task list --state running`，再查 `--state completed` 获取最终结果。
- 上传任务出现 `status=6`、`failCode=42610` 时表示百度网盘空间不足；先释放空间或扩容，不要直接反复提交。
- `info --dlink` 返回的下载链接属于敏感临时 URL，CLI 输出会将 `dlink` 脱敏。
- `move` / `copy` 的最后一个位置参数是目标目录，前面的参数都是源路径。
- 保存分享链接时优先使用 `share-save --all`；需要精确选择且已知分享 fsid 时重复传 `--fsid`。两者不能同时使用。
- 分享链接中的提取码会自动识别，也可显式传 `--password`。不要在回复或日志中回显链接短码、提取码、`spwd` 或中间任务 ID。

## 常用命令

```bash
./scripts/trim-cli baidu-netdisk status
./scripts/trim-cli baidu-netdisk quota
./scripts/trim-cli baidu-netdisk ls / --limit 80
./scripts/trim-cli baidu-netdisk search <keyword> --dir / --recursive
./scripts/trim-cli baidu-netdisk info <fsid...>
./scripts/trim-cli baidu-netdisk mkdir /备份 --yes
./scripts/trim-cli baidu-netdisk upload /备份 /vol1/1000/data --yes
./scripts/trim-cli baidu-netdisk download /vol1/1000/downloads <fsid...> --yes
./scripts/trim-cli baidu-netdisk share-save '<shareUrlWithPassword>' /vol1/1000/downloads --all --yes
./scripts/trim-cli baidu-netdisk task list --transfer download
./scripts/trim-cli baidu-netdisk sync list
```
