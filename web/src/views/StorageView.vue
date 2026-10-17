<script setup lang="ts">
import { computed } from 'vue'
import {
  ArrowRight16,
  Checkmark16,
  DataBase16,
  Renew16,
  StoragePool16,
  TimePlot16,
  TrashCan16,
  VolumeObjectStorage16,
  WarningAlt16,
} from '@carbon/icons-vue'

import VolumePlatter from '@/components/VolumePlatter.vue'
import { formatBytes, formatCount, formatPercent, initialOf } from '@/lib/format'
import { useSessionStore } from '@/stores/session'
import { useVaultStore } from '@/stores/vault'

const vault = useVaultStore()
const session = useSessionStore()

const MiB = 1024 * 1024

const quota = computed(() => vault.quota)

const quotaRows = computed(() => {
  const source = quota.value
  if (!source) return []
  return [
    { label: 'Base allocation', note: 'plan', mb: source.based_quota },
    { label: 'Leveling bonus', note: 'level', mb: source.leveling_quota },
    { label: 'Perk allocation', note: 'perks', mb: source.perk_quota },
    { label: 'Add-on quota', note: 'purchased', mb: source.extra_quota },
  ]
})

const totalQuotaMb = computed(() => quota.value?.total_quota ?? 0)
const averageObject = computed(() =>
  vault.fileCount > 0 ? vault.usedBytes / vault.fileCount : 0,
)
const poolTotal = computed(() => vault.poolRows.reduce((sum, row) => sum + row.bytes, 0))
const maxPool = computed(() => Math.max(1, ...vault.poolRows.map((row) => row.bytes)))
const operator = computed(() => session.identity?.nick || session.identity?.name || 'operator')

const poolColors = ['#c9ae83', '#8184c9', '#8fa6c9', '#8fbaa0', '#c98a7f']

function poolWidth(bytes: number): string {
  return `${Math.max(1.5, (bytes / maxPool.value) * 100)}%`
}

function shareOfDrive(bytes: number): string {
  return formatPercent(bytes, vault.usedBytes || 1, 1)
}

function quotaWidth(mb: number): string {
  if (!totalQuotaMb.value) return '0%'
  return `${Math.max(0, (mb / totalQuotaMb.value) * 100)}%`
}
</script>

<template>
  <div class="min-h-0 flex-1 overflow-y-auto scroll-region bg-ink">
    <div class="mx-auto max-w-[1180px] px-4 py-6 md:px-6 md:py-8">
      <header class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <div class="eyebrow">capacity / volume a</div>
          <h1 class="title mt-1 text-[28px] leading-tight text-base-content">Storage</h1>
          <p class="voice mt-1.5 max-w-[560px]">
            One volume, split across object pools. Brass marks the quota actually consumed; every
            other reading is a proportion of that same budget.
          </p>
        </div>
        <div class="flex items-center gap-2">
          <span
            v-if="vault.sample"
            class="flex items-center gap-1.5 border border-brass/30 bg-brass/10 px-2 py-1 text-3xs uppercase tracking-[0.12em] text-brass"
          >
            <span class="dot bg-brass pulse-soft" aria-hidden="true" />
            sample telemetry
          </span>
          <button
            class="btn btn-outline btn-sm"
            type="button"
            :disabled="vault.volumeLoading"
            @click="vault.loadVolume()"
          >
            <Renew16 class="mr-1.5" :class="vault.volumeLoading ? 'animate-spin' : ''" />
            Refresh
          </button>
        </div>
      </header>

      <div
        v-if="vault.volumeError"
        class="mt-5 flex items-start gap-2 border border-error/30 bg-error/5 px-3 py-2.5 text-2xs text-error"
      >
        <WarningAlt16 class="mt-0.5 h-3.5 w-3.5 shrink-0" />
        <div>
          <span class="font-medium">Telemetry incomplete.</span>
          <span class="text-error/80"> {{ vault.volumeError }}</span>
        </div>
      </div>

      <div class="mt-6 grid gap-3 xl:grid-cols-[400px_1fr]">
        <section class="panel flex flex-col items-center justify-center px-5 py-6">
          <div class="flex w-full items-center justify-between">
            <span class="eyebrow">volume platter</span>
            <span class="flex items-center gap-1.5 text-3xs text-faint">
              <span class="dot bg-brass" aria-hidden="true" />
              quota
            </span>
          </div>
          <div class="mt-4">
            <VolumePlatter size="lg" :legend="true" caption="volume a" />
          </div>
          <div class="mt-5 w-full border-t border-line pt-3">
            <div class="flex items-center justify-between text-3xs text-faint">
              <span>total volume</span>
              <span class="tabular-nums text-dim">{{ formatBytes(vault.quotaBytes) }}</span>
            </div>
            <div class="mt-1.5 flex items-center justify-between text-3xs text-faint">
              <span>capacity state</span>
              <span class="flex items-center gap-1.5">
                <Checkmark16 class="h-3 w-3 text-success" />
                nominal
              </span>
            </div>
          </div>
        </section>

        <div class="flex flex-col gap-3">
          <div class="grid grid-cols-2 gap-px border border-line bg-line">
            <div class="bg-ink p-3.5">
              <div class="eyebrow">consumed</div>
              <div class="title mt-1.5 text-[22px] tabular-nums text-base-content">
                {{ formatBytes(vault.usedBytes) }}
              </div>
              <div class="mt-0.5 text-3xs text-faint">{{ formatPercent(vault.usedBytes, vault.quotaBytes, 1) }} of quota</div>
            </div>
            <div class="bg-ink p-3.5">
              <div class="eyebrow">remaining</div>
              <div class="title mt-1.5 text-[22px] tabular-nums text-base-content">
                {{ formatBytes(vault.remainingBytes) }}
              </div>
              <div class="mt-0.5 text-3xs text-faint">available before throttle</div>
            </div>
            <div class="bg-ink p-3.5">
              <div class="eyebrow">indexed objects</div>
              <div class="title mt-1.5 text-[22px] tabular-nums text-base-content">
                {{ formatCount(vault.fileCount) }}
              </div>
              <div class="mt-0.5 text-3xs text-faint">across {{ vault.pools.length }} pools</div>
            </div>
            <div class="bg-ink p-3.5">
              <div class="eyebrow">average object</div>
              <div class="title mt-1.5 text-[22px] tabular-nums text-base-content">
                {{ formatBytes(averageObject) }}
              </div>
              <div class="mt-0.5 text-3xs text-faint">mean stored payload</div>
            </div>
          </div>

          <section class="panel flex-1 p-4">
            <div class="flex items-center justify-between">
              <span class="eyebrow">allocation ledger</span>
              <span class="text-3xs tabular-nums text-faint">
                {{ formatBytes(totalQuotaMb * MiB) }} total
              </span>
            </div>

            <div v-if="quotaRows.length === 0" class="mt-4 text-2xs text-faint">
              Quota breakdown unavailable.
            </div>
            <div v-else class="mt-3 space-y-3">
              <div v-for="row in quotaRows" :key="row.label">
                <div class="flex items-baseline justify-between gap-3 text-2xs">
                  <span class="text-dim">{{ row.label }}</span>
                  <span class="tabular-nums text-base-content">{{ formatBytes(row.mb * MiB) }}</span>
                </div>
                <div class="mt-1.5 flex items-center gap-2">
                  <div class="h-[3px] flex-1 bg-line-soft">
                    <div class="h-full bg-dim/50" :style="{ width: quotaWidth(row.mb) }" />
                  </div>
                  <span class="w-10 text-right text-3xs tabular-nums text-faint">
                    {{ formatPercent(row.mb, totalQuotaMb, 0) }}
                  </span>
                </div>
              </div>

              <div class="flex items-baseline justify-between gap-3 border-t border-line pt-2.5 text-2xs">
                <span class="uppercase tracking-[0.12em] text-faint">total quota</span>
                <span class="tabular-nums text-base-content">{{ formatBytes(totalQuotaMb * MiB) }}</span>
              </div>
            </div>
          </section>
        </div>
      </div>

      <section class="mt-3 border border-line">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-3">
          <div>
            <div class="eyebrow">object pools</div>
            <h2 class="title mt-0.5 text-lg text-base-content">Where the volume lives</h2>
          </div>
          <div class="flex items-center gap-3 text-3xs text-faint">
            <span>{{ vault.pools.length }} visible</span>
            <span class="h-3 w-px bg-line" aria-hidden="true" />
            <span class="tabular-nums">{{ formatBytes(poolTotal) }} measured</span>
          </div>
        </div>

        <div class="overflow-x-auto">
          <table class="ledger min-w-[700px]">
            <thead>
              <tr>
                <th>Pool</th>
                <th>Bucket</th>
                <th>Stored</th>
                <th>Share</th>
                <th>Multiplier</th>
                <th class="text-right">Access</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(row, index) in vault.poolRows" :key="row.pool.id">
                <td class="py-3">
                  <div class="flex items-center gap-2">
                    <span
                      class="dot"
                      :style="{ backgroundColor: poolColors[index % poolColors.length] }"
                      aria-hidden="true"
                    />
                    <div>
                      <div class="text-xs text-base-content">{{ row.pool.name }}</div>
                      <div class="text-3xs text-faint">{{ row.pool.description || 'no description' }}</div>
                    </div>
                  </div>
                </td>
                <td class="py-3 font-mono text-3xs text-faint">
                  {{ row.pool.storage_config.bucket || '—' }}
                </td>
                <td class="py-3">
                  <div class="text-2xs tabular-nums text-dim">{{ formatBytes(row.bytes) }}</div>
                  <div class="mt-1.5 h-[2px] w-[110px] bg-line-soft">
                    <div
                      class="h-full"
                      :style="{
                        width: poolWidth(row.bytes),
                        backgroundColor: poolColors[index % poolColors.length],
                        opacity: 0.75,
                      }"
                    />
                  </div>
                </td>
                <td class="py-3 text-2xs tabular-nums text-faint">{{ shareOfDrive(row.bytes) }}</td>
                <td class="py-3 text-2xs tabular-nums text-faint">
                  ×{{ row.pool.billing_config.cost_multiplier ?? 1 }}
                </td>
                <td class="py-3 text-right">
                  <span
                    class="chip"
                    :class="row.pool.policy_config.public_usable ? 'border-brass/30 text-brass' : ''"
                  >
                    {{ row.pool.policy_config.public_usable ? 'public' : 'private' }}
                  </span>
                </td>
              </tr>
              <tr v-if="vault.poolRows.length === 0">
                <td colspan="6" class="py-8 text-center text-2xs text-faint">
                  No visible pools were reported by the master.
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <div class="mt-3 grid gap-3 md:grid-cols-2">
        <section class="panel flex items-start gap-3 p-4">
          <span class="grid size-9 shrink-0 place-items-center border border-line bg-base-200/50">
            <TrashCan16 class="h-4 w-4 text-brass" />
          </span>
          <div class="min-w-0">
            <div class="eyebrow">reclaim</div>
            <h2 class="title mt-0.5 text-base text-base-content">Recycled objects still hold capacity.</h2>
            <p class="voice mt-1 text-2xs">
              Purge the bin to release storage back to the volume. Restore instead if the payload
              still matters.
            </p>
            <RouterLink to="/recycle" class="mt-2 inline-flex items-center gap-1 text-2xs text-primary hover:text-base-content">
              Open recycle bin
              <ArrowRight16 class="h-3 w-3" />
            </RouterLink>
          </div>
        </section>

        <section class="panel p-4">
          <div class="flex items-center justify-between">
            <span class="eyebrow">operator</span>
            <span class="text-3xs text-faint">{{ session.sample ? 'sample session' : 'live session' }}</span>
          </div>
          <div class="mt-3 flex items-center gap-2.5">
            <span class="grid size-8 place-items-center border border-line bg-base-300 text-2xs font-semibold text-dim">
              {{ initialOf(operator) }}
            </span>
            <div class="min-w-0">
              <div class="truncate text-xs text-base-content">{{ operator }}</div>
              <div class="text-3xs uppercase tracking-[0.12em] text-faint">
                {{ session.identity?.is_superuser ? 'superuser' : 'authenticated' }}
              </div>
            </div>
            <span class="ml-auto dot bg-success" aria-hidden="true" />
          </div>
          <div class="mt-3 divide-y divide-line-soft border-t border-line text-3xs">
            <div class="flex items-center justify-between py-2 text-faint">
              <span>account</span>
              <span class="font-mono text-dim">{{ session.identity?.name || '—' }}</span>
            </div>
            <div class="flex items-center justify-between py-2 text-faint">
              <span>quota source</span>
              <span class="text-dim">/api/billing/quota</span>
            </div>
            <div class="flex items-center justify-between py-2 text-faint">
              <span>usage source</span>
              <span class="text-dim">/api/billing/usage</span>
            </div>
          </div>
        </section>
      </div>

      <footer class="mt-3 flex flex-wrap items-center gap-2 border-t border-line pt-3 text-3xs text-faint">
        <TimePlot16 class="h-3.5 w-3.5" />
        <span>Last sampled {{ vault.volumeLoading ? 'now' : 'this session' }}.</span>
        <span class="h-2.5 w-px bg-line" aria-hidden="true" />
        <DataBase16 class="h-3.5 w-3.5" />
        <span>Quota is metered in MiB; byte values are rendered from the same reading.</span>
        <VolumeObjectStorage16 class="ml-auto hidden h-4 w-4 text-line sm:block" />
        <StoragePool16 class="hidden h-4 w-4 text-line sm:block" />
      </footer>
    </div>
  </div>
</template>
