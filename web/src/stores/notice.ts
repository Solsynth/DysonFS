import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

export type NoticeTone = 'ok' | 'error' | 'info'

export interface Notice {
  id: number
  tone: NoticeTone
  message: string
  at: number
}

let nextId = 1
const timers = new Map<number, number>()

export const useNoticeStore = defineStore('notice', () => {
  const items = ref<Notice[]>([])

  const latest = computed(() => items.value[items.value.length - 1] ?? null)
  const hasError = computed(() => items.value.some((item) => item.tone === 'error'))

  function push(tone: NoticeTone, message: string, ttl = 5200) {
    const notice: Notice = { id: nextId++, tone, message, at: Date.now() }
    items.value = [...items.value.slice(-3), notice]
    const timer = window.setTimeout(() => dismiss(notice.id), ttl)
    timers.set(notice.id, timer)
    return notice.id
  }

  function dismiss(id: number) {
    const timer = timers.get(id)
    if (timer) window.clearTimeout(timer)
    timers.delete(id)
    items.value = items.value.filter((item) => item.id !== id)
  }

  function ok(message: string) {
    return push('ok', message)
  }
  function error(message: string) {
    return push('error', message, 8000)
  }
  function info(message: string) {
    return push('info', message)
  }

  return { items, latest, hasError, push, dismiss, ok, error, info }
})
