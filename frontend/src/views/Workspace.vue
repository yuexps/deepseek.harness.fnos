<template>
  <div class="w-full flex-1 flex flex-col gap-4 sm:gap-6">
    <!-- 页头标题 -->
    <div
      class="sticky -top-[14px] sm:-top-6 z-20 pt-5 sm:pt-7 pb-2 sm:pb-2.5 bg-[#f5f7fa]/90 dark:bg-[#12141a]/90 backdrop-blur-md flex items-center justify-between gap-3 transition-all duration-200">
      <div class="flex items-baseline gap-2.5 min-w-0">
        <h1 class="text-xl font-bold text-slate-800 dark:text-slate-100 tracking-tight shrink-0">工作区</h1>
        <span v-if="items.length" class="text-xs text-slate-400 dark:text-slate-500 font-medium truncate">
          共 {{ items.length }} 个
        </span>
      </div>

      <n-tooltip trigger="hover" :disabled="isTouch">
        <template #trigger>
          <n-button secondary size="small" class="sm:!h-9 sm:!px-4" :disabled="!dataLibraryPath"
            @click="handleOpenDataLibrary">
            <template #icon>
              <n-icon>
                <Folder />
              </n-icon>
            </template>
            <span>数据目录</span>
          </n-button>
        </template>
        {{ dataLibraryPath ? `在文件管理中打开: ${dataLibraryPath}` : '数据目录路径未配置' }}
      </n-tooltip>
    </div>

    <!-- 数据展示与空状态过渡容器 -->
    <div v-auto-animate class="w-full flex-1">
      <!-- 空状态 -->
      <n-card v-if="!items.length" :bordered="false" class="py-12 text-center shadow-sm rounded-2xl">
        <n-empty description="暂无工作区数据">
          <template #icon>
            <n-icon :size="48" class="text-slate-300 dark:text-slate-600">
              <Folder />
            </n-icon>
          </template>
          <template #extra>
            <span class="text-xs text-slate-400 dark:text-slate-500">请先运行 DeepSeek Harness 并在客户端创建工作区</span>
          </template>
        </n-empty>
      </n-card>

      <!-- 工作区卡片网格 -->
      <n-grid v-else v-auto-animate :cols="2" :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
        <n-gi span="2 m:1" v-for="item in items" :key="item.workspaceId">
          <n-card hoverable :bordered="false" @click="handleOpenWorkspace(item.path)"
            class="cursor-pointer interactive-card select-none shadow-sm group !h-full rounded-2xl">
            <n-thing>
              <!-- 目录图标 -->
              <template #avatar>
                <n-tooltip trigger="hover" :disabled="isTouch">
                  <template #trigger>
                    <div
                      class="w-10 h-10 rounded-xl bg-slate-100 dark:bg-white/[0.06] group-hover:bg-blue-50 dark:group-hover:bg-blue-950/40 text-slate-500 dark:text-slate-400 group-hover:text-fnos-blue dark:group-hover:text-blue-400 flex items-center justify-center transition-all duration-200 group-hover:scale-105">
                      <n-icon :size="22">
                        <Folder />
                      </n-icon>
                    </div>
                  </template>
                  在文件管理中打开 {{ item.title || '此工作区' }}
                </n-tooltip>
              </template>

              <!-- 标题 -->
              <template #header>
                <div
                  class="text-sm font-semibold text-slate-800 dark:text-slate-100 truncate transition-colors group-hover:text-fnos-blue dark:group-hover:text-blue-400">
                  <n-ellipsis :line-clamp="1" :tooltip="!isTouch">
                    {{ item.title || item.workspaceId || '-' }}
                  </n-ellipsis>
                </div>
              </template>

              <!-- 右侧会话标签 -->
              <template #header-extra>
                <n-tooltip trigger="hover" :disabled="isTouch">
                  <template #trigger>
                    <n-tag size="tiny" round :bordered="false" type="info" class="font-mono cursor-default">
                      {{ (item.sessionIds || []).length }} 会话
                    </n-tag>
                  </template>
                  包含 {{ (item.sessionIds || []).length }} 个活动对话会话
                </n-tooltip>
              </template>

              <!-- 路径信息 -->
              <template #description>
                <div class="text-xs text-slate-400 dark:text-slate-500 mt-1">
                  <n-ellipsis :line-clamp="1" :tooltip="!isTouch">
                    {{ item.path || '-' }}
                  </n-ellipsis>
                </div>
              </template>

              <!-- 底部时间信息 -->
              <template #footer>
                <div
                  class="flex items-center justify-between gap-2 text-[11px] text-slate-400 dark:text-slate-500 pt-0.5 w-full min-w-0">
                  <!-- 更新时间 -->
                  <n-tooltip v-if="item.updatedAt" trigger="hover" :disabled="isTouch">
                    <template #trigger>
                      <div class="flex items-center gap-1 shrink-0 cursor-default">
                        <n-icon :size="12">
                          <Clock />
                        </n-icon>
                        <span class="whitespace-nowrap">更新于 <n-time :time="new Date(item.updatedAt)"
                            type="relative" /></span>
                      </div>
                    </template>
                    更新于: {{ new Date(item.updatedAt).toLocaleString() }}
                  </n-tooltip>

                  <!-- 创建时间 -->
                  <n-tooltip v-if="item.createdAt" trigger="hover" :disabled="isTouch">
                    <template #trigger>
                      <div
                        class="flex items-center gap-1 text-slate-400/80 dark:text-slate-500 min-w-0 truncate justify-end cursor-default">
                        <n-icon :size="12" class="shrink-0">
                          <Calendar />
                        </n-icon>
                        <span class="truncate">创建于 <n-time :time="new Date(item.createdAt)"
                            format="yyyy-MM-dd" /></span>
                      </div>
                    </template>
                    创建于: {{ new Date(item.createdAt).toLocaleString() }}
                  </n-tooltip>
                </div>
              </template>
            </n-thing>
          </n-card>
        </n-gi>
      </n-grid>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import {
  NCard,
  NButton,
  NGrid,
  NGi,
  NEmpty,
  NThing,
  NEllipsis,
  NTime,
  NTag,
  NIcon,
  NTooltip,
  useMessage
} from 'naive-ui'
import { Folder, Clock, Calendar } from '@vicons/tabler'
import { useWorkspaceStore } from '../stores/workspace'
import { withAsyncLock } from '../utils/debounce'
import { useIsTouchDevice } from '../utils/device'

const workspaceStore = useWorkspaceStore()
const message = useMessage()
const isTouch = useIsTouchDevice()

const { items, dataLibraryPath } = storeToRefs(workspaceStore)

const handleOpenDataLibrary = withAsyncLock(async () => {
  const res = await workspaceStore.openDataLibrary()
  if (!res.success) {
    message.error(`打开数据目录失败：${res.message || '未知错误'}`)
  }
})

const handleOpenWorkspace = withAsyncLock(async (path: string) => {
  const res = await workspaceStore.openWorkspace(path)
  if (!res.success) {
    message.error(`打开文件管理器失败：${res.message || '未知错误'}`)
  }
})

onMounted(async () => {
  await Promise.all([
    workspaceStore.fetchDataLibraryPath(),
    workspaceStore.fetchWorkspaces()
  ])
})
</script>