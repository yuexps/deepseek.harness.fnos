import { http } from '../utils/http'
import type {
  StatusData,
  CheckUpdateResult,
  WorkspaceData,
  PluginListPayload,
  PluginStatus,
  PreviewResult,
  LogData,
  SettingsConfig
} from '../types/api'

/**
 * 系统与进程控制 API
 */
export const systemApi = {
  getStatus: () => http.get<StatusData>('config'),
  checkUpdate: () => http.get<CheckUpdateResult>('check-update'),
  sendAction: (action: 'start' | 'stop' | 'restart' | 'upgrade' | 'rebuild' | string) =>
    http.post<StatusData>('action', { action })
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
  saveConfig: (config: SettingsConfig) => http.post<SettingsConfig>('config', config)
}
