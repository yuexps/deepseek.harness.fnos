# 文件上传验证 workflow

这个 workflow 用来验证 `file upload` 的基本上传行为、同名策略和大文件断点缓存行为。

## 1. 前置条件

- 已经登录目标 NAS。
- 已经确认一个可写的 `/vol{n}/...` 目录。
- 本地待上传文件存在，并且不是目录。

## 2. 小文件验证

1. 在可写目录下创建一个临时子目录。
2. 上传一个小于 20 MiB 的本地文件：

```bash
./scripts/trim-cli file upload /vol1/1000/tmp-upload ./demo.txt --overwrite rename --yes
```

3. 列出临时目录：

```bash
./scripts/trim-cli file ls /vol1/1000/tmp-upload
```

4. 确认 CLI 输出的路径是用户请求的目标路径，目录列表中存在同名文件。

## 3. 大文件验证

1. 准备一个 20 MiB 或更大的本地文件。
2. 上传到临时目录：

```bash
./scripts/trim-cli file upload /vol1/1000/tmp-upload ./large.bin --overwrite rename --yes
```

3. 列目录确认远端存在原始文件名。
4. 如果需要排查断点续传，可检查本地配置目录中的上传缓存；缓存里的 `upload_path` 是后端返回的 `.~#n` 上传路径，不是用户最终看到的文件名。
5. 只有 endpoint、profile、登录用户、远端目标、大小、覆盖策略和本地 SHA-256 全部相同的缓存
   才允许续传。同一路径换成另一个同大小文件时必须从新的 `check-upload` 开始。
6. 并发验证同一 endpoint 和远端目标时，两个上传应跨进程串行；不要把锁等待误判为缓存损坏。

## 4. 清理

验证后删除上传文件和临时目录：

```bash
./scripts/trim-cli file rm /vol1/1000/tmp-upload/demo.txt --yes
./scripts/trim-cli file rm /vol1/1000/tmp-upload --yes
```

## 5. 判断标准

- `file upload` 成功输出使用请求的目标路径。
- `file ls` 能看到上传后的文件。
- 小文件不会依赖断点缓存。
- 大文件可以产生断点缓存，缓存路径用于 HTTP 上传和续传，不直接作为最终展示路径。
- legacy 缓存、身份字段不完整的缓存及本地文件指纹不匹配的缓存不会被续传。
