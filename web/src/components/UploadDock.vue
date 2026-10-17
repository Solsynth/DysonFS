<script setup lang="ts">
import { computed } from 'vue'
import { Checkmark16, ChevronDown16, ChevronUp16, Close16, Error16, Upload16 } from '@carbon/icons-vue'

import { formatBytes } from '@/lib/format'
import { useUploadStore } from '@/stores/upload'

const uploads = useUploadStore()

const visible = computed(() => uploads.tasks.length > 0)
const summary = computed(() => {
  if (uploads.activeCount > 0) {
    return `${uploads.activeCount} in flight · ${uploads.overallProgress}%`
  }
  const failed = uploads.tasks.filter((task) => task.status === 'error').length
  if (failed > 0) return `${failed} failed`
  return 'all complete'
})

function toggle() {
  uploads.collapsed = !uploads.collapsed
}

function statusLabel(status: string, progress: number): string {
  if (status === 'queued') return 'queued'
  if (status === 'uploading') return `${progress}%`
  if (status === 'done') return 'landed'
  if (status === 'cancelled') return 'cancelled'
  return 'failed'
}
</script>

<template>
  <Transition
    enter-active-class="transition duration-200 ease-out"
    enter-from-class="translate-y-2 opacity-0"
    leave-active-class="transition duration-150 ease-in"
    leave-to-class="translate-y-2 opacity-0"
  >
    <section
      v-if="visible"
      class="fixed bottom-10 right-3 z-50 w-[340px] max-w-[calc(100vw-24px)] border border-line bg-ink shadow-[0_16px_48px_rgba(0,0,0,0.45)]"
      aria-label="Transfers"
    >
      <button
        type="button"
        class="flex w-full items-center gap-2 border-b border-line px-3 py-2 text-left"
        :aria-expanded="!uploads.collapsed"
        @click="toggle"
      >
        <Upload16 class="h-3.5 w-3.5 text-faint" />
        <span class="eyebrow">transfers</span>
        <span class="text-3xs text-faint">{{ summary }}</span>
        <span class="ml-auto text-3xs tabular-nums text-dim">
          {{ uploads.finished.length + uploads.activeCount }}
        </span>
        <ChevronUp16 v-if="uploads.collapsed" class="h-3.5 w-3.5 text-faint" />
        <ChevronDown16 v-else class="h-3.5 w-3.5 text-faint" />
      </button>

      <div v-if="uploads.collapsed" class="h-[2px] bg-line-soft">
        <div
          class="h-full bg-primary transition-[width] duration-300"
          :style="{ width: `${uploads.overallProgress}%` }"
        />
      </div>

      <template v-else>
        <ul class="max-h-[260px] divide-y divide-line-soft overflow-y-auto">
          <li v-for="task in uploads.tasks" :key="task.id" class="px-3 py-2">
            <div class="flex items-center gap-2">
              <Checkmark16 v-if="task.status === 'done'" class="h-3.5 w-3.5 shrink-0 text-success" />
              <Error16 v-else-if="task.status === 'error'" class="h-3.5 w-3.5 shrink-0 text-error" />
              <span
                v-else
                class="dot shrink-0"
                :class="task.status === 'cancelled' ? 'bg-faint' : 'bg-primary pulse-soft'"
                aria-hidden="true"
              />
              <span class="min-w-0 flex-1 truncate text-2xs text-base-content" :title="task.name">
                {{ task.name }}
              </span>
              <span class="shrink-0 text-3xs tabular-nums text-faint">
                {{ statusLabel(task.status, task.progress) }}
              </span>
              <button
                v-if="task.status === 'uploading' || task.status === 'queued'"
                type="button"
                class="shrink-0 text-faint transition-colors hover:text-error"
                :aria-label="`Cancel ${task.name}`"
                @click="uploads.cancel(task.id)"
              >
                <Close16 class="h-3.5 w-3.5" />
              </button>
              <button
                v-else
                type="button"
                class="shrink-0 text-faint transition-colors hover:text-base-content"
                :aria-label="`Dismiss ${task.name}`"
                @click="uploads.dismiss(task.id)"
              >
                <Close16 class="h-3.5 w-3.5" />
              </button>
            </div>

            <div class="mt-1.5 flex items-center gap-2">
              <div class="h-[3px] flex-1 bg-line-soft">
                <div
                  class="h-full transition-[width] duration-200"
                  :class="
                    task.status === 'error'
                      ? 'bg-error'
                      : task.status === 'done'
                        ? 'bg-success'
                        : 'bg-primary'
                  "
                  :style="{ width: `${task.status === 'done' ? 100 : task.progress}%` }"
                />
              </div>
              <span class="text-3xs tabular-nums text-faint">{{ formatBytes(task.size) }}</span>
            </div>
            <p v-if="task.error" class="mt-1 truncate text-3xs text-error">{{ task.error }}</p>
          </li>
        </ul>

        <div class="flex items-center justify-between border-t border-line px-3 py-1.5">
          <span class="text-3xs text-faint">direct upload / proxied</span>
          <button
            type="button"
            class="text-3xs text-faint transition-colors hover:text-base-content"
            @click="uploads.clearFinished()"
          >
            Clear finished
          </button>
        </div>
      </template>
    </section>
  </Transition>
</template>
