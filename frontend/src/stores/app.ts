import { defineStore } from 'pinia'
import { ref } from 'vue'
import { wsClient } from '../utils/websocket'
import { useSystemStore } from './system'
import { useWorkspaceStore } from './workspace'
import { usePluginStore } from './plugin'
import { useLogStore } from './log'

export const useAppStore = defineStore('app', () => {
  const currentTab = ref('overview')
  let isInitialized = false

  function setTab(tab: string) {
    currentTab.value = tab
  }

  function init() {
    if (isInitialized) return
    isInitialized = true

    const systemStore = useSystemStore()
    const workspaceStore = useWorkspaceStore()
    const pluginStore = usePluginStore()
    const logStore = useLogStore()

    systemStore.startClock()

    // 绑定 WebSocket 事件分发
    wsClient.on('connectionChange', (connected) => {
      systemStore.setWsConnected(connected)
    })

    wsClient.on('status', (data) => {
      systemStore.updateStatus(data)
    })

    wsClient.on('workspace', (data) => {
      workspaceStore.updateWorkspaceData(data)
    })

    wsClient.on('plugin', (status) => {
      pluginStore.updatePluginStatus(status)
    })

    wsClient.on('usage', (data) => {
      systemStore.updateUsage(data.cpu, data.memory)
    })

    wsClient.on('log', (chunk) => {
      logStore.appendChunk(chunk)
    })

    wsClient.on('reconnected', () => {
      // 重新连接后主动拉取一次最新工作区与插件与日志
      workspaceStore.fetchWorkspaces()
      pluginStore.fetchPlugins()
      logStore.fetchLogs()
    })

    // 建立连接
    wsClient.connect()
  }

  return {
    currentTab,
    setTab,
    init
  }
})
