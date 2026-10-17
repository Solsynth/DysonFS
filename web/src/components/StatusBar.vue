<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import { formatBytes, formatCount, formatPercent } from '@/lib/format'
import { useNoticeStore } from '@/stores/notice'
import { useUploadStore } from '@/stores/upload'
import { useVaultStore } from '@/stores/vault'

const vault = useVaultStore()
const uploads = useUploadStore()
const notice = useNoticeStore()
const route = useRoute()

const latest = computed(() => notice.latest)
const noticeMessage = computed(() => latest.value?.message ?? '')
const noticeTone = computed(() => (latest.value?.tone === 'error' ? 'bg-error' : 'bg-success'))

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

const usedPercent = computed(() =>
  vault.quotaBytes > 0 ? formatPercent(vault.usedBytes, vault.quotaBytes, 0) : '—',
)
</script>

<template>
  <footer
    class="z-30 flex h-8 shrink-0 items-center gap-3 border-t border-line bg-ink px-3 text-[11px] text-faint md:px-4"
    role="status"
    aria-live="polite"
  >
    <span v-if="latest" class="flex min-w-0 items-center gap-2">
      <span class="dot" :class="noticeTone" aria-hidden="true" />
      <span :key="latest.id" class="notice-in truncate text-dim">{{ noticeMessage }}</span>
    </span>

    <span v-else class="flex min-w-0 items-center gap-2">
      <span class="truncate text-dim">{{ location }}</span>
      <span aria-hidden="true">·</span>
      <span class="truncate">
        {{ selectionLabel ?? vault.showingLabel }}
      </span>
    </span>

    <span class="ml-auto flex shrink-0 items-center gap-3">
      <span v-if="uploads.activeCount > 0" class="flex items-center gap-1.5 text-primary">
        <span class="dot bg-primary" aria-hidden="true" />
        {{ uploads.activeCount }} transferring · {{ uploads.overallProgress }}%
      </span>
      <span class="tabular-nums">
        {{ formatBytes(vault.usedBytes) }} of {{ formatBytes(vault.quotaBytes) }}
        <span class="text-faint/70">· {{ usedPercent }}</span>
      </span>
    </span>
  </footer>
</template>
