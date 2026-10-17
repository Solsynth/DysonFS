<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { formatBytes } from '@/lib/format'
import { useVaultStore } from '@/stores/vault'

const props = withDefaults(
  defineProps<{
    size?: 'sm' | 'md' | 'lg'
    legend?: boolean
    caption?: string
  }>(),
  { size: 'md', legend: true, caption: 'volume a' },
)

const vault = useVaultStore()

const ready = ref(false)
onMounted(() => {
  requestAnimationFrame(() => {
    ready.value = true
  })
})

const diameter = computed(() => (props.size === 'lg' ? 224 : props.size === 'md' ? 164 : 124))
const hasQuota = computed(() => vault.quotaBytes > 0)
const percent = computed(() => (hasQuota.value ? vault.usedFraction * 100 : 0))
const displayPercent = computed(() => {
  if (!hasQuota.value) return '—'
  return percent.value < 10 ? percent.value.toFixed(1) : Math.round(percent.value).toString()
})
const percentClass = computed(() => {
  if (props.size === 'lg') return 'text-[40px] leading-none'
  if (props.size === 'md') return 'text-[30px] leading-none'
  return 'text-[22px] leading-none'
})

const RADIUS = 52
const CIRCUMFERENCE = 2 * Math.PI * RADIUS
const filled = computed(() => (ready.value ? Math.min(1, Math.max(0, vault.usedFraction)) : 0))
const arcLength = computed(() => `${filled.value * CIRCUMFERENCE} ${CIRCUMFERENCE}`)
const axis = computed(() => vault.usedFraction > 0.86)
</script>

<template>
  <div class="flex flex-col items-center gap-4">
    <div class="relative" :style="{ width: `${diameter}px`, height: `${diameter}px` }">
      <svg
        viewBox="0 0 120 120"
        class="h-full w-full -rotate-90"
        role="img"
        :aria-label="`${caption}: ${displayPercent}% of quota used, ${formatBytes(vault.usedBytes)} of ${formatBytes(vault.quotaBytes)}`"
      >
        <circle
          cx="60"
          cy="60"
          :r="RADIUS"
          fill="none"
          stroke="var(--color-line-soft)"
          stroke-width="4"
        />
        <circle
          v-if="hasQuota"
          cx="60"
          cy="60"
          :r="RADIUS"
          fill="none"
          :stroke="axis ? 'var(--color-error)' : 'var(--color-primary)'"
          stroke-width="4"
          stroke-linecap="round"
          :stroke-dasharray="arcLength"
          class="platter-arc"
        />
      </svg>

      <div class="absolute inset-0 flex flex-col items-center justify-center text-center">
        <span class="text-[11px] text-faint">{{ hasQuota ? caption : 'No quota' }}</span>
        <div
          class="title mt-1.5 tabular-nums"
          :class="[percentClass, axis ? 'text-error' : 'text-base-content']"
        >
          {{ displayPercent
          }}<span v-if="hasQuota" class="text-[0.46em] font-normal text-dim">%</span>
        </div>
        <span v-if="size !== 'sm'" class="mt-2 text-[11px] tabular-nums text-faint">
          {{ formatBytes(vault.usedBytes) }} of {{ formatBytes(vault.quotaBytes) }}
        </span>
        <span v-else class="mt-1 text-[11px] tabular-nums text-faint">
          {{ formatBytes(vault.usedBytes) }} used
        </span>
      </div>
    </div>

    <div v-if="legend" class="grid w-full grid-cols-2 gap-4">
      <div>
        <div class="text-[11px] text-faint">Used</div>
        <div class="mt-0.5 text-[12px] tabular-nums text-base-content">
          {{ formatBytes(vault.usedBytes) }}
        </div>
      </div>
      <div class="text-right">
        <div class="text-[11px] text-faint">Free</div>
        <div class="mt-0.5 text-[12px] tabular-nums text-dim">
          {{ formatBytes(vault.remainingBytes) }}
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.platter-arc {
  transition: stroke-dasharray 900ms cubic-bezier(0.22, 1, 0.36, 1);
}
</style>
