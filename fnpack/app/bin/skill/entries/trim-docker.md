---
name: trim-docker
description: 当任务涉及 Docker 镜像、容器、Compose、启停删除或长耗时 Docker 操作时使用
---

# trim-docker

## 什么时候看这个 skill

- 用户要看镜像、容器、Compose 项目
- 用户要拉镜像、启停容器、删除镜像或容器
- 你需要先区分观察类命令和变更类命令

## 先看哪里

- Docker 模块 reference：`../reference/dockermgr.md`

## 核心提醒

- 先观察，再变更；常见入口是 `docker stats`、`image ls`、`container ls`
- `container ls` 默认只列运行中容器；检查 stopped/created 容器或判断容器是否消失时使用 `container ls --all` 或 `container inspect <id>`
- `container top` 只适用于运行中的容器；执行前先用 `container inspect <id>` 确认 `State.Running=true`
- `image pull`、`container stop`、`container restart` 往往比读请求慢
- `image pull` 与 `container create/update/start/stop/restart/kill` 必须显式传 `--yes`；删除命令在 Agent 或非交互流程中也传 `--yes`，且 `--force` 不等于 `--yes`
- `container update` 可能返回新的容器 ID；旧固件会回查资源字段和网络配置。后续操作使用命令输出的 ID
- 镜像引用和容器 ID 不要混用
- 固定命令缺失的 `appcgi.dockermgr.*` 端点使用 `docker request` 并始终传 `--yes`；system/network/compose 写操作前先做只读探测

## 常用命令

```bash
./scripts/trim-cli docker stats
./scripts/trim-cli docker image ls
./scripts/trim-cli docker image pull <imageRef> --yes
./scripts/trim-cli docker container ls
./scripts/trim-cli docker container ls --all
./scripts/trim-cli docker compose ls
./scripts/trim-cli docker request appcgi.dockermgr.networkList --json '{}' --yes
```
