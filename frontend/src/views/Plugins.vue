<template>
  <div class="w-full flex-1 flex flex-col gap-4 sm:gap-6 min-w-0 select-none">
    <!-- 同行页头与主操作区 -->
    <div class="flex items-center justify-between gap-3 w-full min-w-0">
      <div class="flex items-baseline gap-2.5 min-w-0">
        <h1 class="text-xl font-bold text-slate-800 dark:text-slate-100 tracking-tight shrink-0">插件管理</h1>
        <span v-if="plugins.length" class="text-xs text-slate-400 dark:text-slate-500 font-medium truncate">
          共 {{ plugins.length }} 个插件
        </span>
      </div>

      <!-- 右侧同行操作区：安装插件展开按钮 -->
      <div class="flex items-center gap-2 shrink-0">
        <n-button type="primary" size="small" @click="showInstallPanel = !showInstallPanel"
          class="!h-8 !px-3 rounded-lg text-xs font-medium shadow-xs shadow-fnos-blue/20 transition-all duration-150 active:scale-95 shrink-0">
          <template #icon>
            <n-icon :size="15">
              <component :is="showInstallPanel ? ChevronUp : Plus" />
            </n-icon>
          </template>
          <span>{{ showInstallPanel ? '收起' : '安装插件' }}</span>
        </n-button>
      </div>
    </div>

    <!-- 插件操作执行中提示横幅 -->
    <div v-if="busy" v-auto-animate class="w-full min-w-0">
      <n-alert type="info" :show-icon="true"
        class="rounded-2xl shadow-xs border border-blue-200 dark:border-blue-900/40">
        <div class="flex items-center justify-between gap-3 min-w-0">
          <div class="flex items-center gap-2 min-w-0 flex-1">
            <n-spin :size="14" class="shrink-0" />
            <span class="text-xs text-blue-800 dark:text-blue-200 font-medium truncate">
              正在执行插件任务（安装 / 卸载 / 更新），请稍候…
            </span>
          </div>
          <n-button type="error" size="tiny" secondary @click="handleCancel"
            class="shrink-0 rounded-lg !h-6 !px-2.5 text-xs font-medium transition-transform duration-150 active:scale-95">
            终止操作
          </n-button>
        </div>
      </n-alert>
    </div>

    <!-- 配置变更重启提醒横幅 -->
    <div v-if="isRunning && needRestart" v-auto-animate class="w-full min-w-0">
      <n-alert type="warning" title="插件配置已变更" :show-icon="true"
        class="rounded-2xl shadow-xs border border-amber-200 dark:border-amber-900/40">
        <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mt-1 min-w-0">
          <p class="text-xs text-amber-800 dark:text-amber-200 min-w-0 flex-1">
            检测到插件安装或配置更新，为了使改动完全生效，建议重启服务。
          </p>
          <n-button type="warning" size="tiny" secondary :loading="isRestarting"
            :disabled="isActionLocked && !isRestarting" @click="promptRestartService"
            class="shrink-0 rounded-lg !h-6 !px-2.5 text-xs font-medium transition-transform duration-150 active:scale-95">
            重启服务
          </n-button>
        </div>
      </n-alert>
    </div>

    <!-- dshmarket 市场插件推荐引导卡片（未安装时展示） -->
    <div v-if="!hasDshMarketInstalled" v-auto-animate class="w-full min-w-0">
      <div
        class="p-3 sm:p-4 rounded-2xl bg-gradient-to-r from-blue-50/80 via-indigo-50/50 to-purple-50/80 dark:from-blue-950/30 dark:via-indigo-950/20 dark:to-purple-950/30 border border-blue-100/80 dark:border-blue-900/30 shadow-xs flex items-center justify-between gap-2.5 sm:gap-3.5 min-w-0">
        <div class="flex items-center gap-2.5 sm:gap-3 min-w-0 flex-1">
          <div
            class="w-8 h-8 sm:w-9 sm:h-9 rounded-xl bg-fnos-blue/10 dark:bg-fnos-blue/20 text-fnos-blue dark:text-blue-400 flex items-center justify-center shrink-0">
            <n-icon :size="18">
              <BuildingStore />
            </n-icon>
          </div>
          <div class="min-w-0 flex-1 space-y-0.5">
            <h3 class="text-xs sm:text-sm font-bold text-slate-800 dark:text-slate-100 truncate">推荐安装「dshmarket」插件市场</h3>
            <p class="text-[11px] sm:text-xs text-slate-500 dark:text-slate-400 leading-normal line-clamp-1 sm:line-clamp-none">
              第三方插件市场，安装后可在DSH设置中直接可视化浏览、搜索与管理社区插件及主题。
            </p>
          </div>
        </div>
        <n-button type="primary" size="small" secondary @click="handleQuickInstallMarket" :disabled="busy"
          class="shrink-0 rounded-lg !h-7 sm:!h-7.5 !px-2.5 sm:!px-3 text-xs font-medium transition-transform duration-150 active:scale-95">
          <template #icon>
            <n-icon :size="13">
              <Download />
            </n-icon>
          </template>
          <span>安装</span>
        </n-button>
      </div>
    </div>

    <!-- 安装插件平滑展开卡片（聚焦标准指令与包名输入） -->
    <div v-if="showInstallPanel" v-auto-animate class="w-full min-w-0">
      <n-card :bordered="false" class="shadow-sm rounded-2xl border border-slate-100 dark:border-white/[0.06]">
        <template #header>
          <div class="flex items-center justify-between gap-3 w-full min-w-0">
            <div class="flex items-center gap-2 min-w-0">
              <span class="text-base font-bold text-slate-800 dark:text-slate-100">安装新插件</span>
            </div>
            <div class="flex items-center gap-2 shrink-0">
              <n-button v-if="busy" type="error" size="small" secondary @click="handleCancel"
                class="!h-7.5 !px-2.5 rounded-lg text-xs font-medium transition-transform duration-150 active:scale-95">
                <span>取消</span>
              </n-button>
              <n-button type="primary" size="small" :loading="busy" :disabled="!canInstall" @click="handleInstall"
                class="!h-7.5 !px-3 rounded-lg text-xs font-medium">
                <template #icon v-if="!busy">
                  <n-icon :size="14">
                    <Plus />
                  </n-icon>
                </template>
                <span>{{ busy ? '正在执行…' : '安装' }}</span>
              </n-button>
            </div>
          </div>
        </template>

        <div class="space-y-3 min-w-0">
          <div class="space-y-1.5 min-w-0">
            <n-input :value="command" @update:value="pluginStore.setCommand" :disabled="busy"
              placeholder="例如: dsh plugin --profile web add dshmarket" clearable size="medium">
              <template #prefix>
                <n-icon class="text-slate-400 dark:text-slate-500">
                  <Terminal2 />
                </n-icon>
              </template>
            </n-input>
            <div
              class="flex items-center justify-between gap-2 text-[11px] text-slate-400 dark:text-slate-500 pl-1 min-w-0">
              <span class="truncate min-w-0 flex-1">支持: npm 包、@scoped 包、github:user/repo</span>
              <a href="javascript:void(0)" @click="openMarketplace"
                class="text-fnos-blue dark:text-blue-400 hover:underline inline-flex items-center gap-0.5 shrink-0 select-none font-medium cursor-pointer">
                <span>插件精选列表</span>
                <n-icon :size="12">
                  <ExternalLink />
                </n-icon>
              </a>
            </div>
          </div>

          <!-- 命令解析反馈折叠动画 -->
          <div v-auto-animate class="min-w-0">
            <n-alert v-if="command.trim()" :type="preview?.valid ? 'success' : 'error'" :show-icon="true"
              class="rounded-xl text-xs">
              <n-ellipsis :line-clamp="2" :tooltip="!isTouch" class="w-full break-all">
                {{ preview?.valid ? `将执行: ${preview.command}` : (preview?.reason || '解析中…') }}
              </n-ellipsis>
            </n-alert>
          </div>
        </div>
      </n-card>
    </div>

    <!-- 已安装插件主体卡片 -->
    <n-card :bordered="false" class="shadow-sm rounded-2xl min-w-0">
      <template #header>
        <div class="flex flex-col md:flex-row md:items-center justify-between gap-3 w-full min-w-0">
          <!-- 左侧：状态分类 Segment Tabs -->
          <div class="flex items-center overflow-x-auto no-scrollbar p-1 w-full md:w-auto min-w-0">
            <n-radio-group v-model:value="filterStatus" size="small" class="!h-8 flex items-center shrink-0 rounded-xl">
              <n-radio-button value="all" class="!h-8 !leading-8 text-xs">
                全部 <span class="text-[11px] opacity-75 font-mono ml-0.5">({{ plugins.length }})</span>
              </n-radio-button>
              <n-radio-button value="live" class="!h-8 !leading-8 text-xs">
                运行中 <span class="text-[11px] opacity-75 font-mono ml-0.5">({{ liveCount }})</span>
              </n-radio-button>
              <n-radio-button value="disabled" class="!h-8 !leading-8 text-xs">
                已停用 <span class="text-[11px] opacity-75 font-mono ml-0.5">({{ disabledCount }})</span>
              </n-radio-button>
            </n-radio-group>
          </div>

          <!-- 右侧：搜索框与刷新按钮 -->
          <div class="flex items-center gap-2 w-full md:w-72 min-w-0 max-w-full">
            <div class="flex-1 min-w-0">
              <n-input v-model:value="searchKeyword" placeholder="搜索插件名 / 描述 / 作者..." size="small" clearable
                class="w-full !h-8 rounded-lg">
                <template #prefix>
                  <n-icon :size="14" class="text-slate-400">
                    <Search />
                  </n-icon>
                </template>
              </n-input>
            </div>

            <n-tooltip trigger="hover" :disabled="isTouch">
              <template #trigger>
                <n-button secondary size="small" :loading="loading || busy" @click="handleRefresh"
                  class="!h-8 !w-8 !p-0 rounded-lg flex items-center justify-center transition-transform duration-150 active:scale-95 shrink-0">
                  <template #icon>
                    <n-icon :size="15">
                      <Refresh />
                    </n-icon>
                  </template>
                </n-button>
              </template>
              刷新插件列表
            </n-tooltip>
          </div>
        </div>
      </template>

      <!-- 插件列表主体与空状态自适应过渡容器 -->
      <div v-auto-animate class="min-w-0">
        <!-- 空状态 -->
        <div v-if="!filteredPlugins.length" class="py-16 text-center min-w-0">
          <n-empty
            :description="loading ? '正在获取插件列表…' : (searchKeyword || filterStatus !== 'all' ? '未找到匹配的插件' : '暂无已安装插件')">
            <template #icon>
              <n-icon :size="48" class="text-slate-300 dark:text-slate-600">
                <Puzzle />
              </n-icon>
            </template>
            <template #extra>
              <n-button v-if="searchKeyword || filterStatus !== 'all'" size="small" secondary @click="clearFilter"
                class="rounded-lg">
                重置筛选条件
              </n-button>
              <n-button v-else type="primary" size="small" @click="showInstallPanel = true" class="rounded-lg">
                安装新插件
              </n-button>
            </template>
          </n-empty>
        </div>

        <!-- 插件列表项 -->
        <n-list v-else hoverable class="divide-y divide-slate-100 dark:divide-white/[0.06] -mx-4 sm:-mx-6"
          v-auto-animate>
          <n-list-item v-for="p in filteredPlugins" :key="p.name"
            class="!px-4 sm:!px-6 !py-4 transition-colors duration-150 min-w-0">
            <!-- 整体布局：桌面端水平顶对齐，移动端分块排布 -->
            <div class="flex flex-col sm:flex-row sm:items-start justify-between gap-3 sm:gap-4 w-full min-w-0">
              <!-- 左侧：图标 + 详细信息主体 -->
              <div class="flex items-start gap-0 sm:gap-3.5 min-w-0 flex-1">
                <!-- 插件状态代表图标 -->
                <div
                  class="hidden sm:flex w-9 h-9 rounded-xl items-center justify-center shrink-0 mt-0.5 transition-transform duration-200 group-hover:scale-105"
                  :class="getPluginIconBg(p.state)">
                  <n-icon :size="18" :class="getPluginIconColor(p.state)">
                    <Puzzle />
                  </n-icon>
                </div>

                <!-- 插件文本详情 -->
                <div class="flex-1 min-w-0 space-y-1 overflow-hidden">
                  <!-- 第一行：标题 + 版本号 + 状态 Tag + 保护 Tag -->
                  <div class="flex items-center flex-nowrap gap-2 w-full min-w-0 h-6 overflow-hidden">
                    <div class="min-w-0 shrink truncate flex items-center">
                      <n-ellipsis :line-clamp="1" :tooltip="!isTouch"
                        class="text-sm font-bold text-slate-800 dark:text-slate-100 font-mono tracking-tight leading-none truncate">
                        {{ p.name }}
                      </n-ellipsis>
                    </div>

                    <n-tag v-if="p.version" size="tiny" round :bordered="false"
                      class="shrink-0 font-mono text-[11px] bg-slate-100 dark:bg-white/[0.08] text-slate-600 dark:text-slate-300 whitespace-nowrap">
                      v{{ p.version }}
                    </n-tag>

                    <!-- 状态 Tag -->
                    <n-tag :type="getStateTagType(p.state)" size="tiny" round :bordered="false"
                      class="shrink-0 text-[11px] font-medium whitespace-nowrap">
                      {{ getStateTagLabel(p.state) }}
                    </n-tag>

                    <!-- 受保护核心 Tag -->
                    <n-tag v-if="p.isProtected" type="info" size="tiny" round :bordered="false"
                      class="shrink-0 text-[11px] whitespace-nowrap">
                      系统核心
                    </n-tag>
                  </div>

                  <!-- 第二行：功能描述 -->
                  <div v-if="p.description"
                    class="text-xs text-slate-500 dark:text-slate-400 leading-relaxed min-w-0 w-full truncate">
                    <n-ellipsis :line-clamp="1" :tooltip="!isTouch" class="truncate w-full block">
                      {{ p.description }}
                    </n-ellipsis>
                  </div>

                  <!-- 第三行：元数据信息工整排版 -->
                  <div
                    class="flex items-center flex-nowrap gap-x-2 text-[11px] text-slate-400 dark:text-slate-500 font-mono min-w-0 w-full pt-0.5 overflow-hidden whitespace-nowrap">
                    <span v-if="p.author" class="inline-flex items-center min-w-0 shrink-0 truncate">
                      <span class="text-slate-300 dark:text-slate-600 mr-1 shrink-0">作者:</span>
                      <n-ellipsis :line-clamp="1" :tooltip="!isTouch" class="truncate">{{ p.author }}</n-ellipsis>
                    </span>

                    <span v-if="p.author && p.spec"
                      class="text-slate-300 dark:text-slate-700 select-none shrink-0">•</span>

                    <!-- 来源 -->
                    <span v-if="p.spec" class="inline-flex items-center min-w-0 shrink truncate">
                      <span class="text-slate-300 dark:text-slate-600 mr-1 shrink-0">来源:</span>
                      <n-ellipsis :line-clamp="1" :tooltip="!isTouch" class="truncate">{{ p.spec }}</n-ellipsis>
                    </span>

                    <span v-if="(p.author || p.spec) && p.homepage"
                      class="text-slate-300 dark:text-slate-700 select-none shrink-0">•</span>

                    <a v-if="p.homepage" :href="p.homepage" target="_blank"
                      class="text-blue-500 dark:text-blue-400 hover:underline inline-flex items-center gap-0.5 shrink-0 font-sans font-medium whitespace-nowrap">
                      <span>主页</span>
                      <n-icon :size="11">
                        <ExternalLink />
                      </n-icon>
                    </a>
                  </div>
                </div>
              </div>

              <!-- 右侧操作区：卸载 -> 更新 -> Switch 开关 -->
              <div
                class="flex items-center justify-between sm:justify-end gap-2.5 shrink-0 pt-2.5 sm:pt-0 border-t sm:border-t-0 border-slate-50 dark:border-white/[0.04] w-full sm:w-auto sm:h-6">
                <!-- 左侧按钮组（卸载与更新） -->
                <div class="flex items-center gap-2 shrink-0">
                  <!-- 1. 卸载按钮 -->
                  <n-button size="small" secondary type="error" :disabled="busy || p.isProtected"
                    @click="promptUninstallPlugin(p.name)"
                    class="!h-7 !px-2.5 rounded-lg text-xs font-medium transition-transform duration-150 active:scale-95 shrink-0">
                    <template #icon>
                      <n-icon>
                        <Trash />
                      </n-icon>
                    </template>
                    <span>卸载</span>
                  </n-button>

                  <!-- 2. 更新按钮 -->
                  <n-tooltip trigger="hover" :disabled="isTouch">
                    <template #trigger>
                      <n-button size="small" secondary :disabled="busy" @click="handleUpdate(p.name)"
                        class="!h-7 !px-2.5 rounded-lg text-xs font-medium transition-transform duration-150 active:scale-95 shrink-0">
                        <template #icon>
                          <n-icon>
                            <Refresh />
                          </n-icon>
                        </template>
                        <span>更新</span>
                      </n-button>
                    </template>
                    检查并拉取该插件最新版本
                  </n-tooltip>
                </div>

                <!-- 3. Switch 开关（基于 cordis.patch.yml 1秒无感热启停） -->
                <n-tooltip trigger="hover" :disabled="!p.isProtected || isTouch">
                  <template #trigger>
                    <div class="flex items-center gap-2 shrink-0">
                      <span class="text-xs text-slate-400 dark:text-slate-500 sm:hidden">
                        {{ p.state === 'live' ? '已启用' : '已停用' }}
                      </span>
                      <n-switch size="medium" :value="p.state === 'live'" :disabled="busy || p.isProtected"
                        @update:value="(val) => handleToggle(p.name, val)" />
                    </div>
                  </template>
                  核心基础设施插件受到保护，不可停用
                </n-tooltip>
              </div>
            </div>
          </n-list-item>
        </n-list>
      </div>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import {
  NCard,
  NInput,
  NButton,
  NAlert,
  NEmpty,
  NList,
  NListItem,
  NSwitch,
  NTag,
  NIcon,
  NRadioGroup,
  NRadioButton,
  NTooltip,
  NEllipsis,
  useMessage,
  useDialog
} from 'naive-ui'
import {
  Plus,
  Refresh,
  Trash,
  Terminal2,
  Puzzle,
  ExternalLink,
  Search,
  ChevronUp,
  BuildingStore,
  Download
} from '@vicons/tabler'
import { usePluginStore } from '../stores/plugin'
import { useSystemStore } from '../stores/system'
import { withAsyncLock } from '../utils/debounce'
import { trimSdk } from '../utils/trimSdk'
import type { PluginState } from '../types/api'

const pluginStore = usePluginStore()
const systemStore = useSystemStore()
const message = useMessage()
const dialog = useDialog()

// 控制安装面板的展开与收起
const showInstallPanel = ref(false)

// 触摸设备判定
const isTouch = ref(typeof window !== 'undefined' && ('ontouchstart' in window || navigator.maxTouchPoints > 0))

const openMarketplace = () => {
  trimSdk.openURL('https://awesome-dsh-plugin.com/zh', '_blank')
}

const {
  plugins,
  loading,
  pluginBusy: busy,
  needRestart,
  command,
  preview,
  searchKeyword,
  filterStatus,
  canInstall,
  liveCount,
  disabledCount,
  filteredPlugins
} = storeToRefs(pluginStore)

const {
  isRunning,
  isActionLocked,
  activeAction
} = storeToRefs(systemStore)

const isRestarting = computed(() => activeAction.value === 'restart')

// 判定用户是否已经安装了 dshmarket
const hasDshMarketInstalled = computed(() => {
  return plugins.value.some(p => p.name === 'dshmarket' || p.name === 'dsh-market')
})

const handleQuickInstallMarket = () => {
  pluginStore.fillDshMarketCommand()
  showInstallPanel.value = true
}

const getStateTagType = (state: PluginState): 'success' | 'default' | 'warning' => {
  switch (state) {
    case 'live': return 'success'
    case 'disabled': return 'default'
    case 'inert': return 'warning'
  }
}

const getStateTagLabel = (state: PluginState): string => {
  switch (state) {
    case 'live': return '运行中'
    case 'disabled': return '已停用'
    case 'inert': return '普通依赖'
  }
}

const getPluginIconBg = (state: PluginState): string => {
  switch (state) {
    case 'live': return 'bg-emerald-50 dark:bg-emerald-950/40'
    case 'inert': return 'bg-amber-50 dark:bg-amber-950/40'
    default: return 'bg-slate-100 dark:bg-white/[0.06]'
  }
}

const getPluginIconColor = (state: PluginState): string => {
  switch (state) {
    case 'live': return 'text-emerald-600 dark:text-emerald-400'
    case 'inert': return 'text-amber-600 dark:text-amber-400'
    default: return 'text-slate-400 dark:text-slate-500'
  }
}

const clearFilter = () => {
  searchKeyword.value = ''
  filterStatus.value = 'all'
}

const handleRestartService = withAsyncLock(async () => {
  const res = await systemStore.sendAction('restart')
  if (res.success) {
    message.success(res.message || '重启指令已发送，正在等待服务就绪…')
  } else {
    message.error(res.message || '重启失败')
  }
})

const handleRefresh = withAsyncLock(async () => {
  await pluginStore.fetchPlugins()
  message.success('插件列表已刷新')
})

const handleInstall = withAsyncLock(async () => {
  const res = await pluginStore.installPlugin()
  if (res.success) {
    message.success(res.message || '已开始执行插件安装')
  } else {
    message.error(res.message || '安装失败')
  }
})

const handleCancel = withAsyncLock(async () => {
  const res = await pluginStore.cancelPluginOp()
  if (res.success) {
    message.warning(res.message || '已发送取消指令，正在终止进程…')
  } else {
    message.error(res.message || '取消失败')
  }
})

const handleToggle = withAsyncLock(async (name: string, enabled: boolean) => {
  const res = await pluginStore.togglePlugin(name, enabled)
  if (res.success) {
    message.success(res.message || (enabled ? '已启用插件' : '已禁用插件'))
  } else {
    message.error(res.message || '操作失败')
  }
})

const handleUpdate = withAsyncLock(async (name: string) => {
  const res = await pluginStore.updatePlugin(name)
  if (res.success) {
    message.success(res.message || `已开始更新 ${name}`)
  } else {
    message.error(res.message || '更新失败')
  }
})

const handleUninstall = withAsyncLock(async (name: string) => {
  const res = await pluginStore.uninstallPlugin(name)
  if (res.success) {
    message.success(res.message || `已开始卸载 ${name}`)
  } else {
    message.error(res.message || '卸载失败')
  }
})

// 重启服务确认弹窗 (全局模态)
function promptRestartService() {
  dialog.warning({
    title: '确认立即重启服务？',
    content: '检测到插件配置已更新，为了使改动完全生效需要重启服务。重启将短暂中断当前所有 AI 对话连接，是否确认继续？',
    positiveText: '确认重启',
    negativeText: '取消',
    onPositiveClick: () => {
      void handleRestartService()
    }
  })
}

// 卸载插件确认弹窗 (全局模态)
function promptUninstallPlugin(name: string) {
  dialog.warning({
    title: '确认卸载插件？',
    content: `确定要卸载插件「${name}」吗？卸载后将自动清理相关的运行时依赖与配置补丁。`,
    positiveText: '确认卸载',
    negativeText: '取消',
    onPositiveClick: () => {
      void handleUninstall(name)
    }
  })
}

onMounted(() => {
  if (!pluginStore.plugins.length) {
    pluginStore.fetchPlugins()
  }
})
</script>

<style scoped>
:deep(.n-list-item__main) {
  min-width: 0;
  width: 100%;
}

:deep(.n-radio-group) {
  border-radius: 10px !important;
  overflow: visible !important;
}

:deep(.n-radio-group .n-radio-button) {
  padding: 0 9px !important;
}

:deep(.n-radio-group .n-radio-button:first-child),
:deep(.n-radio-group .n-radio-button:first-child .n-radio-button__state-border) {
  border-top-left-radius: 10px !important;
  border-bottom-left-radius: 10px !important;
}

:deep(.n-radio-group .n-radio-button:last-child),
:deep(.n-radio-group .n-radio-button:last-child .n-radio-button__state-border) {
  border-top-right-radius: 10px !important;
  border-bottom-right-radius: 10px !important;
}

@media (min-width: 640px) {
  :deep(.n-radio-group .n-radio-button) {
    padding: 0 13px !important;
  }
}

.no-scrollbar::-webkit-scrollbar {
  display: none;
}

.no-scrollbar {
  -ms-overflow-style: none;
  scrollbar-width: none;
}
</style>
