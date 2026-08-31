<template>
  <div v-auto-animate class="w-full flex-1 flex flex-col gap-4 sm:gap-6">
    <!-- 桌面端页头 -->
    <n-page-header class="hidden sm:block">
      <template #title>
        <div class="text-xl font-bold text-slate-800 dark:text-slate-100 tracking-tight">概览</div>
      </template>
    </n-page-header>

    <template v-if="statusLoaded">
      <!-- 状态监控核心卡片 -->
      <n-card :bordered="false" class="shadow-sm rounded-2xl">
        <div class="flex flex-col py-1 sm:py-2">
          <!-- 上半部分：应用标题/版本 + 右侧主操作（进入 Harness） -->
          <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
            <div class="space-y-1 min-w-0 flex-1">
              <div class="flex items-center gap-2.5 flex-wrap min-w-0">
                <span
                  class="text-lg sm:text-xl font-bold text-slate-800 dark:text-slate-100 tracking-tight leading-tight truncate">
                  {{ statusData.name || 'DeepSeek Harness' }}
                </span>
                <!-- 更新构建过程中的目标 Commit 动态小徽章 -->
                <n-tag
                  v-if="isBuilding && statusData.target_commit && statusData.commit && statusData.commit !== statusData.target_commit"
                  type="info" size="small" round :bordered="false"
                  class="font-mono text-xs bg-blue-50 dark:bg-blue-950/50 text-blue-600 dark:text-blue-400 font-semibold px-2 flex items-center gap-1 shadow-sm shrink-0">
                  <template #icon>
                    <n-spin :size="10" class="mr-0.5" />
                  </template>
                  <span>{{ formatShortCommit(statusData.commit) }}</span>
                  <span class="opacity-60">→</span>
                  <span>{{ formatShortCommit(statusData.target_commit) }}</span>
                </n-tag>
              </div>
              <div
                class="text-xs sm:text-sm text-slate-400 dark:text-slate-500 flex items-center gap-1.5 sm:gap-2 flex-nowrap min-w-0">
                <span class="shrink-0">版本: {{ statusData.version || '-' }}</span>
                <span class="text-slate-200 dark:text-slate-700 shrink-0 select-none">|</span>
                <span class="shrink-0 font-mono">Commit: {{ statusData.commit || '-' }}</span>
                <span class="text-slate-200 dark:text-slate-700 shrink-0 select-none">|</span>
                <span class="min-w-0 truncate" :title="statusData.build_time ? `Build: ${statusData.build_time}` : ''">
                  Build: {{ statusData.build_time || '-' }}
                </span>
              </div>
            </div>

            <n-button type="primary" size="large" :disabled="!isRunning" @click="systemStore.openHarnessApp"
              class="w-full sm:w-auto !h-12 px-7 shadow-sm shadow-fnos-blue/20 font-medium rounded-xl text-base transition-transform duration-150 active:scale-95">
              <template #icon>
                <n-icon :size="20">
                  <ExternalLink />
                </n-icon>
              </template>
              <span>进入 Harness</span>
            </n-button>
          </div>

          <!-- 原生分割线 -->
          <n-divider class="!my-5 sm:!my-6" />

          <!-- 下半部分：3 列运行指标网格 -->
          <n-grid :cols="3" :x-gap="16" :y-gap="12" class="text-center" responsive="screen" item-responsive>
            <n-gi>
              <div
                class="py-3 sm:py-3.5 px-3 rounded-2xl bg-slate-50 dark:bg-white/[0.03] border border-slate-100/80 dark:border-white/[0.06] h-full flex flex-col justify-center transition-all duration-200 hover:border-slate-200/90 dark:hover:border-white/[0.12] hover:bg-slate-100/50 dark:hover:bg-white/[0.06]">
                <n-statistic label="运行状态">
                  <template #default>
                    <div class="flex justify-center mt-1">
                      <n-tag :type="statusTagType" size="medium" round :bordered="false"
                        class="font-medium transition-all duration-200">
                        <template #icon v-if="isBuilding || isStarting">
                          <n-spin :size="12" class="mr-1" />
                        </template>
                        {{ statusLabel }}
                      </n-tag>
                    </div>
                  </template>
                </n-statistic>
              </div>
            </n-gi>

            <n-gi>
              <div
                class="py-3 sm:py-3.5 px-3 rounded-2xl bg-slate-50 dark:bg-white/[0.03] border border-slate-100/80 dark:border-white/[0.06] h-full flex flex-col justify-center transition-all duration-200 hover:border-slate-200/90 dark:hover:border-white/[0.12] hover:bg-slate-100/50 dark:hover:bg-white/[0.06]">
                <n-statistic label="运行时间">
                  <template #default>
                    <div class="text-sm sm:text-base font-bold text-slate-700 dark:text-slate-200 truncate mt-0.5">
                      {{ uptimeText }}
                    </div>
                  </template>
                </n-statistic>
              </div>
            </n-gi>

            <n-gi>
              <div
                class="py-3 sm:py-3.5 px-3 rounded-2xl bg-slate-50 dark:bg-white/[0.03] border border-slate-100/80 dark:border-white/[0.06] h-full flex flex-col justify-center transition-all duration-200 hover:border-slate-200/90 dark:hover:border-white/[0.12] hover:bg-slate-100/50 dark:hover:bg-white/[0.06]">
                <n-statistic label="进程 PID">
                  <template #default>
                    <div class="text-sm sm:text-base font-bold text-slate-700 dark:text-slate-200 truncate mt-0.5"
                      :title="isRunning && statusData.pid ? String(statusData.pid) : '-'">
                      {{ isRunning && statusData.pid ? statusData.pid : '-' }}
                    </div>
                  </template>
                </n-statistic>
              </div>
            </n-gi>
          </n-grid>
        </div>
      </n-card>

      <!-- 运行控制区 -->
      <div class="space-y-4">
        <h2 class="text-lg font-bold text-slate-800 dark:text-slate-100 tracking-tight">运行控制</h2>
        <n-grid v-auto-animate :cols="4" :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
          <n-gi span="2 m:1" v-for="a in actionCards" :key="a.action">
            <n-tooltip trigger="hover" :disabled="isTouch || a.disabled || a.loading">
              <template #trigger>
                <div class="h-full">
                  <!-- 运行控制操作卡片 -->
                  <n-card hoverable :bordered="false"
                    class="cursor-pointer text-center interactive-card select-none !p-2 sm:!p-4 shadow-sm group rounded-2xl !h-full"
                    :class="{ 'opacity-50 !cursor-not-allowed !transform-none': a.disabled && !a.loading }"
                    @click="!a.disabled && !a.loading && triggerAction(a)">
                    <div class="flex flex-col items-center justify-center gap-2.5 py-3">
                      <div
                        class="w-12 h-12 rounded-2xl flex items-center justify-center transition-all duration-200 group-hover:scale-110 group-active:scale-95"
                        :class="a.iconBg">
                        <n-spin v-if="a.loading" :size="24" />
                        <n-icon v-else :size="24" :class="a.iconColor" class="transition-transform duration-200">
                          <component :is="a.icon" />
                        </n-icon>
                      </div>
                      <span
                        class="text-sm font-medium text-slate-700 dark:text-slate-300 group-hover:text-slate-900 dark:group-hover:text-white transition-colors duration-150">
                        {{ a.label }}
                      </span>
                    </div>
                  </n-card>
                </div>
              </template>
              {{ a.desc }}
            </n-tooltip>
          </n-gi>
        </n-grid>
      </div>
    </template>

    <!-- 底部状态通知区 -->
    <div v-auto-animate class="space-y-3">
      <!-- 实时构建进度 / 启动中 / 错误信息 -->
      <n-alert v-if="statusData.last_message" :type="isBuilding || isStarting ? 'info' : 'warning'" :show-icon="true"
        class="rounded-2xl shadow-sm">
        {{ statusData.last_message }}
      </n-alert>

      <!-- 实时连接断开提示 -->
      <n-alert v-if="!wsConnected" type="error" :show-icon="true" class="rounded-2xl shadow-sm">
        实时连接已断开，正在自动重连…
      </n-alert>
    </div>

    <!-- 发现新版本升级确认弹窗 -->
    <n-modal v-model:show="showUpdateModal" preset="dialog" type="info" title="发现新版本" positive-text="立即更新"
      negative-text="暂不更新" :loading="isActionLocked" @positive-click="confirmUpgrade"
      @negative-click="showUpdateModal = false">
      <div class="space-y-3 pt-1 text-xs leading-relaxed text-slate-600 dark:text-slate-300">
        <!-- 版本信息对比 -->
        <div
          class="p-2.5 bg-slate-50 dark:bg-white/[0.03] rounded-xl border border-slate-200/70 dark:border-slate-800 flex items-center justify-between gap-3 font-mono">
          <div class="flex flex-col gap-0.5 min-w-0">
            <span class="text-[11px] text-slate-400 dark:text-slate-500 font-sans">当前版本</span>
            <span class="font-semibold text-slate-700 dark:text-slate-200 text-xs">
              {{ formatVersionDisplay(updateInfo?.current_version || statusData.version, updateInfo?.current_commit ||
                statusData.commit) }}
            </span>
          </div>
          <div class="text-slate-300 dark:text-slate-600 text-sm font-sans shrink-0">→</div>
          <div class="flex flex-col gap-0.5 min-w-0 items-end text-right">
            <span class="text-[11px] text-blue-600 dark:text-blue-400 font-sans font-medium">目标版本</span>
            <span class="font-semibold text-blue-600 dark:text-blue-400 text-xs">
              {{ formatVersionDisplay(updateInfo?.remote_version || updateInfo?.current_version || statusData.version,
                updateInfo?.remote_commit) }}
            </span>
          </div>
        </div>

        <!-- 强警示高危警告区 -->
        <div class="p-3 rounded-xl bg-rose-500/[0.08] dark:bg-rose-500/[0.15] border border-rose-500/30 space-y-1.5">
          <div class="font-bold text-rose-600 dark:text-rose-400 text-xs tracking-wide flex items-center gap-1.5">
            <n-icon :size="15" class="shrink-0">
              <AlertTriangle />
            </n-icon>
            <span>破坏性变更风险</span>
          </div>
          <div class="text-[11.5px] leading-relaxed text-rose-700 dark:text-rose-300 font-medium">
            上游 DSH 频繁存在破坏性变更，升级后未适配的第三方插件将直接瘫痪，并可能导致服务无法启动。
          </div>
          <div class="text-[11px] text-rose-600/80 dark:text-rose-400/80 leading-normal pt-0.5">
            稳定使用请勿盲目升级。若更新后崩溃，可在 <span class="font-semibold text-rose-700 dark:text-rose-200">设置</span> 中 <span
              class="font-semibold text-rose-700 dark:text-rose-200">重置修复</span> 回退内置版本。
          </div>
        </div>
      </div>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, type Component } from 'vue'
import { storeToRefs } from 'pinia'
import {
  NPageHeader,
  NCard,
  NDivider,
  NButton,
  NTag,
  NGrid,
  NGi,
  NStatistic,
  NAlert,
  NSpin,
  NIcon,
  NTooltip,
  NModal,
  useMessage,
  useDialog
} from 'naive-ui'
import {
  ExternalLink,
  PlayerPlay,
  PlayerStop,
  Refresh,
  Download,
  Tools,
  AlertTriangle
} from '@vicons/tabler'
import { useSystemStore } from '../stores/system'
import { withAsyncLock } from '../utils/debounce'
import { useIsTouchDevice } from '../utils/device'
import type { CheckUpdateResult } from '../types/api'

const systemStore = useSystemStore()
const message = useMessage()
const dialog = useDialog()
const isTouch = useIsTouchDevice()

const showUpdateModal = ref(false)
const updateInfo = ref<CheckUpdateResult | null>(null)

const {
  statusData,
  statusLoaded,
  wsConnected,
  activeAction,
  isCheckingUpdate,
  isActionLocked,
  isRunning,
  isStarting,
  isBuilding,
  statusTagType,
  statusLabel,
  uptimeText
} = storeToRefs(systemStore)

onMounted(() => {
  systemStore.fetchInitialStatus()
})

function formatShortCommit(c?: string): string {
  if (!c || c === '-') return '-'
  return c.length > 7 ? c.slice(0, 7) : c
}

function formatVersionDisplay(ver?: string, commit?: string): string {
  const v = (ver || '').replace(/^v/i, '').trim()
  const c = formatShortCommit(commit)
  if (v && c && c !== '-') {
    return `v${v} (${c})`
  }
  if (v) return `v${v}`
  if (c && c !== '-') return c
  return '-'
}

interface ActionCard {
  action: string
  icon: Component
  label: string
  desc: string
  iconBg: string
  iconColor: string
  disabled: boolean
  loading: boolean
  confirmText?: string
}

const isRestarting = computed(() => activeAction.value === 'restart')
const showStopCard = computed(() => isRunning.value || isRestarting.value)

const actionCards = computed<ActionCard[]>(() => [
  showStopCard.value
    ? {
      action: 'stop',
      icon: PlayerStop,
      label: '停止服务',
      desc: '终止 DeepSeek Harness 后台运行进程',
      iconBg: 'bg-rose-50 dark:bg-rose-950/30 group-hover:bg-rose-100 dark:group-hover:bg-rose-950/50',
      iconColor: 'text-rose-600 dark:text-rose-400',
      disabled: isActionLocked.value,
      loading: activeAction.value === 'stop'
    }
    : {
      action: 'start',
      icon: PlayerPlay,
      label: isStarting.value ? '服务启动中' : '启动服务',
      desc: isStarting.value ? '正在拉起服务主进程并等待就绪…' : '拉起 DeepSeek Harness 后台核心服务',
      iconBg: 'bg-emerald-50 dark:bg-emerald-950/30 group-hover:bg-emerald-100 dark:group-hover:bg-emerald-950/50',
      iconColor: 'text-emerald-600 dark:text-emerald-400',
      disabled: isActionLocked.value,
      loading: isStarting.value || activeAction.value === 'start'
    },
  {
    action: 'restart',
    icon: Refresh,
    label: '重启服务',
    desc: '热重启后台进程，即时生效最新配置或插件变更',
    iconBg: 'bg-amber-50 dark:bg-amber-950/30 group-hover:bg-amber-100 dark:group-hover:bg-amber-950/50',
    iconColor: 'text-amber-600 dark:text-amber-400',
    disabled: !isRestarting.value && (!isRunning.value || isActionLocked.value),
    loading: isRestarting.value
  },
  {
    action: 'check_update',
    icon: Download,
    label: isCheckingUpdate.value ? '检查中…' : '检查更新',
    desc: '检查远程代码更新，检测到新版本时确认后再同步依赖并构建',
    iconBg: 'bg-blue-50 dark:bg-blue-950/30 group-hover:bg-blue-100 dark:group-hover:bg-blue-950/50',
    iconColor: 'text-fnos-blue dark:text-blue-400',
    disabled: isActionLocked.value || isCheckingUpdate.value,
    loading: isCheckingUpdate.value || activeAction.value === 'upgrade' || (isBuilding.value && activeAction.value !== 'rebuild')
  },
  {
    action: 'rebuild',
    icon: Tools,
    label: '强制重建',
    desc: '重新拉取全部依赖并完整编译，用于修复异常损坏的环境',
    iconBg: 'bg-purple-50 dark:bg-purple-950/30 group-hover:bg-purple-100 dark:group-hover:bg-purple-950/50',
    iconColor: 'text-purple-600 dark:text-purple-400',
    disabled: isActionLocked.value && activeAction.value !== 'rebuild',
    loading: activeAction.value === 'rebuild',
    confirmText: '强制重建将重新拉取依赖并编译，耗时较长，确定继续？'
  }
])

const confirmUpgrade = async () => {
  showUpdateModal.value = false
  const res = await systemStore.sendAction('upgrade')
  if (res.success) {
    message.success(res.message || '已开始更新并构建')
  } else {
    message.error(res.message || '更新启动失败')
  }
}

const handleAction = withAsyncLock(async (action: string) => {
  if (action === 'check_update') {
    const res = await systemStore.checkUpdate()
    if (!res.success) {
      message.error(res.message || '检查更新失败，请检查网络')
      return
    }
    if (res.data.has_update) {
      updateInfo.value = res.data
      showUpdateModal.value = true
    } else {
      message.info(res.data.message || '当前已是最新版本')
    }
    return
  }

  const res = await systemStore.sendAction(action)
  if (res.success) {
    message.success(res.message || '操作成功')
  } else {
    message.error(res.message || '操作失败')
  }
})

// 统一动作触发（支持全局模态二次确认）
function triggerAction(a: ActionCard) {
  if (a.confirmText) {
    dialog.warning({
      title: `确认${a.label}？`,
      content: a.confirmText,
      positiveText: '确认',
      negativeText: '取消',
      onPositiveClick: () => {
        void handleAction(a.action)
      }
    })
    return
  }
  void handleAction(a.action)
}
</script>