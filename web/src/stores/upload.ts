import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { uploadDirect } from '@/lib/api/drive'
import { isSampleData, sampleUpload } from '@/lib/demo'
import { formatCount } from '@/lib/format'

import { useNoticeStore } from './notice'
import { useVaultStore } from './vault'

export type UploadStatus = 'queued' | 'uploading' | 'done' | 'error' | 'cancelled'

export interface UploadTask {
  id: string
  name: string
  size: number
  progress: number
  status: UploadStatus
  error: string
  startedAt: number
}

const CONCURRENCY = 2
let nextId = 1

export const useUploadStore = defineStore('upload', () => {
  const vault = useVaultStore()
  const notice = useNoticeStore()

  const tasks = ref<UploadTask[]>([])
  const collapsed = ref(false)
  const controllers = new Map<string, AbortController>()
  const files = new Map<string, File>()
  const parents = new Map<string, string | null>()
  const queue: string[] = []
  let running = 0
  let sampleTimer: number | undefined

  const active = computed(() =>
    tasks.value.filter((task) => task.status === 'queued' || task.status === 'uploading'),
  )
  const activeCount = computed(() => active.value.length)
  const finished = computed(() =>
    tasks.value.filter((task) => task.status === 'done' || task.status === 'error'),
  )
  const overallProgress = computed(() => {
    const list = active.value
    if (list.length === 0) return 0
    return Math.round(list.reduce((sum, task) => sum + task.progress, 0) / list.length)
  })

  function update(id: string, patch: Partial<UploadTask>) {
    const index = tasks.value.findIndex((task) => task.id === id)
    if (index < 0) return
    tasks.value[index] = { ...tasks.value[index]!, ...patch }
  }

  function enqueue(list: File[], parentId: string | null) {
    if (isSampleData()) {
      startSample(list, parentId)
      return
    }
    const accepted = list.filter((file) => file.size >= 0)
    if (accepted.length === 0) return

    for (const file of accepted) {
      const id = `up-${nextId++}`
      files.set(id, file)
      tasks.value = [
        ...tasks.value,
        {
          id,
          name: file.name,
          size: file.size,
          progress: 0,
          status: 'queued',
          error: '',
          startedAt: Date.now(),
        },
      ]
      queue.push(id)
      parents.set(id, parentId)
    }
    collapsed.value = false
    notice.info(
      accepted.length === 1
        ? `${accepted[0]!.name} queued to upload.`
        : `${formatCount(accepted.length)} files queued to upload.`,
    )
    pump()
  }

  function pump() {
    while (running < CONCURRENCY && queue.length > 0) {
      const id = queue.shift()
      if (!id) return
      const file = files.get(id)
      if (!file) continue
      running += 1
      void send(id, file, parents.get(id) ?? null).finally(() => {
        running -= 1
        pump()
      })
    }
  }

  async function send(id: string, file: File, parentId: string | null) {
    const controller = new AbortController()
    controllers.set(id, controller)
    update(id, { status: 'uploading', progress: 0 })
    try {
      await uploadDirect(file, {
        parentId,
        signal: controller.signal,
        onProgress: (percent) => update(id, { progress: percent }),
      })
      update(id, { status: 'done', progress: 100 })
      notice.ok(`${file.name} uploaded.`)
      await Promise.all([vault.load(), vault.loadVolume()])
    } catch (caught) {
      if (controller.signal.aborted) {
        update(id, { status: 'cancelled' })
        return
      }
      const message = caught instanceof Error ? caught.message : 'The transfer failed.'
      update(id, { status: 'error', error: message })
      notice.error(`${file.name}: ${message}`)
    } finally {
      controllers.delete(id)
      files.delete(id)
      parents.delete(id)
    }
  }

  function startSample(list: File[], parentId: string | null) {
    if (sampleTimer) window.clearTimeout(sampleTimer)
    const created = list.map<UploadTask>((file) => ({
      id: `sample-${nextId++}`,
      name: file.name,
      size: file.size,
      progress: 0,
      status: 'uploading',
      error: '',
      startedAt: Date.now(),
    }))
    tasks.value = [...tasks.value, ...created]
    collapsed.value = false

    let tick = 0
    const advance = () => {
      tick += 1
      const step = Math.min(100, tick * 9)
      for (const task of created) {
        update(task.id, {
          progress: step,
          status: step >= 100 ? 'done' : 'uploading',
        })
      }
      if (step < 100) {
        sampleTimer = window.setTimeout(advance, 180)
        return
      }
      created.forEach((_, index) => {
        const file = list[index]
        if (file) sampleUpload(file, parentId)
      })
      notice.info('Sample upload complete. Nothing was saved.')
      void vault.load()
    }
    sampleTimer = window.setTimeout(advance, 180)
  }

  function cancel(id: string) {
    controllers.get(id)?.abort()
    const task = tasks.value.find((entry) => entry.id === id)
    if (task?.status === 'queued') {
      const index = queue.indexOf(id)
      if (index >= 0) queue.splice(index, 1)
      update(id, { status: 'cancelled' })
      files.delete(id)
      parents.delete(id)
    }
  }

  function dismiss(id: string) {
    cancel(id)
    tasks.value = tasks.value.filter((task) => task.id !== id)
  }

  function clearFinished() {
    tasks.value = tasks.value.filter(
      (task) => task.status !== 'done' && task.status !== 'cancelled' && task.status !== 'error',
    )
  }

  function reset() {
    for (const controller of controllers.values()) controller.abort()
    controllers.clear()
    files.clear()
    parents.clear()
    queue.length = 0
    tasks.value = []
  }

  return {
    tasks,
    collapsed,
    active,
    activeCount,
    finished,
    overallProgress,
    enqueue,
    cancel,
    dismiss,
    clearFinished,
    reset,
  }
})
