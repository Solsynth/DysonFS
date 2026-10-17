<script setup lang="ts">
import { computed } from 'vue'

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

const TICKS = 84
const tickList = Array.from({ length: TICKS }, (_, index) => index)

const diameter = computed(() => (props.size === 'lg' ? 296 : props.size === 'md' ? 210 : 148))
const percent = computed(() => vault.usedFraction * 100)
const hasQuota = computed(() => vault.quotaBytes > 0)
const displayPercent = computed(() => {
  if (!hasQuota.value) return '—'
  return percent.value < 10 ? percent.value.toFixed(1) : Math.round(percent.value).toString()
})
const activeTicks = computed(() => (hasQuota.value ? Math.round(vault.usedFraction * TICKS) : 0))
const circumference = 2 * Math.PI * 76
const arcLength = computed(
  () => `${Math.max(0, vault.usedFraction) * circumference} ${circumference}`,
)
const axis = computed(() => vault.usedFraction > 0.86)

const percentClass = computed(() => {
  if (props.size === 'lg') return 'text-[52px] leading-none'
  if (props.size === 'md') return 'text-[38px] leading-none'
  return 'text-[27px] leading-none'
})
</script>

<template>
  <div class="flex flex-col items-center gap-3">
    <div class="relative" :style="{ width: `${diameter}px`, height: `${diameter}px` }">
      <svg
        :viewBox="'0 0 200 200'"
        class="h-full w-full"
        role="img"
        :aria-label="`${caption}: ${displayPercent}% of quota used, ${formatBytes(vault.usedBytes)} of ${formatBytes(vault.quotaBytes)}`"
      >
        <defs>
          <radialGradient id="platter-core" cx="50%" cy="42%" r="70%">
            <stop offset="0%" stop-color="var(--color-base-300)" stop-opacity="0.9" />
            <stop offset="100%" stop-color="var(--color-base-100)" stop-opacity="0.2" />
          </radialGradient>
        </defs>

        <circle
          cx="100"
          cy="100"
          r="89"
          fill="url(#platter-core)"
          stroke="var(--color-line-soft)"
        />

        <line
          v-for="tick in tickList"
          :key="tick"
          x1="100"
          :y1="tick % 7 === 0 ? 4.6 : 5.8"
          x2="100"
          :y2="tick % 7 === 0 ? 12.4 : 9.6"
          :stroke="tick < activeTicks ? 'var(--color-brass)' : 'var(--color-line)'"
          :stroke-width="tick % 7 === 0 ? 1.15 : 0.85"
          :opacity="tick < activeTicks ? 0.95 : 0.7"
          :transform="`rotate(${(tick * 360) / TICKS} 100 100)`"
        />

        <path
          d="M100 14.5v7M100 178.5v7M14.5 100h7M178.5 100h7"
          stroke="var(--color-line)"
          stroke-width="1"
        />

        <circle cx="100" cy="100" r="84" fill="none" stroke="var(--color-line-soft)" />
        <circle
          cx="100"
          cy="100"
          r="76"
          fill="none"
          stroke="var(--color-line)"
          stroke-width="3.5"
          opacity="0.75"
        />
        <circle
          cx="100"
          cy="100"
          r="76"
          fill="none"
          :stroke="axis ? 'var(--color-error)' : 'var(--color-brass)'"
          stroke-width="3.5"
          stroke-linecap="butt"
          :stroke-dasharray="arcLength"
          transform="rotate(-90 100 100)"
          class="platter-arc"
        />

        <circle
          cx="100"
          cy="100"
          r="58"
          fill="none"
          stroke="var(--color-line-soft)"
          stroke-dasharray="1 5"
          opacity="0.8"
        />
        <circle
          cx="100"
          cy="100"
          r="38"
          fill="none"
          stroke="var(--color-line-soft)"
          opacity="0.65"
        />

        <path
          d="M100 26v5M100 169v5M26 100h5M169 100h5"
          stroke="var(--color-faint)"
          stroke-width="1"
          opacity="0.45"
        />
        <circle cx="100" cy="100" r="1.5" fill="var(--color-faint)" opacity="0.7" />
      </svg>

      <div class="absolute inset-0 flex flex-col items-center justify-center text-center">
        <span class="eyebrow !text-[10px]">{{ hasQuota ? caption : 'no quota' }}</span>
        <div
          class="title mt-1 tabular-nums"
          :class="[percentClass, axis ? 'text-error' : 'text-base-content']"
        >
          {{ displayPercent
          }}<span v-if="hasQuota" class="text-[0.5em] font-normal text-dim">%</span>
        </div>
        <span v-if="size !== 'sm'" class="mt-1.5 text-2xs tracking-[0.04em] text-faint">
          {{ formatBytes(vault.usedBytes) }} / {{ formatBytes(vault.quotaBytes) }}
        </span>
        <span v-else class="mt-1 text-2xs tracking-[0.08em] text-faint">
          {{ formatBytes(vault.usedBytes) }} used
        </span>
      </div>
    </div>

    <div v-if="legend" class="grid w-full grid-cols-2 gap-3">
      <div>
        <span class="eyebrow">used</span>
        <div class="mt-1 flex items-center gap-1.5 text-xs text-base-content">
          <span class="dot bg-brass" aria-hidden="true" />
          {{ formatBytes(vault.usedBytes) }}
        </div>
      </div>
      <div class="text-right">
        <span class="eyebrow">free</span>
        <div class="mt-1 flex items-center justify-end gap-1.5 text-xs text-dim">
          {{ formatBytes(vault.remainingBytes) }}
          <span class="dot bg-line" aria-hidden="true" />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.platter-arc {
  transition: stroke-dasharray 620ms cubic-bezier(0.22, 1, 0.36, 1);
}
</style>
