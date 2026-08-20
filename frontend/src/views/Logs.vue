<template>
  <div class="w-full h-[calc(100dvh-82px)] sm:h-[calc(100dvh-48px)] flex flex-col min-h-0 overflow-hidden">
    <!-- 原生卡片包裹的日志视图：固定高度并撑满可用纵向空间 -->
    <n-card :bordered="false" class="flex-1 flex flex-col shadow-sm rounded-2xl min-h-0 overflow-hidden"
      content-style="display: flex; flex-direction: column; flex: 1; min-height: 0; overflow: hidden; padding-top: 0;">
      <!-- 标题与操作栏 -->
      <template #header>
        <span class="text-base sm:text-lg font-bold text-slate-800 dark:text-slate-100 tracking-tight">运行日志</span>
      </template>

      <template #header-extra>
        <n-flex :size="8" align="center" :wrap="false">
          <!-- 自动滚动开关 -->
          <n-button size="small" secondary @click="autoScroll = !autoScroll">
            <div class="flex items-center gap-1.5">
              <span>自动滚动</span>
              <n-switch :value="autoScroll" size="small" @click.stop="autoScroll = !autoScroll" />
            </div>
          </n-button>

          <!-- 下载日志按钮 -->
          <n-button size="small" secondary @click="logStore.downloadLogs" title="下载日志"
            class="!px-2 sm:!px-2.5">
            <div class="flex items-center justify-center gap-1">
              <n-icon :size="16">
                <Download />
              </n-icon>
              <span class="hidden sm:inline">下载</span>
            </div>
          </n-button>

          <!-- 清空日志按钮 -->
          <n-button size="small" secondary type="error" title="清空日志" @click="promptClearLogs"
            class="!px-2 sm:!px-2.5">
            <div class="flex items-center justify-center gap-1">
              <n-icon :size="16">
                <Trash />
              </n-icon>
              <span class="hidden sm:inline">清空</span>
            </div>
          </n-button>
        </n-flex>
      </template>

      <!-- 日志容器与悬浮回到底部按钮 -->
      <div ref="logContainerRef" class="relative flex-1 min-h-0 flex flex-col overflow-hidden">
        <!-- Naive UI 原生日志组件：自适应撑满卡片高度并在内部滚动，支持 highlight.js 语法高亮与鼠标划选复制 -->
        <n-log ref="logInstRef" :log="displayedText" :hljs="hljs" language="harness-log"
          :font-size="12" :line-height="1.5" trim
          class="flex-1 min-h-0 bg-slate-50 dark:bg-[#12141a] rounded-xl p-3 sm:p-4 border border-slate-100/80 dark:border-white/[0.08] select-text cursor-text overflow-hidden"
          style="height: 100%;" />

        <!-- 正中央优雅居中加载遮罩 -->
        <transition name="fade">
          <div
            v-if="fetching"
            class="absolute inset-0 z-20 flex flex-col items-center justify-center bg-slate-50/85 dark:bg-[#12141a]/85 backdrop-blur-[1px] rounded-xl pointer-events-none"
          >
            <n-spin size="medium">
              <template #description>
                <span class="text-xs text-slate-500 dark:text-slate-400 font-medium mt-2">正在获取运行日志…</span>
              </template>
            </n-spin>
          </div>
        </transition>

        <!-- 悬浮回到底部按钮 -->
        <transition name="fade">
          <div v-show="showScrollToBottom" class="absolute right-3.5 bottom-3.5 sm:right-4 sm:bottom-4 z-10">
            <n-tooltip trigger="hover" placement="left" :disabled="isTouch">
              <template #trigger>
                <n-button size="small" secondary @click="manualScrollToBottom"
                  class="!w-7 !h-7 sm:!w-8 sm:!h-8 !p-0 shadow-sm border border-slate-200/90 dark:border-white/[0.1] bg-white/95 dark:bg-[#1e212b]/95 transition-transform duration-150 active:scale-90">
                  <div class="flex items-center justify-center">
                    <n-icon :size="16" class="text-slate-600 dark:text-slate-300">
                      <ArrowDown />
                    </n-icon>
                  </div>
                </n-button>
              </template>
              回到底部
            </n-tooltip>
          </div>
        </transition>
      </div>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, onActivated, nextTick } from 'vue'
import { storeToRefs } from 'pinia'
import {
  NCard,
  NSwitch,
  NButton,
  NFlex,
  NIcon,
  NTooltip,
  NLog,
  NSpin,
  useMessage,
  useDialog,
  type LogInst
} from 'naive-ui'
import { Download, Trash, ArrowDown } from '@vicons/tabler'
import hljs from 'highlight.js/lib/core'
import { useLogStore } from '../stores/log'
import { withAsyncLock } from '../utils/debounce'
import { useIsTouchDevice } from '../utils/device'

// 注册专用的 harness-log 日志高亮规则
hljs.registerLanguage('harness-log', () => ({
  contains: [
    // 错误与致命异常
    {
      className: 'type',
      begin: /\[FATAL\]|\[ERROR\]/
    },
    // 警告级别
    {
      className: 'keyword',
      begin: /\[WARN\]|\[WARNING\]/
    },
    // 信息级别
    {
      className: 'meta',
      begin: /\[INFO\]/
    },
    // 时间戳 (如 2026/08/16 13:08:24 或 2026-08-16 13:08:24)
    {
      className: 'comment',
      begin: /\d{4}[-/]\d{2}[-/]\d{2}(?:[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?)?/
    },
    // URL 地址
    {
      className: 'link',
      begin: /https?:\/\/[^\s]+/
    },
    // 路径
    {
      className: 'string',
      begin: /(?:\/[\w.-]+)+\/?/
    },
    // 数字与 PID / 端口等
    {
      className: 'number',
      begin: /\b\d+\b/
    }
  ]
}))

const logStore = useLogStore()
const message = useMessage()
const dialog = useDialog()
const isTouch = useIsTouchDevice()
const { displayedText, logAutoScroll: autoScroll, fetching } = storeToRefs(logStore)

const logInstRef = ref<LogInst | null>(null)
const logContainerRef = ref<HTMLElement | null>(null)
const showScrollToBottom = ref(false)
let scrollEl: HTMLElement | null = null
let offFlush: (() => void) | null = null

const handleScroll = () => {
  if (!scrollEl) return
  const distanceFromBottom = scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight
  showScrollToBottom.value = distanceFromBottom > 40
}

const scrollToBottom = () => {
  if (!autoScroll.value) return
  nextTick(() => {
    logInstRef.value?.scrollTo({ position: 'bottom', silent: true })
    showScrollToBottom.value = false
  })
}

const manualScrollToBottom = () => {
  nextTick(() => {
    logInstRef.value?.scrollTo({ position: 'bottom', silent: false })
    showScrollToBottom.value = false
  })
}

const handleClear = withAsyncLock(async () => {
  const res = await logStore.clearLogs()
  if (res.success) {
    message.success(res.message || '运行日志已清空')
  } else {
    message.error(res.message || '清空日志失败')
  }
})

// 清空日志全局模态二次确认
function promptClearLogs() {
  dialog.warning({
    title: '确认清空运行日志？',
    content: '确定要清空所有历史运行日志吗？清空后将无法恢复。',
    positiveText: '确认清空',
    negativeText: '取消',
    onPositiveClick: () => {
      void handleClear()
    }
  })
}

onMounted(() => {
  if (!logStore.hasLoadedSnapshot) {
    logStore.fetchLogs().then(() => {
      scrollToBottom()
    })
  } else {
    scrollToBottom()
  }

  offFlush = logStore.onFlush(() => {
    scrollToBottom()
  })

  // 绑定日志滚动容器的实时位置监听
  nextTick(() => {
    scrollEl = logContainerRef.value?.querySelector('.n-scrollbar-container') as HTMLElement | null
    scrollEl?.addEventListener('scroll', handleScroll, { passive: true })
  })
})

// 切回日志页面时自动平滑同步滚动位置
onActivated(() => {
  if (autoScroll.value) {
    scrollToBottom()
  }
})

onUnmounted(() => {
  offFlush?.()
  scrollEl?.removeEventListener('scroll', handleScroll)
})
</script>

<style scoped>
:deep(.hljs-type) {
  color: #ef4444;
  font-weight: 600;
}
:deep(.hljs-keyword) {
  color: #f59e0b;
  font-weight: 600;
}
:deep(.hljs-meta) {
  color: #2563eb;
  font-weight: 600;
}
:deep(.hljs-comment) {
  color: #94a3b8;
}
:deep(.hljs-number) {
  color: #0891b2;
}
:deep(.hljs-string) {
  color: #059669;
}
:deep(.hljs-link) {
  color: #4f46e5;
  text-decoration: underline;
}

/* 深色模式下语法高亮对比度调优 */
html.dark :deep(.hljs-type) {
  color: #f87171;
}
html.dark :deep(.hljs-keyword) {
  color: #fbbf24;
}
html.dark :deep(.hljs-meta) {
  color: #60a5fa;
}
html.dark :deep(.hljs-comment) {
  color: #64748b;
}
html.dark :deep(.hljs-number) {
  color: #38bdf8;
}
html.dark :deep(.hljs-string) {
  color: #34d399;
}
html.dark :deep(.hljs-link) {
  color: #818cf8;
}

/* 居中加载遮罩淡入淡出动效 */
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

/* 悬浮按钮过渡动效 */
.fade-scale-enter-active,
.fade-scale-leave-active {
  transition: opacity 0.2s ease, transform 0.2s ease;
}

.fade-scale-enter-from,
.fade-scale-leave-to {
  opacity: 0;
  transform: scale(0.85) translateY(4px);
}
</style>