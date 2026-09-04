<template>
  <div class="w-full flex-1 flex flex-col gap-3.5 sm:gap-6 pb-20 sm:pb-6">
    <!-- 页头：桌面与移动端响应式布局 -->
    <div
      class="sticky -top-[14px] sm:-top-6 z-20 -mt-3.5 sm:-mt-4 pt-5 sm:pt-7 pb-2.5 bg-[#f5f7fa]/90 dark:bg-[#12141a]/90 backdrop-blur-md flex items-center justify-between gap-2.5 transition-all duration-200">
      <div class="flex items-baseline gap-2 min-w-0">
        <h1 class="text-lg sm:text-xl font-bold text-slate-800 dark:text-slate-100 tracking-tight shrink-0">快照管理</h1>
        <span v-if="snapshots.length" class="text-xs text-slate-400 dark:text-slate-500 font-medium truncate"
          :title="`共 ${snapshots.length} 个 · ${formatSize(totalSizeBytes)}`">
          共 {{ snapshots.length }} 个 · {{ formatSize(totalSizeBytes) }}
        </span>
      </div>

      <div class="flex items-center gap-1.5 sm:gap-2 shrink-0">
        <n-button type="primary" size="small" :disabled="actionLoading" @click="openCreateModal"
          class="!px-3 sm:!h-9 rounded-lg">
          创建快照
        </n-button>
      </div>
    </div>

    <!-- 快照任务进行中进度条 -->
    <n-collapse-transition :show="progressVisible">
      <div
        class="p-3 sm:p-4 rounded-xl sm:rounded-2xl bg-white dark:bg-[#181a20] border border-blue-100 dark:border-blue-900/40 shadow-sm flex flex-col gap-2">
        <div class="flex items-center justify-between text-xs sm:text-sm">
          <div class="flex items-center gap-2 font-medium text-slate-700 dark:text-slate-200 min-w-0">
            <n-spin :size="14" class="shrink-0" />
            <span class="truncate">{{ progressTitle }}</span>
          </div>
          <span class="font-mono font-bold text-fnos-blue dark:text-blue-400 shrink-0 ml-2">
            {{ Math.round(progressPercent) }}%
          </span>
        </div>
        <n-progress type="line" :percentage="progressPercent" :show-indicator="false" :processing="true" status="info"
          :height="6" border-radius="3px" />
      </div>
    </n-collapse-transition>

    <!-- 快照列表 / 空状态 -->
    <div class="w-full flex-1">
      <n-card v-if="!snapshots.length && !loading" :bordered="false" class="py-12 text-center shadow-sm rounded-2xl">
        <n-empty description="暂无快照备份">
          <template #icon>
            <n-icon :size="48" class="text-slate-300 dark:text-slate-600">
              <History />
            </n-icon>
          </template>
        </n-empty>
      </n-card>

      <!-- 单列快照卡片流（一行一个，融合工作区卡片设计风格） -->
      <div v-else class="flex flex-col gap-2.5 sm:gap-3">
        <n-card v-for="item in snapshots" :key="item.id" hoverable :bordered="false"
          class="interactive-card select-none shadow-sm group rounded-2xl transition-all duration-200 hover:shadow-md"
          content-style="padding: 12px 14px sm:padding: 14px 18px;">
          <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 sm:gap-4">
            <!-- 左侧核心信息 -->
            <div class="flex items-start sm:items-center gap-2.5 sm:gap-3.5 min-w-0 flex-1">
              <!-- 时光机图标：工作区同款层次感底色与 hover 缩放高亮 -->
              <div
                class="w-10 h-10 rounded-xl bg-slate-100 dark:bg-white/[0.06] group-hover:bg-blue-50 dark:group-hover:bg-blue-950/40 text-slate-500 dark:text-slate-400 group-hover:text-fnos-blue dark:group-hover:text-blue-400 flex items-center justify-center transition-all duration-200 group-hover:scale-105 shrink-0 mt-0.5 sm:mt-0">
                <n-icon :size="20">
                  <History />
                </n-icon>
              </div>

              <!-- 标题与标签行容器 -->
              <div class="min-w-0 flex-1 space-y-1 sm:space-y-1.5">
                <!-- 第一行：标题 + 仅数据徽章 + 版本标签（单行自适应截断） -->
                <div class="flex items-center gap-1.5 sm:gap-2 min-w-0">
                  <span
                    class="text-sm sm:text-base font-semibold text-slate-800 dark:text-slate-100 truncate min-w-0 transition-colors group-hover:text-fnos-blue dark:group-hover:text-blue-400"
                    :title="item.name">
                    {{ item.name }}
                  </span>

                  <!-- 语义化版本标签 (Semver + 短Commit) -->
                  <n-tag size="tiny" :bordered="false"
                    class="shrink-0 font-mono text-[10px] sm:text-xs bg-slate-100 dark:bg-white/[0.08] text-slate-600 dark:text-slate-300">
                    {{ formatSnapshotVersion(item) }}
                  </n-tag>
                </div>

                <!-- 附属元信息：时间微图标、物理大小、插件数 -->
                <div
                  class="text-[11px] sm:text-xs text-slate-400 dark:text-slate-500 flex items-center flex-wrap gap-x-2.5 gap-y-0.5">
                  <div class="flex items-center gap-1 shrink-0">
                    <n-icon :size="12">
                      <Clock />
                    </n-icon>
                    <span>{{ formatTime(item.created_at) }}</span>
                  </div>
                  <span>·</span>
                  <span>{{ formatSize(item.size_bytes) }}</span>
                  <span v-if="item.plugin_count !== undefined">·</span>
                  <span v-if="item.plugin_count !== undefined">{{ item.plugin_count }} 个插件</span>
                </div>
              </div>
            </div>

            <!-- 右侧 / 移动端底部操作栏 -->
            <div
              class="flex items-center justify-end gap-2 shrink-0 pt-2 border-t border-slate-100/80 dark:border-white/[0.04] sm:border-0 sm:pt-0">
              <n-button secondary type="primary" size="small" :disabled="actionLoading" @click="promptRestore(item)"
                class="!h-7 sm:!h-8 !px-2.5 sm:!px-3 rounded-lg text-xs font-medium transition-transform duration-150 active:scale-95">
                <template #icon>
                  <n-icon>
                    <Rotate />
                  </n-icon>
                </template>
                还原
              </n-button>

              <n-button secondary type="error" size="small" :disabled="actionLoading" @click="promptDelete(item)"
                class="!h-7 sm:!h-8 !px-2.5 sm:!px-3 rounded-lg text-xs font-medium transition-transform duration-150 active:scale-95">
                <template #icon>
                  <n-icon>
                    <Trash />
                  </n-icon>
                </template>
                删除
              </n-button>
            </div>
          </div>
        </n-card>
      </div>
    </div>
    <!-- 创建快照弹窗 -->
    <n-modal v-model:show="showCreateModal" preset="dialog" title="创建 DSH 快照" positive-text="创建快照" negative-text="取消"
      :loading="actionLoading" @positive-click="handleCreateSnapshot">
      <div class="space-y-3.5 pt-1 text-xs">
        <!-- 快照说明 -->
        <div
          class="p-2 rounded-xl bg-slate-50 dark:bg-white/[0.03] border border-slate-200/60 dark:border-white/[0.06] text-[11.5px] text-slate-500 dark:text-slate-400">
          完整备份 DSH 源码、数据、插件与依赖。运行中将短暂重启服务。
        </div>

        <div class="space-y-1.5">
          <div class="flex items-center justify-between font-medium text-slate-700 dark:text-slate-200">
            <span>快照名称</span>
            <span v-if="isDuplicateName" class="text-[11px] text-amber-500 font-normal">已存在同名快照</span>
          </div>
          <n-input v-model:value="snapName" placeholder="请输入快照名称" autofocus maxlength="40"
            :status="isDuplicateName ? 'warning' : undefined" />
        </div>
        <div class="space-y-1.5">
          <div class="font-medium text-slate-700 dark:text-slate-200">压缩级别</div>
          <n-select v-model:value="compressionLevel" :options="compressionOptions" />
        </div>
      </div>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import {
  NButton,
  NCard,
  NEmpty,
  NIcon,
  NTag,
  NInput,
  NSelect,
  NModal,
  NProgress,
  NCollapseTransition,
  NSpin,
  useMessage,
  useDialog
} from 'naive-ui'
import {
  History,
  Clock,
  Trash,
  Rotate
} from '@vicons/tabler'
import { useSnapshotStore } from '../stores/snapshot'
import type { SnapshotMeta } from '../types/api'

const message = useMessage()
const dialog = useDialog()
const snapshotStore = useSnapshotStore()

const {
  snapshots,
  totalSizeBytes,
  loading,
  actionLoading,
  progressVisible,
  progressPercent,
  progressTitle
} = storeToRefs(snapshotStore)

// 创建弹窗状态
const showCreateModal = ref(false)
const snapName = ref('')
const compressionLevel = ref(1)

const isDuplicateName = computed(() => {
  const name = snapName.value.trim().toLowerCase()
  if (!name) return false
  return snapshots.value.some(s => (s.name || '').trim().toLowerCase() === name)
})

const compressionOptions = [
  { label: '快速 (Lv 1)', value: 1 },
  { label: '标准 (Lv 6)', value: 6 },
  { label: '极限 (Lv 9)', value: 9 }
]

// 格式化语义化版本显示：如 v0.2.8 (7b8f9a2)
function formatSnapshotVersion(item: SnapshotMeta): string {
  if (item.version_tag) {
    return item.version_tag
  }
  const ver = item.harness_version ? 'v' + item.harness_version.replace(/^v/, '') : ''
  const commit = item.git_commit ? item.git_commit.substring(0, 7) : ''
  if (ver && commit) {
    return `${ver} (${commit})`
  }
  return ver || commit || '-'
}

function openCreateModal() {
  const d = new Date()
  let baseName = `快照_${pad(d.getMonth() + 1)}${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}`
  if (snapshots.value.some(s => (s.name || '').trim().toLowerCase() === baseName.toLowerCase())) {
    baseName += `_${pad(d.getSeconds())}`
  }
  snapName.value = baseName
  compressionLevel.value = 1
  showCreateModal.value = true
}

function handleCreateSnapshot() {
  const name = snapName.value.trim()
  if (!name) {
    message.warning('请输入快照名称')
    return false
  }
  if (isDuplicateName.value) {
    message.warning(`已存在同名快照「${name}」，请更换名称`)
    return false
  }

  showCreateModal.value = false

  void snapshotStore.createSnapshot({
    name,
    compression_level: compressionLevel.value
  }).then(res => {
    if (res.success) {
      message.success('快照创建成功')
    } else {
      message.error(res.message || '创建快照失败')
    }
  }).catch((err: any) => {
    message.error(err?.message || '创建快照失败')
  })

  return true
}

function promptRestore(item: SnapshotMeta) {
  dialog.warning({
    title: '确认还原快照？',
    content: `确定要还原到快照「${item.name}」吗？系统将还原快照数据并重新启动服务。`,
    positiveText: '确认还原',
    negativeText: '取消',
    onPositiveClick: () => {
      void handleRestore(item.id, item.name)
    }
  })
}

function promptDelete(item: SnapshotMeta) {
  dialog.warning({
    title: '确认删除快照？',
    content: `确定永久删除快照「${item.name}」？此操作不可撤回。`,
    positiveText: '确认删除',
    negativeText: '取消',
    onPositiveClick: () => {
      void handleDelete(item.id)
    }
  })
}

async function handleRestore(id: string, name?: string) {
  try {
    const res = await snapshotStore.restoreSnapshot(id, name)
    if (res.success) {
      message.success('快照还原成功')
    } else {
      message.error(res.message || '还原快照失败')
    }
  } catch (err: any) {
    message.error(err?.message || '还原快照失败')
  }
}

async function handleDelete(id: string) {
  try {
    const res = await snapshotStore.deleteSnapshot(id)
    if (res.success) {
      message.success('已删除')
    } else {
      message.error(res.message || '删除失败')
    }
  } catch (err: any) {
    message.error(err?.message || '删除失败')
  }
}

function formatSize(bytes: number) {
  if (!bytes || bytes <= 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1)
  return (bytes / Math.pow(k, i)).toFixed(1) + ' ' + sizes[i]
}

function formatTime(timestamp: number) {
  if (!timestamp) return '-'
  const d = new Date(timestamp * 1000)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function pad(n: number) {
  return n < 10 ? '0' + n : String(n)
}

onMounted(() => {
  // 挂载时拉取快照列表与当前进行中的任务进度
  snapshotStore.fetchSnapshots()
})
</script>
