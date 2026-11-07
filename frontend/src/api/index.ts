import { http } from '../utils/http'
import type {
  StatusData,
  CheckUpdateResult,
  DshVersionsResult,
  WorkspaceData,
  PluginListPayload,
  PluginStatus,
  PreviewResult,
  LogData,
  SettingsConfig,
  SnapshotSummary,
  SnapshotMeta,
  CreateSnapshotParams,
  DshAccountStatus,
  AccountPublicInfo,
  EnvVarItem,
  EnvVarListPayload
} from '../types/api'

/**
 * 系统与进程控制 API
 */
export const systemApi = {
  getStatus: () => http.get<StatusData>('config'),
  checkUpdate: () => http.get<CheckUpdateResult>('check-update'),
  getDshVersions: () => http.get<DshVersionsResult>('deploy/versions'),
  sendAction: (action: 'start' | 'stop' | 'restart' | 'upgrade' | 'deploy' | 'repair' | string, payload?: Record<string, unknown>) =>
    http.post<StatusData>('action', { action, ...payload })
}

/**
 * 工作区数据 API
 */
export const workspaceApi = {
  getList: () => http.get<WorkspaceData>('workspace/list')
}

/**
 * 插件管理 API
 */
export const pluginApi = {
  getList: () => http.get<PluginListPayload>('plugins'),
  getStatus: () => http.get<PluginStatus>('plugins/status'),
  preview: (command: string) => http.post<PreviewResult>('plugins/preview', { command }),
  run: (command: string) => http.post<{ command: string }>('plugins/run', { command }),
  toggle: (name: string, enabled: boolean) =>
    http.post<{ name: string; enabled: boolean }>('plugins/toggle', { name, enabled }),
  cancel: () => http.post('plugins/cancel')
}

/**
 * 日志流与文件 API
 */
export const logApi = {
  getLogs: () => http.get<LogData>('logs'),
  clearLogs: () => http.delete<boolean>('logs'),
  getDownloadUrl: () => 'api/logs/download'
}

/**
 * 应用配置 API
 */
export const configApi = {
  getConfig: () => http.get<SettingsConfig>('config'),
  saveConfig: (config: SettingsConfig) => http.post<SettingsConfig>('config', config),
  getEnvVars: () => http.get<EnvVarListPayload>('env-vars'),
  saveEnvVars: (items: EnvVarItem[]) => http.post<{ count: number }>('env-vars', { items })
}

/**
 * 快照管理 API
 */
export const snapshotApi = {
  getList: () => http.get<SnapshotSummary>('snapshots'),
  create: (params: CreateSnapshotParams) =>
    http.post<SnapshotMeta>('snapshots', params, { timeout: 600000 }),
  cancel: () => http.post('snapshots/cancel'),
  restore: (id: string) =>
    http.post(`snapshots/${encodeURIComponent(id)}/restore`, undefined, { timeout: 600000 }),
  delete: (id: string) => http.delete(`snapshots/${encodeURIComponent(id)}`)
}

/**
 * DeepSeek 官方账号授权 API
 */
export const accountApi = {
  getStatus: () => http.get<DshAccountStatus>('account/status'),
  getPool: () => http.get<AccountPublicInfo[]>('account/pool'),
  switchAccount: (id: string) => http.post<{ message: string; id: string }>('account/switch', { id }),
  deleteFromPool: (id: string) => http.delete<{ message: string }>(`account/pool/${encodeURIComponent(id)}`),
  startLogin: () => http.post<{ authorize_url: string; attempt_id: string; phase: string }>('account/login/start'),
  confirmLogin: (payload: { url?: string; code?: string; state?: string }) =>
    http.post<{ message: string }>('account/login/confirm', payload),
  logout: () => http.post<{ message: string }>('account/logout')
}




