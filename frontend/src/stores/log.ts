import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { logApi } from '../api'
import type { RequestResult } from '../types/api'

export const useLogStore = defineStore('log', () => {
  const logLines = ref<string[]>([])
  const logAutoScroll = ref(true)
  const fetching = ref(false)
  const hasLoadedSnapshot = ref(false)

  const MAX_LOG_LINES = 300
  const FLUSH_INTERVAL = 80

  let pendingBuffer = ''
  let flushTimer: ReturnType<typeof setTimeout> | null = null
  const flushListeners = new Set<() => void>()

  function splitLines(text: string): string[] {
    if (!text) return []
    const raw = text.split('\n')
    const lines: string[] = []
    for (let i = 0; i < raw.length - 1; i++) {
      lines.push(raw[i] + '\n')
    }
    if (raw[raw.length - 1]) {
      lines.push(raw[raw.length - 1])
    }
    return lines
  }

  function trimLogs() {
    if (logLines.value.length > MAX_LOG_LINES) {
      logLines.value = logLines.value.slice(-MAX_LOG_LINES)
    }
  }

  function flushPending() {
    flushTimer = null
    if (!pendingBuffer) return
    const incomingLines = splitLines(pendingBuffer)
    pendingBuffer = ''
    if (incomingLines.length > 0) {
      logLines.value.push(...incomingLines)
      trimLogs()
      flushListeners.forEach((fn) => fn())
    }
  }

  function appendChunk(chunk: string) {
    pendingBuffer += chunk
    if (flushTimer === null) {
      flushTimer = setTimeout(flushPending, FLUSH_INTERVAL)
    }
  }

  function onFlush(fn: () => void): () => void {
    flushListeners.add(fn)
    return () => {
      flushListeners.delete(fn)
    }
  }

  function setLogs(lines: string[]) {
    const flattened: string[] = []
    for (const l of lines) {
      flattened.push(...splitLines(l))
    }
    logLines.value = flattened.slice(-MAX_LOG_LINES)
    flushListeners.forEach((fn) => fn())
  }

  const displayedText = computed(() => logLines.value.join(''))

  async function fetchLogs(): Promise<void> {
    fetching.value = true
    try {
      const res = await logApi.getLogs()
      if (res.success && res.data) {
        hasLoadedSnapshot.value = true
        if (Array.isArray(res.data.lines) && res.data.lines.length > 0) {
          setLogs(res.data.lines)
        } else if (typeof res.data.content === 'string' && res.data.content) {
          setLogs([res.data.content])
        } else {
          setLogs([])
        }
      }
    } finally {
      fetching.value = false
    }
  }

  async function clearLogs(): Promise<RequestResult<boolean>> {
    const res = await logApi.clearLogs()
    if (res.success) {
      logLines.value = []
      pendingBuffer = ''
      if (flushTimer !== null) {
        clearTimeout(flushTimer)
        flushTimer = null
      }
    }
    return res
  }

  function downloadLogs(): void {
    window.open(logApi.getDownloadUrl(), '_blank')
  }

  return {
    logLines,
    logAutoScroll,
    fetching,
    hasLoadedSnapshot,
    displayedText,
    appendChunk,
    setLogs,
    fetchLogs,
    clearLogs,
    downloadLogs,
    onFlush
  }
})
