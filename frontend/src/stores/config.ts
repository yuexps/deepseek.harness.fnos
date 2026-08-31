import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { configApi } from '../api'
import type { SettingsConfig, RequestResult } from '../types/api'

export const useConfigStore = defineStore('config', () => {
  const config = ref<SettingsConfig>({
    server_port: 2298,
    proxy_port: 2299,
    heap_memory_limit: 0,
    network_proxy: '',
    access_mode: 'fngateway',
    reverse_proxy_url: '',
    access_password: '',
    enable_builtin_skill: true
  })

  const savedConfig = ref<SettingsConfig | null>(null)
  const loading = ref(false)
  const saving = ref(false)
  const loadError = ref(false)
  const lastErrorMessage = ref('')
  const configLoaded = ref(false)

  // 检测是否存在未保存的配置更改
  const isChanged = computed(() => {
    if (!savedConfig.value) return false
    return (
      config.value.server_port !== savedConfig.value.server_port ||
      config.value.proxy_port !== savedConfig.value.proxy_port ||
      (config.value.heap_memory_limit ?? 0) !== (savedConfig.value.heap_memory_limit ?? 0) ||
      (config.value.access_mode || 'fngateway') !== (savedConfig.value.access_mode || 'fngateway') ||
      (config.value.network_proxy || '') !== (savedConfig.value.network_proxy || '') ||
      (config.value.reverse_proxy_url || '') !== (savedConfig.value.reverse_proxy_url || '') ||
      (config.value.access_password || '') !== (savedConfig.value.access_password || '') ||
      Boolean(config.value.enable_builtin_skill ?? true) !== Boolean(savedConfig.value.enable_builtin_skill ?? true)
    )
  })

  // 内部监听端口是否变更
  const isServerPortChanged = computed(() => {
    if (!savedConfig.value) return false
    return config.value.server_port !== savedConfig.value.server_port
  })

  // 反向代理端口是否变更
  const isProxyPortChanged = computed(() => {
    if (!savedConfig.value) return false
    return config.value.proxy_port !== savedConfig.value.proxy_port
  })

  // 堆内存上限是否变更
  const isHeapMemoryChanged = computed(() => {
    if (!savedConfig.value) return false
    return (config.value.heap_memory_limit ?? 0) !== (savedConfig.value.heap_memory_limit ?? 0)
  })

  // 放弃修改，还原为当前已保存的服务器配置
  function resetConfig() {
    if (savedConfig.value) {
      config.value = { ...savedConfig.value }
    }
  }

  // 加载服务端配置
  async function fetchConfig(force = false): Promise<void> {
    if (configLoaded.value && !force) return
    loading.value = true
    loadError.value = false
    lastErrorMessage.value = ''
    try {
      const res = await configApi.getConfig()
      if (res.success && res.data) {
        const data = { ...res.data }
        if (!data.access_mode) {
          data.access_mode = data.reverse_proxy_url ? 'custom' : 'fngateway'
        }
        data.heap_memory_limit = data.heap_memory_limit ?? 0
        if (data.enable_builtin_skill === undefined) {
          data.enable_builtin_skill = true
        }
        config.value = { ...data }
        savedConfig.value = { ...data }
        configLoaded.value = true
      } else {
        loadError.value = true
        lastErrorMessage.value = res.message || '加载配置失败'
      }
    } catch (e: any) {
      loadError.value = true
      lastErrorMessage.value = e?.message || '加载配置失败'
    } finally {
      loading.value = false
    }
  }

  // 手动保存配置
  async function saveConfig(): Promise<RequestResult<SettingsConfig>> {
    if (saving.value) {
      return { success: false, message: '正在保存中，请稍候' }
    }
    saving.value = true
    try {
      const res = await configApi.saveConfig(config.value)
      if (res.success && res.data) {
        savedConfig.value = { ...config.value }
      }
      return res
    } finally {
      saving.value = false
    }
  }

  return {
    config,
    savedConfig,
    loading,
    saving,
    loadError,
    lastErrorMessage,
    configLoaded,
    isChanged,
    isServerPortChanged,
    isProxyPortChanged,
    isHeapMemoryChanged,
    fetchConfig,
    saveConfig,
    resetConfig
  }
})
