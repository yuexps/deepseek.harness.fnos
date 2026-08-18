import type { Plugin } from 'vite'
import { WebSocketServer, WebSocket } from 'ws'
import type { IncomingMessage, ServerResponse } from 'http'
import type { PluginItem } from '../types/api'

export function viteDevMock(): Plugin {
  return {
    name: 'vite-plugin-dev-mock',
    configureServer(server) {
      let status = 'running'
      let pid: number | null = 1043416
      const startedAt = Math.floor(Date.now() / 1000) - 3600

      const config = {
        server_port: 2298,
        proxy_port: 2299,
        network_proxy: '',
        reverse_proxy_url: '',
        access_password: '',
        data_library_path: '/vol1/@appdata/deepseek.harness',
        version: '0.1.0-rc.6',
        commit: '7b8f9a2',
        build_time: '2026-08-18 16:00:00'
      }

      const workspaces = [
        {
          workspaceId: 'ws-main-dev',
          title: 'DeepSeek 核心研发项目',
          path: '/vol1/1000/Projects/deepseek-core',
          sessionIds: ['sess-001', 'sess-002'],
          updatedAt: new Date(Date.now() - 1000 * 60 * 30).toISOString()
        }
      ]

      let plugins: PluginItem[] = [
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

      let logs = [
        '2026-08-18 16:00:00 [INFO ] 启动 DeepSeek Harness 守护服务',
        '2026-08-18 16:00:01 [INFO ] 服务主进程已拉起 (PID=1043416)，正在等待 Web 服务就绪...',
        '2026-08-18 16:00:03 [INFO ] Web 服务就绪探测通过，反向代理启动完成 [0.0.0.0:2299 → http://127.0.0.1:2298]',
        '2026-08-18 16:00:03 [INFO ] [状态变更] stopped → running',
        '2026-08-18 16:00:04 [INFO ] dsh web: http://127.0.0.1:2298'
      ]

      function getStatusPayload() {
        return {
          name: 'DeepSeek Harness',
          version: config.version,
          commit: config.commit,
          status,
          uptime: status === 'running' ? '1小时0分0秒' : null,
          started_at: status === 'running' ? startedAt : 0,
          server_port: config.server_port,
          server_time: Math.floor(Date.now() / 1000),
          build_time: config.build_time,
          app_url: `:${config.proxy_port}/`,
          pid,
          last_message: ''
        }
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
        ws.send(JSON.stringify({ type: 'workspace', data: { workspaces, dataLibraryPath: config.data_library_path }, timestamp: Date.now() }))
        ws.send(JSON.stringify({ type: 'plugin', data: { running: false }, timestamp: Date.now() }))

        ws.on('message', (msg) => {
          try {
            const parsed = JSON.parse(msg.toString())
            if (parsed.type === 'ping') {
              ws.send(JSON.stringify({ type: 'pong', data: { server_time: Math.floor(Date.now() / 1000) }, timestamp: Date.now() }))
            }
          } catch {
            // 忽略格式错误
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

        // 1. 状态快照
        if (path.endsWith('/api/status') && req.method === 'GET') {
          return sendJson(res, 0, 'success', getStatusPayload())
        }

        // 2. 服务控制动作
        if (path.endsWith('/api/action') && req.method === 'POST') {
          const body = await readJsonBody(req)
          if (body.action === 'stop') {
            status = 'stopped'
            pid = null
          } else {
            status = 'running'
            pid = 1043416
          }
          broadcast('status', getStatusPayload())
          return sendJson(res, 0, '操作成功', getStatusPayload())
        }

        // 3. 工作区
        if (path.endsWith('/api/workspaces') && req.method === 'GET') {
          return sendJson(res, 0, 'success', {
            workspaces,
            dataLibraryPath: config.data_library_path
          })
        }

        // 4. 插件管理
        if (path.endsWith('/api/plugins') && req.method === 'GET') {
          return sendJson(res, 0, 'success', {
            profile: 'web',
            plugins,
            bundles: plugins.filter((p) => p.layer).map((p) => p.name)
          })
        }

        if (path.endsWith('/api/plugins/status') && req.method === 'GET') {
          return sendJson(res, 0, 'success', { running: false })
        }

        if (path.endsWith('/api/plugins/preview') && req.method === 'POST') {
          const body = await readJsonBody(req)
          const cmd = (body.command || '').trim()
          return sendJson(res, 0, 'success', {
            valid: cmd.length > 0,
            ok: cmd.length > 0,
            command: cmd,
            specs: [cmd]
          })
        }

        if (path.endsWith('/api/plugins/toggle') && req.method === 'POST') {
          const body = await readJsonBody(req)
          const p = plugins.find((item) => item.name === body.name)
          if (p) {
            p.state = body.enabled ? 'live' : 'disabled'
            p.layer = body.enabled
          }
          return sendJson(res, 0, '状态已切换', { name: body.name, enabled: body.enabled })
        }

        if (path.endsWith('/api/plugins/disable-broken') && req.method === 'POST') {
          plugins.forEach(p => {
            if (p.state === 'broken') {
              p.state = 'disabled'
              p.errorReason = undefined
            }
          })
          return sendJson(res, 0, '已禁用异常插件', { disabled: [] })
        }

        if (path.endsWith('/api/plugins/run') && req.method === 'POST') {
          const body = await readJsonBody(req)
          const cmd = body.command || ''
          if (cmd.includes('remove')) {
            const name = cmd.split('remove')[1]?.trim() || ''
            plugins = plugins.filter((p) => p.name !== name)
          }
          return sendJson(res, 0, '指令已执行', { command: cmd })
        }

        if (path.endsWith('/api/plugins/cancel') && req.method === 'POST') {
          return sendJson(res, 0, '操作已取消', {})
        }

        // 5. 日志
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

        // 6. 设置
        if (path.endsWith('/api/config') && req.method === 'GET') {
          return sendJson(res, 0, 'success', config)
        }

        if (path.endsWith('/api/config') && req.method === 'POST') {
          const body = await readJsonBody(req)
          Object.assign(config, body)
          return sendJson(res, 0, '应用设置保存成功', config)
        }

        next()
      })
    }
  }
}
