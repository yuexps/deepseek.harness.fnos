import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { StatusData, CheckUpdateResult, RequestResult } from '../types/api'
import { systemApi, configApi } from '../api'
import { trimSdk } from '../utils/trimSdk'
import { usePluginStore } from './plugin'

export const useSystemStore = defineStore('system', () => {
  const statusData = ref<StatusData>({
    name: 'DeepSeek Harness',
    version: '-',
    commit: '-',
    status: 'stopped',
    uptime: '-',
    started_at: 0,
    build_time: '-',
    app_url: '/app/deepseek-harness/',
    last_message: ''
  })

  const wsConnected = ref(true)
  const serverTimeOffset = ref(0)
  const activeAction = ref<string | null>(null)
  const isCheckingUpdate = ref(false)
  const currentTime = ref(Date.now())
  const statusLoaded = ref(false)

  let clockTimer: ReturnType<typeof setInterval> | null = null
  let actionTimeoutTimer: ReturnType<typeof setTimeout> | null = null
  let statusTimeoutTimer: ReturnType<typeof setTimeout> | null = null

  // 启动 3 秒超时保底：超过 3 秒若未收到状态也强制放行渲染
  function startStatusTimeout() {
    if (statusLoaded.value) return
    if (!statusTimeoutTimer) {
      statusTimeoutTimer = setTimeout(() => {
        statusLoaded.value = true
      }, 3000)
    }
  }

  function clearActionLock() {
    activeAction.value = null
    if (actionTimeoutTimer) {
      clearTimeout(actionTimeoutTimer)
      actionTimeoutTimer = null
    }
  }

  function startClock() {
    if (clockTimer) return
    clockTimer = setInterval(() => {
      currentTime.value = Date.now()
    }, 1000)
  }

  function stopClock() {
    if (clockTimer) {
      clearInterval(clockTimer)
      clockTimer = null
    }
  }

  function setWsConnected(val: boolean) {
    wsConnected.value = val
  }

  function updateStatus(data: Partial<StatusData>) {
    statusLoaded.value = true
    if (statusTimeoutTimer) {
      clearTimeout(statusTimeoutTimer)
      statusTimeoutTimer = null
    }

    const oldStatus = statusData.value.status
    statusData.value = {
      ...statusData.value,
      ...data
    }
    if (data.server_time) {
      serverTimeOffset.value = data.server_time * 1000 - Date.now()
    }
    const curStatus = statusData.value.status

    // 服务停止或重启恢复运行后清理插件待生效状态
    if (curStatus === 'stopped' || (oldStatus === 'starting' && curStatus === 'running')) {
      const pluginStore = usePluginStore()
      pluginStore.clearRestartNeeded()
    }

    // 状态流转完成时自动解除动作锁定
    if (activeAction.value) {
      const isTransitionToRunning = oldStatus !== 'running' && curStatus === 'running'
      const isFailedOrStopped = (oldStatus === 'starting' || oldStatus === 'building') && curStatus === 'stopped'

      if (activeAction.value === 'stop') {
        if (curStatus === 'stopped') {
          clearActionLock()
        }
      } else if (activeAction.value === 'start' || activeAction.value === 'restart') {
        if (isTransitionToRunning || isFailedOrStopped) {
          clearActionLock()
        }
      } else if (
        activeAction.value === 'upgrade' ||
        activeAction.value === 'rebuild' ||
        activeAction.value === 'repair' ||
        activeAction.value === 'reset'
      ) {
        if (isTransitionToRunning || isFailedOrStopped) {
          clearActionLock()
        }
      }
    }
  }

  const isRunning = computed(() => statusData.value.status === 'running')
  const isStarting = computed(() => statusData.value.status === 'starting')
  const isBuilding = computed(() => statusData.value.status === 'building')
  const isActionLocked = computed(() => Boolean(activeAction.value) || isBuilding.value || isStarting.value)

  const statusTagType = computed<'success' | 'warning' | 'info' | 'default'>(() => {
    if (isRunning.value) return 'success'
    if (isStarting.value) return 'warning'
    if (isBuilding.value) return 'info'
    return 'default'
  })

  const statusLabel = computed(() => {
    if (isRunning.value) return '运行中'
    if (isStarting.value) return '启动中'
    if (isBuilding.value) return '构建中'
    return '已停止'
  })

  function formatDuration(total: number): string {
    const h = Math.floor(total / 3600)
    const m = Math.floor((total % 3600) / 60)
    const s = total % 60
    if (h > 0) return `${h}小时${m}分${s}秒`
    if (m > 0) return `${m}分${s}秒`
    return `${s}秒`
  }

  const uptimeText = computed(() => {
    const s = statusData.value
    if (s.status !== 'running' || !s.started_at) return '-'
    const serverNow = currentTime.value + serverTimeOffset.value
    const secs = Math.max(0, Math.floor((serverNow - s.started_at * 1000) / 1000))
    return formatDuration(secs)
  })

  async function sendAction(action: string, payload?: Record<string, unknown>): Promise<RequestResult<StatusData>> {
    if (isActionLocked.value) {
      return { success: false, message: '当前有任务正在进行中，请稍候' }
    }

    activeAction.value = action

    // 超时兜底（最多锁定 60s）
    actionTimeoutTimer = setTimeout(() => {
      clearActionLock()
    }, 60000)

    try {
      const res = await systemApi.sendAction(action, payload)
      if (!res.success) {
        clearActionLock()
      }
      return res
    } catch {
      clearActionLock()
    }
    return { success: false, message: '请求失败' }
  }

  async function checkUpdate(): Promise<RequestResult<CheckUpdateResult>> {
    if (isCheckingUpdate.value) {
      return { success: false, message: '正在检查更新中，请稍候' }
    }
    isCheckingUpdate.value = true
    try {
      return await systemApi.checkUpdate()
    } finally {
      isCheckingUpdate.value = false
    }
  }

  async function openHarnessApp(): Promise<void> {
    const res = await configApi.getConfig()
    const cfg = res.success ? res.data : null
    const mode = cfg?.access_mode || (cfg?.reverse_proxy_url ? 'custom' : 'fngateway')

    if (mode === 'custom' && cfg?.reverse_proxy_url) {
      await trimSdk.openURL(cfg.reverse_proxy_url, '_blank')
      return
    }

    if (mode === 'port') {
      const port = cfg?.proxy_port || 2299
      await trimSdk.openURL(`https://${window.location.hostname}:${port}/`, '_blank')
      return
    }

    // 默认飞牛网关直连模式
    const gatewayUrl = `${window.location.origin}/app/deepseek-harness/fngateway/`
    await trimSdk.openURL(gatewayUrl, '_blank')
  }

  // 首屏快速拉取状态
  async function fetchInitialStatus(): Promise<void> {
    if (statusLoaded.value) return
    startStatusTimeout()
    try {
      const res = await systemApi.getStatus()
      if (res.success && res.data) {
        updateStatus(res.data)
      }
    } catch {
      // 捕获异常，依赖 WebSocket 或 3 秒超时保底
    }
  }

  return {
    statusData,
    statusLoaded,
    wsConnected,
    serverTimeOffset,
    activeAction,
    isCheckingUpdate,
    isActionLocked,
    isRunning,
    isStarting,
    isBuilding,
    statusTagType,
    statusLabel,
    uptimeText,
    startClock,
    stopClock,
    setWsConnected,
    updateStatus,
    fetchInitialStatus,
    sendAction,
    checkUpdate,
    openHarnessApp
  }
})
