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
export type ServiceStatus = 'stopped' | 'starting' | 'running' | 'building' | 'snapshotting'

/**
 * 系统运行状态模型
 */
export interface StatusData {
  name: string
  version?: string
  app_version?: string
  app_remote_version?: string
  app_has_update?: boolean
  target_version?: string
  status: ServiceStatus
  uptime: string
  started_at: number
  server_port?: number
  server_time?: number
  build_time: string
  app_url: string
  pid?: number
  cpu?: string
  memory?: string
  last_message: string
}

/**
 * 检查更新结果模型
 */
export interface CheckUpdateResult {
  has_update: boolean
  current_version?: string
  remote_version?: string
  message: string
}

/**
 * 在线部署可选版本
 */
export interface DshVersionsResult {
  current_version: string
  versions: string[]
  recommended_version: string
  min_version: string
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
 * 插件状态:
 * - live: 已启用
 * - disabled: 已停用
 * - inert: 普通依赖 (未声明 bundle)
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
 * 自定义环境变量项
 */
export interface EnvVarItem {
  key: string
  value: string
}

/**
 * 环境变量列表响应
 */
export interface EnvVarListPayload {
  items: EnvVarItem[]
}

/**
 * 应用设置配置模型
 */
export interface SettingsConfig {
  server_port: number
  proxy_port: number
  heap_memory_limit?: number
  network_proxy: string
  proxy_dsh_runtime?: boolean
  npm_registry?: string
  update_channel?: 'latest' | 'alpha' | 'next' | 'newest'
  access_mode?: 'fngateway' | 'port' | 'custom'
  reverse_proxy_url: string
  access_password: string
  data_library_path?: string
  version?: string
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

/**
 * 快照元数据模型
 */
export interface SnapshotMeta {
  id: string
  name: string
  created_at: number
  size_bytes: number
  compression_level?: number
  app_version?: string
  harness_version?: string
  plugin_count?: number
}

/**
 * 快照进行中任务进度状态
 */
export interface SnapshotProgressTask {
  active?: boolean
  action: string
  percent: number
  stage?: string
  message?: string
  error?: string
}

/**
 * 快照列表与存储汇总
 */
export interface SnapshotSummary {
  items: SnapshotMeta[]
  total_size_bytes: number
  disk_free_bytes: number
  disk_total_bytes: number
  current_task?: SnapshotProgressTask
}

/**
 * 创建快照参数
 */
export interface CreateSnapshotParams {
  name: string
  compression_level?: number
}

/**
 * DeepSeek 官方账号状态
 */
export interface DshAccountStatus {
  status: 'signed-out' | 'credential-stored'
  name?: string
  contact?: string
  avatar_url?: string
  currency?: string
  balance?: string
  bonus_balance?: string
  usage_url?: string
  top_up_url?: string
  waiting_sign_in: boolean
  authorize_url?: string
}

/**
 * 已保存的 DeepSeek 账号公开信息
 */
export interface AccountPublicInfo {
  id: string
  name?: string
  contact?: string
  avatar_url?: string
  currency?: string
  balance?: string
  bonus_balance?: string
  is_active: boolean
  last_active_at: number
}
