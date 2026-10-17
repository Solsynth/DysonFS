<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  Checkmark16,
  ChevronDown16,
  ChevronUp16,
  Close16,
  Error16,
  Upload16,
} from '@carbon/icons-vue'

import IconButton from './IconButton.vue'
import { formatBytes, formatCount } from '@/lib/format'
import { useUploadStore } from '@/stores/upload'

const uploads = useUploadStore()

const { t } = useI18n()

const visible = computed(() => uploads.tasks.length > 0)
const summary = computed(() => {
  if (uploads.activeCount > 0) {
    return t('transfers.inFlight', {
      n: formatCount(uploads.activeCount),
      percent: uploads.overallProgress,
    })
  }
  const failed = uploads.tasks.filter((task) => task.status === 'error').length
  if (failed > 0) return t('transfers.failed', { n: formatCount(failed) })
  return t('transfers.allComplete')
})

function toggle() {
  uploads.collapsed = !uploads.collapsed
}

function statusLabel(status: string, progress: number): string {
  if (status === 'queued') return t('transfers.queued')
  if (status === 'uploading') return `${progress}%`
  if (status === 'done') return t('transfers.done')
  if (status === 'cancelled') return t('transfers.cancelled')
  return t('transfers.failedOne')
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
      class="fixed bottom-11 right-3 z-50 w-[340px] max-w-[calc(100vw-24px)] rounded-xl bg-base-200 shadow-2xl"
      :aria-label="t('transfers.aria')"
    >
      <button
        type="button"
        class="flex w-full items-center gap-2 px-3 py-2.5 text-left"
        :aria-expanded="!uploads.collapsed"
        @click="toggle"
      >
        <Upload16 class="h-3.5 w-3.5 text-faint" />
        <span class="text-[11px] font-medium text-faint">{{ t('transfers.title') }}</span>
        <span class="text-[11px] text-faint">{{ summary }}</span>
        <span class="ml-auto text-[11px] tabular-nums text-dim">
          {{ uploads.finished.length + uploads.activeCount }}
        </span>
        <ChevronUp16 v-if="uploads.collapsed" class="h-3.5 w-3.5 text-faint" />
        <ChevronDown16 v-else class="h-3.5 w-3.5 text-faint" />
      </button>

      <div v-if="uploads.collapsed" class="px-3 pb-2.5">
        <div class="h-[2px] overflow-hidden rounded-full bg-base-300">
          <div
            class="h-full rounded-full bg-primary transition-[width] duration-300"
            :style="{ width: `${uploads.overallProgress}%` }"
          />
        </div>
      </div>

      <template v-else>
        <ul class="max-h-[260px] divide-y divide-line-soft overflow-y-auto border-t border-line">
          <li v-for="task in uploads.tasks" :key="task.id" class="px-3 py-2">
            <div class="flex items-center gap-2">
              <Checkmark16
                v-if="task.status === 'done'"
                class="h-3.5 w-3.5 shrink-0 text-success"
              />
              <Error16
                v-else-if="task.status === 'error'"
                class="h-3.5 w-3.5 shrink-0 text-error"
              />
              <span
                v-else
                class="dot shrink-0"
                :class="task.status === 'cancelled' ? 'bg-faint' : 'bg-primary pulse-soft'"
                aria-hidden="true"
              />
              <span class="min-w-0 flex-1 truncate text-[12px] text-base-content" :title="task.name">
                {{ task.name }}
              </span>
              <span class="shrink-0 text-[11px] tabular-nums text-faint">
                {{ statusLabel(task.status, task.progress) }}
              </span>
              <IconButton
                v-if="task.status === 'uploading' || task.status === 'queued'"
                tone="danger"
                :label="t('transfers.cancel', { name: task.name })"
                @click="uploads.cancel(task.id)"
              >
                <Close16 class="h-3.5 w-3.5" />
              </IconButton>
              <IconButton v-else :label="t('transfers.dismiss', { name: task.name })" @click="uploads.dismiss(task.id)">
                <Close16 class="h-3.5 w-3.5" />
              </IconButton>
            </div>

            <div class="mt-1.5 flex items-center gap-2">
              <div class="h-[2px] flex-1 overflow-hidden rounded-full bg-base-300">
                <div
                  class="h-full rounded-full transition-[width] duration-200"
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
              <span class="text-[11px] tabular-nums text-faint">{{ formatBytes(task.size) }}</span>
            </div>
            <p v-if="task.error" class="mt-1 truncate text-[11px] text-error">{{ task.error }}</p>
          </li>
        </ul>

        <div class="flex items-center justify-between border-t border-line px-3 py-2">
          <span class="text-[11px] text-faint">{{ t('transfers.target') }}</span>
          <button
            type="button"
            class="text-[11px] text-faint transition-colors hover:text-base-content"
            @click="uploads.clearFinished()"
          >
            {{ t('transfers.clearFinished') }}
          </button>
        </div>
      </template>
    </section>
  </Transition>
</template>
