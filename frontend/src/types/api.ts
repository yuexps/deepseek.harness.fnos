/**
 * 统一 API 响应包装契约
 */
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
  timestamp?: number
}

/**
 * 统一网络请求返回结果
 */
export type RequestResult<T> =
  | { success: true; data: T; message: string; timestamp?: number }
  | { success: false; message: string; code?: number }

/**
 * 服务运行状态枚举
 */
export type ServiceStatus = 'stopped' | 'starting' | 'running' | 'building'

/**
 * 系统运行状态模型
 */
export interface StatusData {
  name: string
  version: string
  commit: string
  target_commit?: string
  status: ServiceStatus
  uptime: string
  started_at: number
  server_port?: number
  server_time?: number
  build_time: string
  app_url: string
  pid?: number
  last_message: string
}

/**
 * 检查更新结果模型
 */
export interface CheckUpdateResult {
  has_update: boolean
  current_version?: string
  remote_version?: string
  current_commit: string
  remote_commit: string
  remote_short_commit: string
  message: string
}

/**
 * 工作区项模型
 */
export interface WorkspaceItem {
  workspaceId: string
  path: string
  title: string
  sessionIds: string[]
  createdAt: string
  updatedAt: string
}

/**
 * 工作区数据集合模型
 */
export interface WorkspaceData {
  items: WorkspaceItem[]
  archivedSessionIds: string[]
}

/**
 * 插件运行状态
 * - live: 正常作为层活跃运行中
 * - disabled: 在 cordis.patch.yml 中被显式禁用
 * - inert: 已安装依赖但未声明 dsh.bundle
 */
export type PluginState = 'live' | 'disabled' | 'inert'

/**
 * 已安装插件模型
 */
export interface PluginItem {
  name: string
  spec?: string
  version?: string
  state: PluginState
  layer: boolean
  entryIds?: string[]
  description?: string
  author?: string
  homepage?: string
  license?: string
  keywords?: string[]
  isProtected?: boolean
  hasBundle?: boolean
}

/**
 * 插件列表响应模型
 */
export interface PluginListPayload {
  profile: string
  plugins: PluginItem[]
  bundles: string[]
}

/**
 * 插件操作状态模型
 */
export interface PluginStatus {
  running: boolean
  ok?: boolean
  message?: string
}

/**
 * 插件命令解析预览模型
 */
export interface PreviewResult {
  valid: boolean
  ok?: boolean
  command?: string
  reason?: string
  verb?: string
  profile?: string
  specs?: string[]
}

/**
 * 日志数据模型
 */
export interface LogData {
  lines: string[]
  content: string
}

/**
 * 应用设置配置模型
 */
export interface SettingsConfig {
  server_port: number
  proxy_port: number
  network_proxy: string
  access_mode?: 'fngateway' | 'port' | 'custom'
  reverse_proxy_url: string
  access_password: string
  data_library_path?: string
  version?: string
  commit?: string
  build_time?: string
}

/**
 * WebSocket 信封消息结构
 */
export interface WSEnvelope<T = unknown> {
  type: 'status' | 'workspace' | 'plugin' | 'log' | 'pong' | string
  data: T
  timestamp?: number
}
