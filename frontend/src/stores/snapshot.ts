import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { SnapshotMeta, SnapshotSummary, SnapshotProgressTask, CreateSnapshotParams } from '../types/api'
import { snapshotApi } from '../api'

export const useSnapshotStore = defineStore('snapshot', () => {
  const snapshots = ref<SnapshotMeta[]>([])
  const totalSizeBytes = ref(0)
  const loading = ref(false)
  const actionLoading = ref(false)
  const cancelLoading = ref(false)

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

    // 任务失败或取消处理
    if (data.error) {
      progressAction.value = (data.action as 'create' | 'restore') || progressAction.value
      // 优先采用服务端返回的 stage（如“快照创建已取消”），避免将手动取消误标为“快照创建失败”
      progressStage.value = data.stage || (data.action === 'restore' ? '快照还原失败' : '快照创建失败')
      progressMessage.value = data.error
      progressPercent.value = 0
      actionLoading.value = false
      cancelLoading.value = false
      if (!hideTimer) {
        hideTimer = setTimeout(() => {
          progressVisible.value = false
          hideTimer = null
        }, 3000)
      }
      return
    }

    if (data.active === false) {
      cancelLoading.value = false
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

    // 仅在本地无活跃进度、且服务端存在未完成任务时恢复显示
    if (summary.current_task?.active && !progressVisible.value && summary.current_task.percent < 100) {
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
    progressStage.value = `正在启动快照创建任务「${params.name}」`
    progressMessage.value = '准备执行环境'

    try {
      const res = await snapshotApi.create(params)
      if (!res.success) {
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
    progressStage.value = `正在启动快照还原任务「${name || id}」`
    progressMessage.value = '校验并准备还原'

    try {
      const res = await snapshotApi.restore(id)
      if (!res.success) {
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
      return await snapshotApi.delete(id)
    } finally {
      actionLoading.value = false
    }
  }

  async function cancelCreateSnapshot() {
    cancelLoading.value = true
    try {
      const res = await snapshotApi.cancel()
      return res
    } finally {
      setTimeout(() => {
        cancelLoading.value = false
      }, 500)
    }
  }

  return {
    snapshots,
    totalSizeBytes,
    loading,
    actionLoading,
    cancelLoading,
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
    cancelCreateSnapshot,
    restoreSnapshot,
    deleteSnapshot
  }
})
