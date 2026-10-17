<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

import { formatBytes, formatCount } from '@/lib/format'
import { useUploadStore } from '@/stores/upload'
import { useVaultStore } from '@/stores/vault'

const vault = useVaultStore()
const uploads = useUploadStore()
const route = useRoute()

const now = ref(new Date())
let timer: number | undefined

const clock = computed(() => `${now.value.toISOString().slice(11, 16)} UTC`)

const selectionLabel = computed(() => {
  const count = vault.selectedItems.length
  if (count === 0) return null
  if (count === 1) return `${vault.selectedItems[0]?.name ?? 'object'} selected`
  return `${formatCount(count)} selected · ${formatBytes(vault.selectedBytes)}`
})

const location = computed(() => {
  if (route.meta.mode === 'recycle') return 'recycle://local'
  if (route.meta.mode === 'unindexed') return 'unindexed://local'
  if (route.path === '/storage') return 'volume://a'
  const path = vault.crumbs.map((crumb) => crumb.name).join('/')
  return `drive://${path || 'root'}`
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
    class="z-30 flex h-7 shrink-0 items-center gap-3 border-t border-line bg-ink px-3 text-3xs text-faint"
  >
    <span class="flex items-center gap-1.5">
      <span class="dot bg-success" aria-hidden="true" />
      <span class="uppercase tracking-[0.12em] text-dim">link stable</span>
    </span>
    <span class="hidden h-2.5 w-px bg-line sm:block" aria-hidden="true" />
    <span class="hidden truncate font-mono tracking-[0.04em] sm:block">{{ location }}</span>

    <span class="mx-auto hidden items-center gap-2 md:flex">
      <template v-if="onDrive">
        <span v-if="selectionLabel" class="text-dim">{{ selectionLabel }}</span>
        <template v-else>
          <span>{{ vault.showingLabel }}</span>
          <span v-if="vault.query" class="text-faint">/ query “{{ vault.query }}”</span>
        </template>
      </template>
      <template v-else>
        <span class="uppercase tracking-[0.1em]">capacity telemetry</span>
        <span class="text-faint">/</span>
        <span>{{ vault.quotaBytes > 0 ? `${Math.round(vault.usedFraction * 100)}% of quota` : 'no quota reading' }}</span>
      </template>
    </span>

    <span class="ml-auto flex items-center gap-3">
      <span v-if="uploads.activeCount > 0" class="flex items-center gap-1 text-primary">
        <span class="dot bg-primary pulse-soft" aria-hidden="true" />
        {{ uploads.activeCount }} transfer{{ uploads.activeCount === 1 ? '' : 's' }}
        · {{ uploads.overallProgress }}%
      </span>
      <span class="hidden text-3xs uppercase tracking-[0.1em] text-faint lg:block">
        {{ vault.sample ? 'sample telemetry' : 'dyson master' }}
      </span>
      <span class="font-mono tabular-nums text-dim">{{ clock }}</span>
    </span>
  </footer>
</template>
