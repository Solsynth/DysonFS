<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

import { formatBytes, formatCount } from '@/lib/format'
import { useNoticeStore } from '@/stores/notice'
import { useUploadStore } from '@/stores/upload'
import { useVaultStore } from '@/stores/vault'

const vault = useVaultStore()
const uploads = useUploadStore()
const notice = useNoticeStore()
const route = useRoute()

const now = ref(new Date())
let timer: number | undefined

const clock = computed(() => `${now.value.toISOString().slice(11, 16)} UTC`)

const latest = computed(() => notice.latest)
const noticeMarker = computed(() => (latest.value?.tone === 'error' ? 'ERROR' : 'OK'))
const noticeMessage = computed(() => latest.value?.message ?? 'Ready')

const selectionLabel = computed(() => {
  const count = vault.selectedItems.length
  if (count === 0) return null
  if (count === 1) return `${vault.selectedItems[0]?.name ?? 'object'} selected`
  return `${formatCount(count)} selected · ${formatBytes(vault.selectedBytes)}`
})

const location = computed(() => {
  if (route.meta.mode === 'recycle') return 'Recycle Bin'
  if (route.meta.mode === 'unindexed') return 'Unindexed'
  if (route.path === '/storage') return 'Storage'
  const path = vault.crumbs.map((crumb) => crumb.name).join(' / ')
  return path || 'My Drive'
})

const onDrive = computed(() => Boolean(route.meta.mode))

onMounted(() => {
  timer = window.setInterval(() => {
    now.value = new Date()
  }, 30_000)
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
})
</script>

<template>
  <footer
    class="z-30 flex h-7 shrink-0 items-center gap-3 border-t border-line bg-base-100 px-3 text-2xs text-faint"
    role="status"
    aria-live="polite"
  >
    <span class="flex min-w-0 items-center gap-1.5">
      <span
        class="uppercase tracking-[0.12em]"
        :class="latest?.tone === 'error' ? 'text-error' : 'text-success'"
      >
        {{ noticeMarker }}
      </span>
      <span :key="latest?.id ?? 'idle'" class="notice-in truncate text-dim">{{
        noticeMessage
      }}</span>
    </span>

    <span class="hidden h-2.5 w-px bg-line sm:block" aria-hidden="true" />
    <span class="hidden max-w-[220px] truncate sm:block">{{ location }}</span>

    <span class="mx-auto hidden items-center gap-2 md:flex">
      <template v-if="onDrive">
        <span v-if="selectionLabel" class="text-dim">{{ selectionLabel }}</span>
        <span v-else>{{ vault.showingLabel }}</span>
      </template>
      <template v-else>
        <span class="uppercase tracking-[0.1em]">capacity</span>
        <span class="text-faint">/</span>
        <span>
          {{
            vault.quotaBytes > 0
              ? `${Math.round(vault.usedFraction * 100)}% of quota`
              : 'no quota reading'
          }}
        </span>
      </template>
    </span>

    <span class="ml-auto flex items-center gap-3">
      <span v-if="uploads.activeCount > 0" class="flex items-center gap-1 text-primary">
        <span class="dot bg-primary" aria-hidden="true" />
        {{ uploads.activeCount }} transfer{{ uploads.activeCount === 1 ? '' : 's' }} ·
        {{ uploads.overallProgress }}%
      </span>
      <span class="font-mono tabular-nums text-dim">{{ clock }}</span>
    </span>
  </footer>
</template>
