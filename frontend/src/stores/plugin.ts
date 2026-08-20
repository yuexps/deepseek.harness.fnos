import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { pluginApi } from '../api'
import type { PluginItem, PluginStatus, PreviewResult, RequestResult } from '../types/api'

export const usePluginStore = defineStore('plugin', () => {
  const plugins = ref<PluginItem[]>([])
  const loading = ref(false)
  const pluginBusy = ref(false)

  const command = ref('')
  const preview = ref<PreviewResult | null>(null)

  const searchKeyword = ref('')
  const filterStatus = ref<'all' | 'live' | 'disabled' | 'inert'>('all')

  const needRestart = ref(false)

  function markRestartNeeded() {
    needRestart.value = true
  }

  function clearRestartNeeded() {
    needRestart.value = false
  }

  let previewTimer: ReturnType<typeof setTimeout> | null = null

  function setCommand(cmd: string) {
    command.value = cmd
    if (previewTimer) clearTimeout(previewTimer)
    const trimmed = cmd.trim()
    if (!trimmed) {
      preview.value = null
      return
    }
    previewTimer = setTimeout(async () => {
      const res = await pluginApi.preview(trimmed)
      if (res.success && res.data) {
        preview.value = {
          valid: res.data.valid ?? res.data.ok ?? false,
          command: res.data.command,
          reason: res.data.reason,
          verb: res.data.verb,
          profile: res.data.profile,
          specs: res.data.specs
        }
      } else {
        preview.value = {
          valid: false,
          reason: res.message || '命令解析失败'
        }
      }
    }, 200)
  }

  function fillDshMarketCommand() {
    setCommand('dsh plugin --profile web add dshmarket')
  }

  const canInstall = computed(() => {
    return Boolean(!pluginBusy.value && command.value.trim() && preview.value?.valid)
  })

  // 统计指标
  const liveCount = computed(() => plugins.value.filter(p => p.state === 'live').length)
  const disabledCount = computed(() => plugins.value.filter(p => p.state === 'disabled').length)
  const inertCount = computed(() => plugins.value.filter(p => p.state === 'inert').length)

  // 过滤后的插件列表
  const filteredPlugins = computed(() => {
    let list = plugins.value

    // 状态过滤
    if (filterStatus.value !== 'all') {
      list = list.filter(p => p.state === filterStatus.value)
    }

    // 关键词搜索
    const q = searchKeyword.value.trim().toLowerCase()
    if (q) {
      list = list.filter(p => {
        const nameMatch = p.name.toLowerCase().includes(q)
        const descMatch = (p.description || '').toLowerCase().includes(q)
        const authorMatch = (p.author || '').toLowerCase().includes(q)
        const specMatch = (p.spec || '').toLowerCase().includes(q)
        const keywordMatch = (p.keywords || []).some(k => k.toLowerCase().includes(q))
        return nameMatch || descMatch || authorMatch || specMatch || keywordMatch
      })
    }

    return list
  })

  async function fetchPlugins(): Promise<void> {
    loading.value = true
    try {
      const res = await pluginApi.getList()
      if (res.success && res.data) {
        plugins.value = Array.isArray(res.data.plugins) ? res.data.plugins : []
      }
    } finally {
      loading.value = false
    }
  }

  let busyWatchTimer: ReturnType<typeof setTimeout> | null = null

  function clearBusyWatchTimer() {
    if (busyWatchTimer) {
      clearTimeout(busyWatchTimer)
      busyWatchTimer = null
    }
  }

  function startBusyWatchTimer() {
    clearBusyWatchTimer()
    busyWatchTimer = setTimeout(async () => {
      try {
        const res = await pluginApi.getStatus()
        if (res.success && res.data) {
          updatePluginStatus(res.data)
        }
      } catch {
        pluginBusy.value = false
        await fetchPlugins()
      }
    }, 190000)
  }

  function updatePluginStatus(s: PluginStatus) {
    const wasBusy = pluginBusy.value
    pluginBusy.value = s.running
    if (s.running) {
      startBusyWatchTimer()
    } else {
      clearBusyWatchTimer()
    }
    if (wasBusy && !s.running) {
      if (s.ok) {
        needRestart.value = true
      }
      fetchPlugins()
    }
  }

  async function installPlugin(): Promise<RequestResult<unknown>> {
    if (!canInstall.value) {
      return { success: false, message: '请先输入有效的安装指令' }
    }
    const res = await pluginApi.run(command.value.trim())
    if (res.success) {
      command.value = ''
      preview.value = null
    }
    return res
  }

  async function togglePlugin(name: string, enable: boolean): Promise<RequestResult<unknown>> {
    const res = await pluginApi.toggle(name, enable)
    if (res.success) {
      await fetchPlugins()
    }
    return res
  }

  async function updatePlugin(name: string): Promise<RequestResult<unknown>> {
    return pluginApi.run(`dsh plugin --profile web update ${name}`)
  }

  async function uninstallPlugin(name: string): Promise<RequestResult<unknown>> {
    return pluginApi.run(`dsh plugin --profile web remove ${name}`)
  }

  async function cancelPluginOp(): Promise<RequestResult<unknown>> {
    return pluginApi.cancel()
  }

  return {
    plugins,
    loading,
    pluginBusy,
    needRestart,
    command,
    preview,
    searchKeyword,
    filterStatus,
    canInstall,
    liveCount,
    disabledCount,
    inertCount,
    filteredPlugins,
    markRestartNeeded,
    clearRestartNeeded,
    setCommand,
    fillDshMarketCommand,
    fetchPlugins,
    updatePluginStatus,
    installPlugin,
    togglePlugin,
    cancelPluginOp,
    updatePlugin,
    uninstallPlugin
  }
})
