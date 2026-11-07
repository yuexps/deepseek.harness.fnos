import { TrimApp } from '@trimjs/web-app'

let sdkInstance: TrimApp | null = null

function getSdk(): TrimApp {
  if (!sdkInstance) {
    sdkInstance = new TrimApp()
  }
  return sdkInstance
}

/**
 * 飞牛 SDK 统一服务封装
 */
export const trimSdk = {
  get sdk(): TrimApp {
    return getSdk()
  },

  get isStandaloneWeb(): boolean {
    return Boolean(this.sdk.isStandaloneWeb)
  },

  get isWeb(): boolean {
    return Boolean(this.sdk.isWeb)
  },

  async ready(): Promise<void> {
    try {
      await this.sdk.ready()
    } catch {
      // 独立浏览器或非微应用环境静默忽略
    }
  },

  async openFileManager(path: string): Promise<{ success: boolean; message?: string }> {
    await this.ready()
    if (this.isStandaloneWeb) {
      return { success: false, message: '请在飞牛系统桌面内使用文件管理定位' }
    }
    try {
      await this.sdk.openFileManager(path)
      return { success: true }
    } catch (e: any) {
      return { success: false, message: e?.message || String(e) }
    }
  },

  async openFile(path: string): Promise<{ success: boolean; message?: string }> {
    await this.ready()
    if (this.isStandaloneWeb) {
      return { success: false, message: '请在飞牛系统Web桌面内打开' }
    }
    try {
      await this.sdk.openFile(path)
      return { success: true }
    } catch (e: any) {
      return { success: false, message: e?.message || String(e) }
    }
  },

  async openURL(url: string, target = '_blank'): Promise<void> {
    await this.ready()
    try {
      await this.sdk.openURL(url, target)
    } catch {
      window.open(url, target)
    }
  },

  async setTitle(title: string): Promise<void> {
    await this.ready()
    try {
      await this.sdk.setTitle(title)
    } catch {
      document.title = title
    }
  },

  async setExitPageTips(params?: { title?: string; content?: string }): Promise<void> {
    await this.ready()
    try {
      await this.sdk.setExitPageTips(params)
    } catch {
      // 宿主不支持时静默忽略
    }
  },

  async clearExitPageTips(): Promise<void> {
    await this.ready()
    try {
      await this.sdk.setExitPageTips()
    } catch {
      // 宿主不支持时静默忽略
    }
  },

  async initPlatformTheme(onThemeChange: (theme: 'dark' | 'light') => void): Promise<void> {
    await this.ready()
    try {
      const config = await this.sdk.getPlatformConfig()
      if (config?.theme) {
        onThemeChange(config.theme)
      }
      if (this.isWeb && !this.isStandaloneWeb) {
        await this.sdk.$on('os/theme', (theme: 'dark' | 'light') => {
          onThemeChange(theme)
        })
      }
    } catch {
      // 非宿主环境忽略
    }
  }
}
