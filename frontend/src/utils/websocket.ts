import type { WSEnvelope, StatusData, WorkspaceData, PluginStatus, SnapshotSummary, SnapshotProgressTask } from '../types/api'

export type WSEventMap = {
  status: StatusData
  workspace: WorkspaceData
  plugin: PluginStatus
  snapshot: SnapshotSummary
  usage: { cpu: string; memory: string }
  log: string
  pong: { server_time: number }
  connectionChange: boolean
  reconnected: void
  snapshot_progress: SnapshotProgressTask
}

type EventCallback<K extends keyof WSEventMap> = (data: WSEventMap[K]) => void

export class WSManager {
  private ws: WebSocket | null = null
  private url: string = ''
  private isConnected = false
  private openedOnce = false
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private lastActivity = 0

  private retryCount = 0
  private readonly minDelay = 1000
  private readonly maxDelay = 10000
  private readonly heartbeatInterval = 10000
  private readonly activityTimeout = 25000

  private listeners: { [K in keyof WSEventMap]?: Set<EventCallback<K>> } = {}

  constructor() {
    this.setupVisibilityListener()
  }

  private resolveWsUrl(): string {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${window.location.host}/app/deepseek-harness/api/ws`
  }

  public connect(): void {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return
    }

    this.url = this.resolveWsUrl()
    try {
      this.ws = new WebSocket(this.url)
    } catch {
      this.handleClose()
      return
    }

    this.ws.onopen = () => {
      this.isConnected = true
      this.retryCount = 0
      this.lastActivity = Date.now()
      this.emit('connectionChange', true)

      if (this.openedOnce) {
        this.emit('reconnected', undefined as unknown as void)
      }
      this.openedOnce = true
      this.startHeartbeat()
    }

    this.ws.onmessage = (e: MessageEvent) => {
      this.lastActivity = Date.now()
      if (typeof e.data !== 'string') return

      try {
        const envelope = JSON.parse(e.data) as WSEnvelope
        if (envelope && envelope.type) {
          this.emit(envelope.type as keyof WSEventMap, envelope.data as any)
        }
      } catch {
        // 忽略非 JSON 异常消息
      }
    }

    this.ws.onclose = () => {
      this.handleClose()
    }

    this.ws.onerror = () => {
      this.ws?.close()
    }
  }

  private handleClose(): void {
    this.stopHeartbeat()
    this.ws = null
    if (this.isConnected) {
      this.isConnected = false
      this.emit('connectionChange', false)
    }
    this.scheduleReconnect()
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer !== null) return

    const delay = Math.min(this.minDelay * Math.pow(1.5, this.retryCount), this.maxDelay)
    this.retryCount++

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this.connect()
    }, delay)
  }

  private startHeartbeat(): void {
    this.stopHeartbeat()
    this.heartbeatTimer = setInterval(() => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return

      // 检测是否长时间未收到数据包（假死检测）
      if (Date.now() - this.lastActivity > this.activityTimeout) {
        this.ws.close()
        return
      }

      // 发送应用层心跳帧
      this.send({ type: 'ping' })
    }, this.heartbeatInterval)
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer !== null) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
  }

  private setupVisibilityListener(): void {
    if (typeof document === 'undefined') return
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') {
        if (!this.isConnected || !this.ws || this.ws.readyState !== WebSocket.OPEN) {
          this.connect()
        }
      }
    })
  }

  public send(data: unknown): boolean {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      try {
        this.ws.send(typeof data === 'string' ? data : JSON.stringify(data))
        return true
      } catch {
        return false
      }
    }
    return false
  }

  public on<K extends keyof WSEventMap>(event: K, callback: EventCallback<K>): () => void {
    if (!this.listeners[event]) {
      this.listeners[event] = new Set() as any
    }
    const set = this.listeners[event] as Set<EventCallback<K>>
    set.add(callback)

    return () => {
      set.delete(callback)
    }
  }

  private emit<K extends keyof WSEventMap>(event: K, data: WSEventMap[K]): void {
    const set = this.listeners[event] as Set<EventCallback<K>> | undefined
    if (set) {
      set.forEach((cb) => {
        try {
          cb(data)
        } catch {
          // 防止监听器执行异常阻断分发
        }
      })
    }
  }

  public get connected(): boolean {
    return this.isConnected
  }
}

export const wsClient = new WSManager()
