<template>
  <div class="w-full flex-1 flex flex-col gap-4 sm:gap-6">
    <!-- 页头与操作区 -->
    <div
      class="sticky -top-[14px] sm:-top-6 z-20 -mt-3.5 sm:-mt-4 pt-5 sm:pt-7 pb-2 sm:pb-2.5 bg-[#f5f7fa]/90 dark:bg-[#12141a]/90 backdrop-blur-md flex items-center justify-between gap-3 w-full transition-all duration-200">
      <h1 class="text-xl font-bold text-slate-800 dark:text-slate-100 tracking-tight">应用设置</h1>

      <!-- 右侧同行操作按钮组 -->
      <div v-auto-animate class="flex items-center gap-2 shrink-0">
        <n-button v-if="isChanged" size="small" :disabled="saving" @click="handleReset"
          class="!h-8 !px-3 rounded-lg text-xs font-medium transition-transform duration-150 active:scale-95">
          <template #icon>
            <n-icon :size="15">
              <X />
            </n-icon>
          </template>
          取消
        </n-button>

        <n-button type="primary" size="small" :loading="saving" :disabled="!isChanged || loadError || !configLoaded"
          @click="handleSave"
          class="!h-8 !px-3 rounded-lg text-xs font-medium shadow-xs shadow-fnos-blue/20 transition-all duration-150 active:scale-95 shrink-0">
          保存设置
        </n-button>
      </div>
    </div>

    <!-- 加载状态与表单内容过渡容器 -->
    <div v-auto-animate class="w-full flex-1">
      <!-- 加载失败轻量状态 -->
      <n-card v-if="loadError && !configLoaded" :bordered="false" class="shadow-sm py-12 text-center rounded-2xl">
        <n-empty description="配置加载失败">
          <template #extra>
            <n-button size="small" secondary @click="configStore.fetchConfig(true)"
              class="transition-transform duration-150 active:scale-95">
              重新加载
            </n-button>
          </template>
        </n-empty>
      </n-card>

      <!-- 配置表单内容 -->
      <n-form v-else ref="formRef" :model="config" :rules="rules" label-placement="top" size="medium">
        <div class="flex flex-col gap-4 sm:gap-6">
          <!-- 核心服务网络与端口配置卡片 -->
          <n-card title="核心服务" :bordered="false" class="shadow-sm rounded-2xl">
            <n-grid :cols="2" :x-gap="20" :y-gap="16" responsive="screen" item-responsive>
              <!-- 第一行：内部端口 反代端口 -->
              <n-gi span="2 m:1">
                <n-form-item label="内部监听端口" path="server_port">
                  <template #label>
                    <div class="flex items-center gap-1.5">
                      <span>内部监听端口</span>
                      <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                        <template #trigger>
                          <n-icon size="14"
                            class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                            <Help />
                          </n-icon>
                        </template>
                        DeepSeek Harness 本地后端进程监听端口，默认 2298
                      </n-tooltip>
                    </div>
                  </template>
                  <n-input-number v-model:value="config.server_port" :min="1" :max="65535" placeholder="2298"
                    class="w-full" />
                </n-form-item>
              </n-gi>

              <n-gi span="2 m:1">
                <n-form-item label="反向代理端口" path="proxy_port">
                  <template #label>
                    <div class="flex items-center gap-1.5">
                      <span>反向代理端口</span>
                      <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                        <template #trigger>
                          <n-icon size="14"
                            class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                            <Help />
                          </n-icon>
                        </template>
                        对外暴露的代理访问端口 (默认 2299)，用于 Web 客户端直连
                      </n-tooltip>
                    </div>
                  </template>
                  <n-input-number v-model:value="config.proxy_port" :min="1" :max="65535" placeholder="2299"
                    class="w-full" />
                </n-form-item>
              </n-gi>

              <!-- 第二行：堆内存上限 反代密码 -->
              <n-gi span="2 m:1">
                <n-form-item label="堆内存上限" path="heap_memory_limit">
                  <template #label>
                    <div class="flex items-center gap-1.5">
                      <span>堆内存上限 (Heap Memory)</span>
                      <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                        <template #trigger>
                          <n-icon size="14"
                            class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                            <Help />
                          </n-icon>
                        </template>
                        Node.js 核心服务最大堆内存限制 (--max-old-space-size)，若分析超大项目发生内存溢出时可调高。
                      </n-tooltip>
                    </div>
                  </template>
                  <n-select v-model:value="config.heap_memory_limit" :options="heapMemoryOptions"
                    placeholder="自动 (系统默认)" />
                </n-form-item>
              </n-gi>

              <n-gi span="2 m:1">
                <n-form-item label="访问控制密码" path="access_password">
                  <template #label>
                    <div class="flex items-center gap-1.5">
                      <span>反代访问密码</span>
                      <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                        <template #trigger>
                          <n-icon size="14"
                            class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                            <Help />
                          </n-icon>
                        </template>
                        反向代理端口的访问密码，留空则不开启访问校验
                      </n-tooltip>
                    </div>
                  </template>
                  <n-input type="password" show-password-on="click" v-model:value="config.access_password"
                    placeholder="留空则不启用密码保护" autocomplete="new-password" />
                </n-form-item>
              </n-gi>

              <!-- 第三行：打开方式选择 -->
              <n-gi span="2">
                <n-form-item label="打开方式选择" path="access_mode">
                  <template #label>
                    <div class="flex items-center gap-1.5">
                      <span>打开方式选择</span>
                      <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                        <template #trigger>
                          <n-icon size="14"
                            class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                            <Help />
                          </n-icon>
                        </template>
                        控制概览页「进入 Harness」按钮的访问链路
                      </n-tooltip>
                    </div>
                  </template>
                  <n-radio-group v-model:value="config.access_mode" size="medium" class="w-full flex">
                    <n-radio-button value="fngateway" class="!flex-1 text-center">
                      飞牛网关
                    </n-radio-button>
                    <n-radio-button value="port" class="!flex-1 text-center">
                      反代端口
                    </n-radio-button>
                    <n-radio-button value="custom" class="!flex-1 text-center">
                      自定义地址
                    </n-radio-button>
                  </n-radio-group>
                </n-form-item>
              </n-gi>

              <!-- 自定义外部地址输入框 (仅在选中自定义地址时动态渲染) -->
              <n-gi v-if="config.access_mode === 'custom'" span="2">
                <n-form-item label="自定义外部访问地址" path="reverse_proxy_url">
                  <template #label>
                    <div class="flex items-center gap-1.5">
                      <span>自定义外部访问地址</span>
                      <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                        <template #trigger>
                          <n-icon size="14"
                            class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                            <Help />
                          </n-icon>
                        </template>
                        点击概览页「进入 Harness」时跳转的绝对 URL (例如 https://dsh.nas.com:2299)
                      </n-tooltip>
                    </div>
                  </template>
                  <n-input v-model:value="config.reverse_proxy_url" placeholder="例如 https://dsh.example.com:2299"
                    clearable />
                </n-form-item>
              </n-gi>

              <!-- 飞牛官方 TRIM CLI 技能开关 -->
              <n-gi span="2">
                <div class="pt-2 border-t border-slate-100 dark:border-slate-800/80 space-y-1.5">
                  <div class="flex items-center justify-between gap-3">
                    <div class="flex items-center gap-1.5">
                      <span class="text-sm font-medium text-slate-800 dark:text-slate-100">飞牛官方 TRIM CLI 技能</span>
                      <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                        <template #trigger>
                          <n-icon size="14"
                            class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                            <Help />
                          </n-icon>
                        </template>
                        自动向 DSH 注入飞牛官方命令行工具，提供 NAS 文件、存储、相册与系统状态管理能力
                      </n-tooltip>
                    </div>
                    <n-switch v-model:value="config.enable_builtin_skill" size="medium" class="shrink-0" />
                  </div>
                  <div class="text-xs text-slate-500 dark:text-slate-400 leading-relaxed">
                    启用后自动同步内置技能到 DSH 数据目录，支持通过自然语言操控 NAS。
                  </div>
                </div>
              </n-gi>
            </n-grid>
          </n-card>

          <!-- 网络与依赖源卡片 -->
          <n-card title="网络与依赖源" :bordered="false" class="shadow-sm rounded-2xl">
            <div class="space-y-4">
              <n-form-item path="npm_registry">
                <template #label>
                  <div class="flex items-center gap-1.5">
                    <span>NPM 依赖源</span>
                    <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                      <template #trigger>
                        <n-icon size="14"
                          class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                          <Help />
                        </n-icon>
                      </template>
                      用于插件安装与项目构建时的 npm/pnpm 依赖包下载
                    </n-tooltip>
                  </div>
                </template>
                <n-select v-model:value="config.npm_registry" :options="npmRegistryOptions" />
              </n-form-item>

              <n-form-item path="network_proxy">
                <template #label>
                  <div class="flex items-center gap-1.5">
                    <span>网络代理地址 (HTTP / SOCKS5)</span>
                    <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                      <template #trigger>
                        <n-icon size="14"
                          class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                          <Help />
                        </n-icon>
                      </template>
                      用于 GitHub 源码克隆与版本检测，留空使用系统直连
                    </n-tooltip>
                  </div>
                </template>
                <n-input v-model:value="config.network_proxy"
                  placeholder="例如 http://192.168.1.100:7890 或 socks5://192.168.1.100:7890" clearable />
              </n-form-item>

              <div class="pt-3 border-t border-slate-100 dark:border-slate-800/80 space-y-1.5">
                <div class="flex items-center justify-between gap-3">
                  <div class="flex items-center gap-1.5">
                    <span class="text-sm font-medium text-slate-800 dark:text-slate-100">应用于 DSH 服务进程</span>
                    <n-tooltip :trigger="isTouch ? 'click' : 'hover'">
                      <template #trigger>
                        <n-icon size="14"
                          class="text-slate-400 dark:text-slate-500 cursor-help transition-colors active:text-fnos-blue dark:active:text-blue-400">
                          <Help />
                        </n-icon>
                      </template>
                      启用后向 Node.js 进程注入 HTTP_PROXY / HTTPS_PROXY 并设置 NODE_USE_ENV_PROXY=1
                    </n-tooltip>
                  </div>
                  <n-switch v-model:value="config.proxy_dsh_runtime" :disabled="!config.network_proxy?.trim()"
                    size="medium" class="shrink-0" />
                </div>
                <div class="text-xs text-slate-500 dark:text-slate-400 leading-relaxed">
                  启用后让 DSH 使用网络代理访问外部网络。
                </div>
              </div>
            </div>
          </n-card>

          <!-- 运行环境重置卡片 -->
          <n-card title="重置修复" :bordered="false"
            class="shadow-sm rounded-2xl border border-rose-100 dark:border-rose-950/40">
            <div class="space-y-2">
              <div class="flex items-center justify-between gap-3">
                <div class="text-sm font-medium text-slate-800 dark:text-slate-100">
                  重置为初始运行环境
                </div>
                <n-button type="error" size="small" secondary :loading="systemStore.isBuilding"
                  :disabled="systemStore.isActionLocked" @click="handleRepairEnvironment"
                  class="shrink-0 !h-8 !px-3.5 rounded-xl font-medium transition-transform duration-150 active:scale-95">
                  重置
                </n-button>
              </div>
              <div class="text-xs text-slate-500 dark:text-slate-400 leading-relaxed">
                适用于在线更新回退、插件冲突、服务崩溃或环境损坏等场景。重新部署应用内置的已适配版本，您的模型 API 密钥、历史会话记录与系统设置将完整保留。
              </div>
            </div>
          </n-card>
        </div>
      </n-form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, h, watch, onMounted, onUnmounted } from 'vue'
import { storeToRefs } from 'pinia'
import {
  NCard,
  NForm,
  NFormItem,
  NGrid,
  NGi,
  NInput,
  NInputNumber,
  NSelect,
  NRadioGroup,
  NRadioButton,
  NEmpty,
  NTooltip,
  NIcon,
  NButton,
  NSwitch,
  useMessage,
  useDialog,
  type FormInst,
  type FormRules
} from 'naive-ui'
import {
  Help,
  X
} from '@vicons/tabler'
import { useConfigStore } from '../stores/config'
import { useSystemStore } from '../stores/system'
import { trimSdk } from '../utils/trimSdk'
import { useIsTouchDevice } from '../utils/device'

const configStore = useConfigStore()
const systemStore = useSystemStore()
const message = useMessage()
const dialog = useDialog()
const isTouch = useIsTouchDevice()

const formRef = ref<FormInst | null>(null)

const {
  config,
  savedConfig,
  saving,
  loadError,
  configLoaded,
  isChanged,
  isServerPortChanged,
  isHeapMemoryChanged,
  isProxyDshChanged
} = storeToRefs(configStore)

// 堆内存上限选项
const heapMemoryOptions = [
  { label: '自动 (系统默认)', value: 0 },
  { label: '2 GB', value: 2 },
  { label: '4 GB', value: 4 },
  { label: '6 GB', value: 6 },
  { label: '8 GB', value: 8 },
  { label: '10 GB', value: 10 }
]

// NPM 依赖源选项
const npmRegistryOptions = [
  { label: '淘宝镜像源 (npmmirror.com)', value: 'https://registry.npmmirror.com' },
  { label: '腾讯云镜像源 (cloud.tencent.com)', value: 'https://mirrors.cloud.tencent.com/npm/' },
  { label: '华为云镜像源 (huaweicloud.com)', value: 'https://repo.huaweicloud.com/repository/npm/' },
  { label: 'npm 官方源 (npmjs.org)', value: 'https://registry.npmjs.org' }
]

watch(isChanged, (changed) => {
  if (changed) {
    trimSdk.setExitPageTips({
      title: '设置未保存',
      content: '当前设置有未保存的修改，离开可能丢失这些内容。'
    })
  } else {
    trimSdk.clearExitPageTips()
  }
}, { immediate: true })

onUnmounted(() => {
  trimSdk.clearExitPageTips()
})

// 表单输入校验规则
const rules: FormRules = {
  server_port: [
    { required: true, type: 'number', message: '请输入内部监听端口', trigger: ['input', 'blur'] },
    {
      validator(_rule, value: number) {
        if (!value || value < 1 || value > 65535) {
          return new Error('端口范围必须在 1 ~ 65535 之间')
        }
        if (value === config.value.proxy_port) {
          return new Error('内部监听端口不能与反向代理端口相同')
        }
        return true
      },
      trigger: ['input', 'blur']
    }
  ],
  proxy_port: [
    { required: true, type: 'number', message: '请输入反向代理端口', trigger: ['input', 'blur'] },
    {
      validator(_rule, value: number) {
        if (!value || value < 1 || value > 65535) {
          return new Error('端口范围必须在 1 ~ 65535 之间')
        }
        if (value === config.value.server_port) {
          return new Error('反向代理端口不能与内部监听端口相同')
        }
        return true
      },
      trigger: ['input', 'blur']
    }
  ],
  network_proxy: [
    {
      validator(_rule, value: string) {
        if (!value || !value.trim()) return true
        const trimmed = value.trim()
        if (!/^(http|https|socks5|socks5h):\/\//i.test(trimmed)) {
          return new Error('代理地址需以 http://、https:// 或 socks5:// 开头')
        }
        return true
      },
      trigger: ['input', 'blur']
    }
  ],
  reverse_proxy_url: [
    {
      validator(_rule, value: string) {
        if (config.value.access_mode !== 'custom') return true
        if (!value || !value.trim()) {
          return new Error('请填写自定义外部访问地址')
        }
        const trimmed = value.trim()
        if (!/^(http|https):\/\//i.test(trimmed)) {
          return new Error('外部访问地址需以 http:// 或 https:// 开头')
        }
        return true
      },
      trigger: ['input', 'blur']
    }
  ]
}

onMounted(async () => {
  if (!configLoaded.value) {
    await configStore.fetchConfig()
    if (loadError.value) {
      message.error('加载配置失败')
    }
  }
})

// 执行实际保存
async function executeSave() {
  const res = await configStore.saveConfig()
  if (res.success) {
    message.success(res.message || '设置保存成功')
  } else {
    message.error(res.message || '保存设置失败')
  }
}

// 提交保存
async function handleSave() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    message.error('请检查表单填写是否正确')
    return
  }

  // 端口、内存或 DSH 运行时代理变更时提示重启
  if ((isServerPortChanged.value || isHeapMemoryChanged.value || isProxyDshChanged.value) && systemStore.isRunning) {
    const changes: string[] = []
    if (isServerPortChanged.value) {
      changes.push(`内部监听端口已由 ${savedConfig.value?.server_port || 2298} 变更为 ${config.value.server_port}`)
    }
    if (isHeapMemoryChanged.value) {
      const oldMem = savedConfig.value?.heap_memory_limit ? `${savedConfig.value.heap_memory_limit} GB` : '自动'
      const newMem = config.value.heap_memory_limit ? `${config.value.heap_memory_limit} GB` : '自动'
      changes.push(`堆内存上限已由 ${oldMem} 变更为 ${newMem}`)
    }
    if (isProxyDshChanged.value) {
      const isNowEnabled = Boolean(config.value.network_proxy?.trim()) && Boolean(config.value.proxy_dsh_runtime)
      changes.push(`DSH 服务进程代理已${isNowEnabled ? '启用' : '关闭'}`)
    }

    dialog.warning({
      title: '确认保存并重启核心服务？',
      content: `检测到${changes.join('，')}。保存设置后，系统将自动重启 DeepSeek Harness 后端进程以应用新配置。当前所有正在执行的任务可能会短暂中断，是否确认继续？`,
      positiveText: '保存并重启',
      negativeText: '取消',
      onPositiveClick: async () => {
        await executeSave()
      }
    })
    return
  }

  await executeSave()
}

// 取消修改
function handleReset() {
  configStore.resetConfig()
  formRef.value?.restoreValidation()
  message.info('已取消修改')
}

// 重置运行环境确认弹窗
function handleRepairEnvironment() {
  const cleanPlugins = ref(true)
  dialog.warning({
    title: '确认重置运行环境？',
    content: () =>
      h('div', { class: 'space-y-3 pt-1 text-xs leading-relaxed text-slate-600 dark:text-slate-300' }, [
        h(
          'p',
          '此操作将终止当前服务并重新部署应用内置的已适配版本。您的模型 API 密钥、历史会话记录与系统设置将完整保留。是否确认继续？'
        ),
        h(
          'div',
          {
            class:
              'flex items-center justify-between gap-3 p-2.5 rounded-xl bg-slate-50 dark:bg-slate-800/60 border border-slate-200/70 dark:border-slate-700/70'
          },
          [
            h('div', { class: 'flex flex-col gap-0.5' }, [
              h('span', { class: 'font-medium text-slate-700 dark:text-slate-200' }, '清理已安装的第三方插件'),
              h(
                'span',
                { class: 'text-[11px] text-slate-400 dark:text-slate-500' },
                '建议开启以避免插件冲突并恢复纯净环境；如需保留请关闭'
              )
            ]),
            h(NSwitch, {
              value: cleanPlugins.value,
              size: 'small',
              'onUpdate:value': (val: boolean) => {
                cleanPlugins.value = val
              }
            })
          ]
        )
      ]),
    positiveText: '确认重置',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await systemStore.sendAction('repair', { keep_plugins: !cleanPlugins.value })
      if (res.success) {
        message.success(res.message || '已开始重置运行环境…')
      } else {
        message.error(res.message || '重置运行环境失败')
      }
    }
  })
}
</script>