<script setup lang="ts">
import { useI18n } from 'vue-i18n'
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

const { t } = useI18n()
const latest = computed(() => notice.latest)

const selectionLabel = computed(() => {
  const count = vault.selectionCount
  if (count === 0) return null
  const name = vault.selectedItems[0]?.name
  if (count === 1 && name) return t('status.oneSelected', { name })
  return t('status.selected', { n: formatCount(count), size: formatBytes(vault.selectedBytes) })
})

const location = computed(() => {
  if (route.meta.mode === 'recycle') return t('nav.recycle')
  if (route.meta.mode === 'unindexed') return t('nav.unindexed')
  if (route.meta.mode === 'storage') return t('nav.storage')
  if (route.meta.mode === 'preview') return vault.previewFile?.name ?? t('preview.title')
  // Root crumbs hold a symbolic name, so resolve the first segment through
  // `t()`; the deeper segments are folder names from the server.
  const segments = vault.crumbs.map((crumb, index) =>
    index === 0 || crumb.id === null ? t('nav.drive') : crumb.name,
  )
  return segments.join(' / ') || t('nav.drive')
})

const usedPercent = computed(() =>
  vault.quotaBytes > 0 ? formatPercent(vault.usedBytes, vault.quotaBytes, 0) : '—',
)
</script>

<template>
  <footer
    class="z-30 flex h-8 shrink-0 items-center gap-3 border-t border-line bg-ink px-3 text-2xs md:px-4"
    role="status"
    aria-live="polite"
  >
    <!-- The status marker is never lowercased: it is the console's one
         machine-printed verdict about what just happened. -->
    <span v-if="latest" class="flex min-w-0 items-center gap-2">
      <span :class="latest.tone === 'error' ? 'text-error' : 'text-success'">
        {{ latest.tone === 'error' ? t('status.error') : t('status.ok') }}
      </span>
      <span :key="latest.id" class="notice-in truncate text-dim">{{ latest.message }}</span>
    </span>

    <span v-else class="flex min-w-0 items-center gap-2">
      <span class="truncate text-dim">{{ location }}</span>
      <span class="text-line" aria-hidden="true">·</span>
      <span class="truncate text-faint">
        {{ selectionLabel ?? vault.showingLabel }}
      </span>
    </span>

    <span class="ml-auto flex shrink-0 items-center gap-3 text-faint">
      <span v-if="uploads.activeCount > 0" class="flex items-center gap-1.5 text-primary">
        <span class="dot bg-primary" aria-hidden="true" />
        {{ t('status.transferring', { n: uploads.activeCount, percent: uploads.overallProgress }) }}
      </span>
      <span class="tabular-nums">
        {{ t('status.usedOfQuota', { used: formatBytes(vault.usedBytes), quota: formatBytes(vault.quotaBytes) }) }}
        <span class="text-faint/70">· {{ usedPercent }}</span>
      </span>
    </span>
  </footer>
</template>