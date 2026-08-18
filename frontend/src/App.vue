<template>
  <n-config-provider :theme="currentTheme" :theme-overrides="currentThemeOverrides">
    <n-global-style />
    <n-loading-bar-provider>
      <n-dialog-provider>
        <n-notification-provider>
          <n-message-provider>
            <!-- 整体视口容器：Naive UI 原生 Layout -->
            <n-layout has-sider position="absolute" class="h-screen w-screen bg-[#f5f7fa] dark:bg-[#12141a]">
              <!-- 桌面端侧边栏 Sider：Naive UI 原生 NLayoutSider -->
              <n-layout-sider
                bordered
                :width="240"
                :native-scrollbar="false"
                class="hidden sm:block select-none z-10"
                content-style="display: flex; flex-direction: column; justify-content: space-between; height: 100%;"
              >
                <!-- 顶部品牌与主导航菜单 -->
                <div class="flex flex-col gap-3 p-3">
                  <!-- 应用品牌标题卡片 -->
                  <div class="flex items-center gap-3 px-3 py-3 rounded-2xl bg-slate-50 dark:bg-white/[0.04] border border-slate-100/80 dark:border-white/[0.06] transition-all duration-200 hover:border-slate-200 dark:hover:border-white/[0.12] hover:bg-slate-100/50 dark:hover:bg-white/[0.07]">
                    <img src="/favicon.svg" alt="logo" class="w-8 h-8 rounded-xl object-contain shrink-0 transition-transform duration-200 hover:scale-105" />
                    <div class="min-w-0 flex-1">
                      <div class="text-sm font-bold text-slate-800 dark:text-slate-100 leading-tight truncate">DeepSeek</div>
                      <div class="text-[11px] text-slate-400 dark:text-slate-500 font-medium truncate mt-0.5">
                        Harness 管理器
                      </div>
                    </div>
                  </div>

                  <!-- 主导航菜单 -->
                  <n-menu
                    :value="tab"
                    :options="menuOptions"
                    @update:value="handleMenuSelect"
                  />
                </div>

                <!-- 底部设置项：统一采用 NMenu 驱动交互与主题高亮 -->
                <div class="p-3">
                  <n-divider class="!my-2" />
                  <n-menu
                    :value="tab"
                    :options="settingsMenuOptions"
                    @update:value="handleMenuSelect"
                  />
                </div>
              </n-layout-sider>

                <!-- 右侧主界面（滚动 Content + 移动端 Footer） -->
                <n-layout
                  class="h-full flex-1 flex flex-col overflow-hidden bg-[#f5f7fa] dark:bg-[#12141a]"
                  content-style="display: flex; flex-direction: column; height: 100%; flex: 1;"
                >
                  <!-- 主内容滚动区域：Naive UI 原生 NLayoutContent -->
                  <n-layout-content
                    :native-scrollbar="false"
                    content-class="app-content-scroll"
                    content-style="min-height: 100%; display: flex; flex-direction: column; align-items: center;"
                    class="flex-1"
                  >
                    <!-- 全局统一宽度约束容器 -->
                    <div class="w-full max-w-6xl flex-1 flex flex-col min-h-0">
                      <Transition name="view-fade-slide" mode="out-in">
                        <KeepAlive>
                          <component :is="currentView" :key="tab" />
                        </KeepAlive>
                      </Transition>
                    </div>
                    <n-back-top :bottom="70" :right="20" class="sm:!bottom-8 sm:!right-8" />
                  </n-layout-content>

                <!-- 移动端底部导航 Tabbar：全套纯正 Naive UI 组件 -->
                <n-layout-footer
                  bordered
                  position="absolute"
                  class="sm:hidden z-50 !bg-white/95 dark:!bg-[#181b22]/95 !backdrop-blur-md px-1 pt-1 shadow-lg mobile-tabbar-footer"
                >
                  <n-flex justify="space-around" align="center" :wrap="false" class="w-full">
                    <n-button
                      v-for="t in mobileTabs"
                      :key="t.key"
                      text
                      :type="tab === t.key ? 'primary' : 'default'"
                      @click="tab = t.key"
                      class="flex-1 !py-1 !px-0 transition-transform duration-150 active:scale-90"
                    >
                      <div class="flex flex-col items-center gap-0.5 select-none">
                        <n-icon :size="20" class="transition-transform duration-200" :class="tab === t.key ? 'scale-110' : 'scale-100'">
                          <component :is="t.icon" />
                        </n-icon>
                        <span
                          class="text-[11px] leading-tight transition-colors duration-150"
                          :class="tab === t.key ? 'font-bold text-fnos-blue dark:text-blue-400' : 'font-normal text-slate-500 dark:text-slate-400'"
                        >
                          {{ t.label }}
                        </span>
                      </div>
                    </n-button>
                  </n-flex>
                </n-layout-footer>
              </n-layout>
            </n-layout>
          </n-message-provider>
        </n-notification-provider>
      </n-dialog-provider>
    </n-loading-bar-provider>
  </n-config-provider>
</template>

<script setup lang="ts">
import { h, ref, computed, watch, onMounted, type Component } from 'vue'
import {
  NConfigProvider,
  NGlobalStyle,
  NMessageProvider,
  NDialogProvider,
  NNotificationProvider,
  NLoadingBarProvider,
  NLayout,
  NLayoutSider,
  NLayoutContent,
  NLayoutFooter,
  NMenu,
  NDivider,
  NFlex,
  NButton,
  NBackTop,
  NIcon,
  darkTheme,
  useOsTheme,
  type MenuOption
} from 'naive-ui'
import {
  Dashboard,
  Folder,
  Puzzle,
  FileText,
  Settings
} from '@vicons/tabler'
import { getThemeOverrides } from './theme'
import Overview from './views/Overview.vue'
import Logs from './views/Logs.vue'
import SettingsView from './views/Settings.vue'
import Workspace from './views/Workspace.vue'
import Plugins from './views/Plugins.vue'
import { useAppStore } from './stores/app'
import { trimSdk } from './utils/trimSdk'

const appStore = useAppStore()
const osTheme = useOsTheme()
const themeMode = ref<'light' | 'dark'>(osTheme.value === 'dark' ? 'dark' : 'light')

// 监听系统偏好自动切换（当非宿主覆盖时）
watch(osTheme, (newOsTheme) => {
  if (trimSdk.isStandaloneWeb || !trimSdk.isWeb) {
    themeMode.value = newOsTheme === 'dark' ? 'dark' : 'light'
  }
})

// 同步 HTML 根节点 class 与 dataset
watch(themeMode, (mode) => {
  document.documentElement.dataset.theme = mode
  if (mode === 'dark') {
    document.documentElement.classList.add('dark')
  } else {
    document.documentElement.classList.remove('dark')
  }
}, { immediate: true })

const currentTheme = computed(() => (themeMode.value === 'dark' ? darkTheme : null))
const currentThemeOverrides = computed(() => getThemeOverrides(themeMode.value))

type TabKey = 'overview' | 'workspace' | 'logs' | 'plugins' | 'settings'

const tabLabels: Record<TabKey, string> = {
  overview: '概览 · DeepSeek Harness',
  workspace: '工作区 · DeepSeek Harness',
  plugins: '插件管理 · DeepSeek Harness',
  logs: '运行日志 · DeepSeek Harness',
  settings: '应用设置 · DeepSeek Harness'
}

const views: Record<TabKey, Component> = {
  overview: Overview,
  workspace: Workspace,
  logs: Logs,
  plugins: Plugins,
  settings: SettingsView
}

function renderIcon(icon: Component) {
  return () => h(NIcon, null, { default: () => h(icon) })
}

const menuOptions: MenuOption[] = [
  { key: 'overview', label: '概览', icon: renderIcon(Dashboard) },
  { key: 'workspace', label: '工作区', icon: renderIcon(Folder) },
  { key: 'plugins', label: '插件管理', icon: renderIcon(Puzzle) },
  { key: 'logs', label: '运行日志', icon: renderIcon(FileText) }
]

const settingsMenuOptions: MenuOption[] = [
  { key: 'settings', label: '应用设置', icon: renderIcon(Settings) }
]

const mobileTabs = [
  { key: 'overview' as TabKey, label: '概览', icon: Dashboard },
  { key: 'workspace' as TabKey, label: '工作区', icon: Folder },
  { key: 'plugins' as TabKey, label: '插件', icon: Puzzle },
  { key: 'logs' as TabKey, label: '日志', icon: FileText },
  { key: 'settings' as TabKey, label: '设置', icon: Settings }
]

const tab = computed<TabKey>({
  get: () => appStore.currentTab as TabKey,
  set: (v) => appStore.setTab(v)
})

watch(tab, (newTab) => {
  trimSdk.setTitle(tabLabels[newTab] || 'DeepSeek Harness')
}, { immediate: true })

const handleMenuSelect = (key: string) => {
  tab.value = key as TabKey
}

const currentView = computed(() => views[tab.value])

onMounted(() => {
  appStore.init()
  trimSdk.initPlatformTheme((theme) => {
    themeMode.value = theme
  })
})
</script>

<style scoped>
:deep(.app-content-scroll) {
  padding: 14px 14px calc(68px + env(safe-area-inset-bottom, 0px)) 14px;
}
.mobile-tabbar-footer {
  padding-bottom: calc(8px + env(safe-area-inset-bottom, 0px));
}
@media (min-width: 640px) {
  :deep(.app-content-scroll) {
    padding: 24px;
  }
}
</style>