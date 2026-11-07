import { defineConfig, type UserConfig, type ConfigEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import { viteDevMock } from './src/mock/viteDevMock'

const chunkGroups: Array<[string, string[]]> = [
  ['vendor-vue', ['/node_modules/vue/', '/node_modules/@vue/', '/node_modules/pinia/']],
  ['vendor-naive', ['/node_modules/naive-ui/', '/node_modules/@vicons/']],
  ['vendor-highlight', ['/node_modules/highlight.js/']]
]

// 按依赖分组拆分第三方包
function manualChunks(id: string): string | undefined {
  return chunkGroups.find(([, patterns]) => patterns.some((pattern) => id.includes(pattern)))?.[0]
}

export default defineConfig(({ command }: ConfigEnv): UserConfig => {
  return {
    plugins: [
      vue(),
      ...(command === 'serve' ? [viteDevMock()] : [])
    ],
    base: './',
    build: {
      rollupOptions: {
        output: {
          manualChunks
        }
      }
    },
    server: {
      port: 3000
    }
  }
})
