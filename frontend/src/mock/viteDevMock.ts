import type { Plugin } from 'vite'
import { WebSocketServer, WebSocket } from 'ws'
import type { IncomingMessage, ServerResponse } from 'http'
import type { PluginItem, PluginState, ServiceStatus } from '../types/api'

export function viteDevMock(): Plugin {
  return {
    name: 'vite-plugin-dev-mock',
    configureServer(server) {
      let status: ServiceStatus = 'running'
      let pid: number | null = 1043416
      let targetCommit = ''
      let lastMessage = ''
      let startedAt = Math.floor(Date.now() / 1000) - 7200

      const config = {
        server_port: 2298,
        proxy_port: 2299,
        heap_memory_limit: 0,
        network_proxy: '',
        proxy_dsh_runtime: false,
        npm_registry: 'https://registry.npmmirror.com',
        access_mode: 'fngateway',
        reverse_proxy_url: '',
        access_password: '',
        enable_builtin_skill: true,
        data_library_path: '/vol1/@appdata/deepseek.harness',
        version: '0.2.8-4',
        commit: '7b8f9a2',
        build_time: '2026-08-31 16:00'
      }

      const workspaces = [
        {
          workspaceId: 'ws-main-deepseek',
          title: 'DeepSeek 智能助手项目',
          path: '/vol1/1000/Projects/deepseek-assistant',
          sessionIds: ['sess-001', 'sess-002', 'sess-003'],
          createdAt: new Date(Date.now() - 1000 * 60 * 60 * 24 * 3).toISOString(),
          updatedAt: new Date(Date.now() - 1000 * 60 * 15).toISOString()
        },
        {
          workspaceId: 'ws-fnos-plugin',
          title: 'FNOS 自动化运维脚本',
          path: '/vol1/1000/Projects/fnos-ops',
          sessionIds: ['sess-101'],
          createdAt: new Date(Date.now() - 1000 * 60 * 60 * 48).toISOString(),
          updatedAt: new Date(Date.now() - 1000 * 60 * 60 * 2).toISOString()
        }
      ]

      let plugins: PluginItem[] = [
        {
          name: '@deepseek-ai/dsh-settings',
          version: '0.2.6',
          spec: 'workspace:*',
          state: 'live',
          layer: true,
          entryIds: ['dsh-settings'],
          description: '系统核心配置与设置基础设施组件。',
          author: 'DeepSeek AI',
          homepage: 'https://github.com/deepseek-ai/deepseek-harness',
          license: 'MIT',
          keywords: ['core', 'settings', 'infra'],
          isProtected: true,
          hasBundle: true
        },
        {
          name: 'dsh-better-sidebar',
          version: '0.12.2',
          spec: '^0.12.2',
          state: 'live',
          layer: true,
          entryIds: ['better-sidebar'],
          description: '增强型侧边栏管理工具，提供会话分组、快捷置顶与工作区快速切换功能。',
          author: 'DeepSeek Community',
          homepage: 'https://github.com/dsh-market/dsh-better-sidebar',
          license: 'MIT',
          keywords: ['sidebar', 'ui', 'workspace'],
          isProtected: false,
          hasBundle: true
        },
        {
          name: 'dsh-tool-web-search',
          version: '0.8.5',
          spec: '^0.8.0',
          state: 'live',
          layer: true,
          entryIds: ['tool-web-search'],
          description: '为大模型提供实时联网搜索能力的扩展工具。',
          author: 'DeepSeek AI',
          homepage: 'https://github.com/deepseek-ai/deepseek-harness',
          license: 'MIT',
          keywords: ['search', 'tool', 'network'],
          isProtected: false,
          hasBundle: true
        },
        {
          name: '@dsh-external/dsh-client-ui-skin-maid-atelier',
          version: '1.0.4',
          spec: 'github:Small-tailqwq/dsh-deep-whale#path:/maid-atelier',
          state: 'disabled',
          layer: false,
          entryIds: ['maid-atelier-skin'],
          description: '女仆工坊定制深色主题皮肤包。',
          author: 'Small-tailqwq',
          license: 'GPL-3.0',
          keywords: ['theme', 'skin', 'ui'],
          isProtected: false,
          hasBundle: true
        }
      ]

      let logs: string[] = [
        '2026-08-21 16:00:00 [INFO] 启动 DeepSeek Harness 守护服务',
        '2026-08-21 16:00:01 [INFO] 服务主进程已拉起 (PID=1043416)，正在等待 Web 服务就绪...',
        '2026-08-21 16:00:03 [INFO] Web 服务就绪探测通过，反向代理启动完成 [0.0.0.0:2299 → http://127.0.0.1:2298]',
        '2026-08-21 16:00:03 [INFO] [状态变更] stopped → running',
        '2026-08-21 16:00:04 [INFO] HTTP 服务已就绪，正在监听 Socket'
      ]

      let activePluginTask: {
        timer: NodeJS.Timeout | null
        verb: string
        specs: string[]
      } | null = null

      function getStatusPayload() {
        return {
          name: 'DeepSeek Harness',
          version: config.version,
          app_version: '0.2.8-4',
          app_remote_version: '0.2.8-5',
          app_has_update: true,
          commit: config.commit,
          target_commit: targetCommit,
          status,
          uptime: status === 'running' ? formatUptime(Math.floor(Date.now() / 1000) - startedAt) : '-',
          started_at: status === 'running' ? startedAt : 0,
          server_port: config.server_port,
          server_time: Math.floor(Date.now() / 1000),
          build_time: config.build_time,
          app_url: `/app/deepseek-harness/`,
          pid,
          last_message: lastMessage
        }
      }

      function formatUptime(seconds: number): string {
        if (seconds <= 0) return '0秒'
        const h = Math.floor(seconds / 3600)
        const m = Math.floor((seconds % 3600) / 60)
        const s = seconds % 60
        if (h > 0) return `${h}小时${m}分${s}秒`
        if (m > 0) return `${m}分${s}秒`
        return `${s}秒`
      }

      function appendLog(line: string) {
        const fullLine = `${new Date().toISOString().replace('T', ' ').substring(0, 19)} ${line}`
        logs.push(fullLine)
        broadcast('log', fullLine + '\n')
      }

      // WebSocket 仿真服务
      const wss = new WebSocketServer({ noServer: true })
      const clients = new Set<WebSocket>()

      function broadcast(type: string, data: unknown) {
        const payload = JSON.stringify({ type, data, timestamp: Date.now() })
        clients.forEach((client) => {
          if (client.readyState === WebSocket.OPEN) {
            client.send(payload)
          }
        })
      }

      wss.on('connection', (ws) => {
        clients.add(ws)
        ws.send(JSON.stringify({ type: 'status', data: getStatusPayload(), timestamp: Date.now() }))
        ws.send(JSON.stringify({ type: 'workspace', data: { items: workspaces, archivedSessionIds: [] }, timestamp: Date.now() }))
        ws.send(JSON.stringify({ type: 'plugin', data: { running: activePluginTask !== null }, timestamp: Date.now() }))

        ws.on('message', (msg) => {
          try {
            const parsed = JSON.parse(msg.toString())
            if (parsed.type === 'ping') {
              ws.send(JSON.stringify({ type: 'pong', data: { server_time: Math.floor(Date.now() / 1000) }, timestamp: Date.now() }))
            }
          } catch {
            // 忽略无效消息
          }
        })

        ws.on('close', () => clients.delete(ws))
      })

      if (server.httpServer) {
        server.httpServer.on('upgrade', (req, socket, head) => {
          if ((req.url || '').includes('/api/ws')) {
            wss.handleUpgrade(req, socket, head, (ws) => wss.emit('connection', ws, req))
          }
        })
      }

      function sendJson(res: ServerResponse, code: number, message: string, data: unknown) {
        res.setHeader('Content-Type', 'application/json; charset=utf-8')
        res.end(JSON.stringify({ code, message, data, timestamp: Date.now() }))
      }

      function readJsonBody(req: IncomingMessage): Promise<any> {
        return new Promise((resolve) => {
          let body = ''
          req.on('data', (chunk) => (body += chunk))
          req.on('end', () => {
            try {
              resolve(body ? JSON.parse(body) : {})
            } catch {
              resolve({})
            }
          })
        })
      }

      // API 路由拦截
      server.middlewares.use(async (req, res, next) => {
        const url = req.url || ''
        if (!url.includes('/api/')) return next()
        const path = url.split('?')[0]

        // 状态与配置快照
        if ((path.endsWith('/api/status') || path.endsWith('/api/config')) && req.method === 'GET') {
          if (path.endsWith('/api/config')) {
            return sendJson(res, 0, 'success', config)
          }
          return sendJson(res, 0, 'success', getStatusPayload())
        }

        // 服务控制动作
        if (path.endsWith('/api/action') && req.method === 'POST') {
          const body = await readJsonBody(req)
          const action = body.action

          if (action === 'stop') {
            status = 'stopped'
            pid = null
            lastMessage = ''
            appendLog('[INFO] 收到停止指令，服务已正常停止')
            broadcast('status', getStatusPayload())
            return sendJson(res, 0, '服务已停止', getStatusPayload())
          }

          if (action === 'start') {
            status = 'starting'
            lastMessage = '服务主进程已拉起，正在等待 Web 服务就绪...'
            appendLog('[INFO] 正在启动 DeepSeek Harness 应用进程...')
            broadcast('status', getStatusPayload())

            setTimeout(() => {
              status = 'running'
              pid = Math.floor(Math.random() * 100000) + 1000000
              startedAt = Math.floor(Date.now() / 1000)
              lastMessage = ''
              appendLog('[INFO] Web 服务就绪探测通过，状态恢复 running')
              broadcast('status', getStatusPayload())
            }, 1200)

            return sendJson(res, 0, '服务正在启动', getStatusPayload())
          }

          if (action === 'restart') {
            status = 'stopped'
            pid = null
            lastMessage = '正在终止旧进程并准备重启...'
            appendLog('[INFO] 正在停止 deepseek.harness 应用进程...')
            broadcast('status', getStatusPayload())

            setTimeout(() => {
              status = 'starting'
              lastMessage = '服务主进程已拉起，正在等待 Web 服务就绪...'
              appendLog('[INFO] 正在拉起新进程并监听端口...')
              broadcast('status', getStatusPayload())

              setTimeout(() => {
                status = 'running'
                pid = Math.floor(Math.random() * 100000) + 1000000
                startedAt = Math.floor(Date.now() / 1000)
                lastMessage = ''
                appendLog('[INFO] 服务重启完成，WebUI 已恢复响应')
                broadcast('status', getStatusPayload())
              }, 1200)
            }, 800)

            return sendJson(res, 0, '服务正在重启', getStatusPayload())
          }

          if (action === 'upgrade' || action === 'rebuild') {
            status = 'building'
            targetCommit = '9a3b8c1'
            lastMessage = action === 'upgrade' ? '正在拉取远程更新并编译...' : '正在强制重新编译项目源码...'
            appendLog(`[INFO] 开始执行 ${action === 'upgrade' ? '在线版本升级' : '强制源码重建'}`)
            broadcast('status', getStatusPayload())

            setTimeout(() => {
              appendLog('[INFO] 正在执行 pnpm install --prefer-offline...')
              setTimeout(() => {
                appendLog('[INFO] 正在编译前端与 CLI 核心产物 (pnpm run build)...')
                setTimeout(() => {
                  appendLog('[INFO] 编译完成，正在重启服务...')
                  status = 'starting'
                  lastMessage = '编译完成，正在拉起服务...'
                  broadcast('status', getStatusPayload())

                  setTimeout(() => {
                    status = 'running'
                    pid = Math.floor(Math.random() * 100000) + 1000000
                    startedAt = Math.floor(Date.now() / 1000)
                    targetCommit = ''
                    lastMessage = ''
                    config.version = '0.2.7'
                    config.commit = '9a3b8c1'
                    config.build_time = new Date().toISOString().replace('T', ' ').substring(0, 16)
                    appendLog('[INFO] [状态变更] building → running: 构建完成并成功拉起')
                    broadcast('status', getStatusPayload())
                  }, 1000)
                }, 1000)
              }, 1000)
            }, 1000)

            return sendJson(res, 0, '已开始构建升级', getStatusPayload())
          }

          return sendJson(res, 0, '操作成功', getStatusPayload())
        }

        // 检查远程更新
        if (path.endsWith('/api/check-update') && req.method === 'GET') {
          await new Promise((r) => setTimeout(r, 600))
          return sendJson(res, 0, 'success', {
            has_update: true,
            current_version: config.version,
            remote_version: '0.2.7',
            current_commit: config.commit,
            remote_commit: '9a3b8c1d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b',
            remote_short_commit: '9a3b8c1',
            message: `发现新版本 [ v${config.version} (${config.commit}) → v0.2.7 (9a3b8c1) ]`
          })
        }

        // 工作区列表
        if (path.endsWith('/api/workspace/list') && req.method === 'GET') {
          return sendJson(res, 0, 'success', {
            items: workspaces,
            archivedSessionIds: []
          })
        }

        // 插件管理
        if (path.endsWith('/api/plugins') && req.method === 'GET') {
          return sendJson(res, 0, 'success', {
            profile: 'web',
            plugins,
            bundles: plugins.filter((p) => p.layer).map((p) => p.name)
          })
        }

        if (path.endsWith('/api/plugins/status') && req.method === 'GET') {
          return sendJson(res, 0, 'success', {
            running: activePluginTask !== null,
            ok: true,
            message: ''
          })
        }

        // 插件命令解析预览
        if (path.endsWith('/api/plugins/preview') && req.method === 'POST') {
          const body = await readJsonBody(req)
          const raw = (body.command || '').trim()

          if (!raw) {
            return sendJson(res, 0, 'success', { valid: false, ok: false, reason: '请输入插件命令' })
          }

          const parts = raw.split(/\s+/)
          if (parts[0] !== 'dsh' || parts[1] !== 'plugin') {
            return sendJson(res, 0, 'success', {
              valid: false,
              ok: false,
              reason: '请输入标准 dsh 命令，例如: dsh plugin --profile web add 包名'
            })
          }

          let profile = 'web'
          let verb = ''
          const specs: string[] = []

          for (let i = 2; i < parts.length; i++) {
            if (parts[i] === '--profile' && i + 1 < parts.length) {
              profile = parts[i + 1]
              i++
              continue
            }
            if (!verb) {
              verb = parts[i]
            } else {
              specs.push(parts[i])
            }
          }

          if (verb !== 'add') {
            return sendJson(res, 0, 'success', { valid: false, ok: false, reason: '仅支持添加插件指令 (add)' })
          }

          if (specs.length === 0) {
            return sendJson(res, 0, 'success', { valid: false, ok: false, reason: 'add 操作需要一个或多个包名' })
          }

          return sendJson(res, 0, 'success', {
            valid: true,
            ok: true,
            command: raw,
            verb,
            profile,
            specs
          })
        }

        // 插件启停切换
        if (path.endsWith('/api/plugins/toggle') && req.method === 'POST') {
          const body = await readJsonBody(req)
          const target = plugins.find((p) => p.name === body.name)

          if (!target) {
            return sendJson(res, 400, '插件不存在', null)
          }

          if (target.isProtected) {
            return sendJson(res, 403, `核心基础设施插件「${target.name}」受到保护，禁止更改启停状态`, null)
          }

          target.state = (body.enabled ? 'live' : 'disabled') as PluginState
          target.layer = body.enabled

          appendLog(`[INFO] [Cordis Patch] 已${body.enabled ? '启用' : '禁用'}插件 ${target.name}`)
          return sendJson(res, 0, `${body.enabled ? '已启用' : '已禁用'}插件「${target.name}」`, {
            name: target.name,
            enabled: body.enabled
          })
        }

        // 执行插件操作（安装 / 更新 / 卸载）
        if (path.endsWith('/api/plugins/run') && req.method === 'POST') {
          const body = await readJsonBody(req)
          const raw = (body.command || '').trim()

          if (activePluginTask) {
            return sendJson(res, 409, '插件操作正在进行中，请稍候', null)
          }

          const parts = raw.split(/\s+/)
          let verb = 'add'
          const specs: string[] = []

          for (let i = 2; i < parts.length; i++) {
            if (parts[i] === '--profile' && i + 1 < parts.length) {
              i++
              continue
            }
            if (['add', 'remove', 'update', 'install'].includes(parts[i])) {
              verb = parts[i]
            } else if (!parts[i].startsWith('--')) {
              specs.push(parts[i])
            }
          }

          // 受保护插件卸载拦截
          if (verb === 'remove' && specs.some((s) => plugins.find((p) => p.name === s)?.isProtected)) {
            return sendJson(res, 403, '核心基础设施插件受到保护，禁止卸载', null)
          }

          // 启动异步仿真长任务
          broadcast('plugin', { running: true })
          appendLog(`[INFO] 开始执行插件管理操作: verb=${verb}, specs=[${specs.join(', ')}]`)

          const timer = setTimeout(() => {
            if (verb === 'add') {
              specs.forEach((pkgName) => {
                const cleanName = pkgName.replace(/@latest$/, '').replace(/\^/g, '')
                if (!plugins.some((p) => p.name === cleanName)) {
                  plugins.push({
                    name: cleanName,
                    version: '1.0.0',
                    spec: pkgName,
                    state: 'live',
                    layer: true,
                    entryIds: [cleanName.replace(/[@/]/g, '-')],
                    description: cleanName === 'dshmarket'
                      ? 'DSH 官方第三方插件市场，支持图形化发现与一键安装扩展。'
                      : `社区扩展插件 ${cleanName}`,
                    author: 'Community',
                    license: 'MIT',
                    keywords: ['plugin', 'extension'],
                    isProtected: false,
                    hasBundle: true
                  })
                }
              })
              appendLog(`[INFO] 插件安装完成: ${specs.join(', ')}`)
            } else if (verb === 'update') {
              specs.forEach((pkgName) => {
                const p = plugins.find((item) => item.name === pkgName)
                if (p && p.version) {
                  const parts = p.version.split('.')
                  parts[parts.length - 1] = String(Number(parts[parts.length - 1] || 0) + 1)
                  p.version = parts.join('.')
                }
              })
              appendLog(`[INFO] 插件更新完成: ${specs.join(', ')}`)
            } else if (verb === 'remove') {
              plugins = plugins.filter((p) => !specs.includes(p.name))
              appendLog(`[INFO] 插件卸载完成: ${specs.join(', ')}`)
            }

            activePluginTask = null
            broadcast('plugin', {
              running: false,
              ok: true,
              message: '操作完成，重启服务后生效'
            })
          }, 2200)

          activePluginTask = { timer, verb, specs }

          return sendJson(res, 0, '已开始执行插件指令', { command: raw })
        }

        // 取消插件操作
        if (path.endsWith('/api/plugins/cancel') && req.method === 'POST') {
          if (activePluginTask?.timer) {
            clearTimeout(activePluginTask.timer)
            activePluginTask = null
            broadcast('plugin', { running: false, ok: false, message: '操作已被用户手动取消' })
            appendLog('[WARN] [插件管理] 收到取消请求，操作已被终止')
            return sendJson(res, 0, '已发送取消指令', null)
          }
          return sendJson(res, 400, '当前没有正在执行的插件操作', null)
        }

        // 运行日志
        if (path.endsWith('/api/logs') && req.method === 'GET') {
          return sendJson(res, 0, 'success', {
            lines: logs.map((l) => l + '\n'),
            content: logs.join('\n')
          })
        }

        if (path.endsWith('/api/logs') && req.method === 'DELETE') {
          logs = []
          return sendJson(res, 0, '运行日志已清空', true)
        }

        // 保存配置
        if (path.endsWith('/api/config') && req.method === 'POST') {
          const body = await readJsonBody(req)
          Object.assign(config, body)
          appendLog('[INFO] 应用设置已更新')
          return sendJson(res, 0, '应用设置保存成功', config)
        }

        next()
      })
    }
  }
}
