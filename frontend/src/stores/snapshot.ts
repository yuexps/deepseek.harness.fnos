import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { SnapshotMeta, SnapshotSummary, SnapshotProgressTask, CreateSnapshotParams } from '../types/api'
import { snapshotApi } from '../api'

export const useSnapshotStore = defineStore('snapshot', () => {
  const snapshots = ref<SnapshotMeta[]>([])
  const totalSizeBytes = ref(0)
  const loading = ref(false)
  const actionLoading = ref(false)

  // 全局持久化快照进度状态
  const progressVisible = ref(false)
  const progressPercent = ref(0)
  const progressStage = ref('')
  const progressMessage = ref('')
  const progressAction = ref<'create' | 'restore' | ''>('')

  let hideTimer: ReturnType<typeof setTimeout> | null = null

  const progressTitle = computed(() => {
    if (progressMessage.value && progressStage.value) {
      return `${progressStage.value} · ${progressMessage.value}`
    }
    if (progressStage.value) {
      return progressStage.value
    }
    return progressAction.value === 'restore' ? '正在还原快照' : '正在处理快照'
  })

  function updateProgress(data: SnapshotProgressTask) {
    if (!data) return

    if (data.active === false) {
      if (progressVisible.value) {
        if (!hideTimer) {
          hideTimer = setTimeout(() => {
            progressVisible.value = false
            actionLoading.value = false
            hideTimer = null
          }, 1000)
        }
      } else {
        progressVisible.value = false
        actionLoading.value = false
      }
      return
    }

    // 活跃任务进入
    if (hideTimer) {
      clearTimeout(hideTimer)
      hideTimer = null
    }

    progressVisible.value = true
    actionLoading.value = true
    progressPercent.value = Math.max(0, Math.min(100, data.percent))
    if (data.action) {
      progressAction.value = data.action as 'create' | 'restore'
    }
    if (data.stage !== undefined) {
      progressStage.value = data.stage
    }
    if (data.message !== undefined) {
      progressMessage.value = data.message
    }

    if (progressPercent.value >= 100) {
      if (!hideTimer) {
        hideTimer = setTimeout(() => {
          progressVisible.value = false
          actionLoading.value = false
          hideTimer = null
        }, 1500)
      }
    }
  }

  function updateSummary(summary: SnapshotSummary) {
    if (!summary) return
    snapshots.value = summary.items || []
    totalSizeBytes.value = summary.total_size_bytes || 0
    loading.value = false

    // 同步后端随附的任务进度
    if (summary.current_task) {
      updateProgress(summary.current_task)
    }
  }

  async function fetchSnapshots() {
    loading.value = true
    try {
      const res = await snapshotApi.getList()
      if (res.success && res.data) {
        updateSummary(res.data)
      }
      return res
    } finally {
      loading.value = false
    }
  }

  async function createSnapshot(params: CreateSnapshotParams) {
    actionLoading.value = true
    progressVisible.value = true
    progressPercent.value = 0
    progressAction.value = 'create'
    progressStage.value = `正在准备打包快照「${params.name}」`
    progressMessage.value = '校验环境与数据源'

    try {
      const res = await snapshotApi.create(params)
      if (res.success) {
        progressPercent.value = 100
        progressStage.value = `快照「${params.name}」创建完成`
        progressMessage.value = ''
        await fetchSnapshots()
        if (!hideTimer) {
          hideTimer = setTimeout(() => {
            progressVisible.value = false
            actionLoading.value = false
            hideTimer = null
          }, 1500)
        }
      } else {
        progressVisible.value = false
        actionLoading.value = false
      }
      return res
    } catch (err) {
      progressVisible.value = false
      actionLoading.value = false
      throw err
    }
  }

  async function restoreSnapshot(id: string, name?: string) {
    actionLoading.value = true
    progressVisible.value = true
    progressPercent.value = 5
    progressAction.value = 'restore'
    progressStage.value = `正在还原快照「${name || id}」`
    progressMessage.value = '停止服务并校验数据包'

    try {
      const res = await snapshotApi.restore(id)
      if (res.success) {
        progressPercent.value = 100
        progressStage.value = '快照还原完成'
        progressMessage.value = '服务已就绪'
        await fetchSnapshots()
        if (!hideTimer) {
          hideTimer = setTimeout(() => {
            progressVisible.value = false
            actionLoading.value = false
            hideTimer = null
          }, 1500)
        }
      } else {
        progressVisible.value = false
        actionLoading.value = false
      }
      return res
    } catch (err) {
      progressVisible.value = false
      actionLoading.value = false
      throw err
    }
  }

  async function deleteSnapshot(id: string) {
    actionLoading.value = true
    try {
      const res = await snapshotApi.delete(id)
      if (res.success) {
        await fetchSnapshots()
      }
      return res
    } finally {
      actionLoading.value = false
    }
  }

  return {
    snapshots,
    totalSizeBytes,
    loading,
    actionLoading,
    progressVisible,
    progressPercent,
    progressStage,
    progressMessage,
    progressAction,
    progressTitle,
    updateProgress,
    updateSummary,
    fetchSnapshots,
    createSnapshot,
    restoreSnapshot,
    deleteSnapshot
  }
})
