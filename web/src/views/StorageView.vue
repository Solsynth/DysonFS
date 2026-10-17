<script setup lang="ts">
import { computed } from 'vue'
import { ArrowRight16, Checkmark16, Renew16, TrashCan16, WarningAlt16 } from '@carbon/icons-vue'

import ConsolePanel from '@/components/ConsolePanel.vue'
import PageHeader from '@/components/PageHeader.vue'
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
const averageObject = computed(() => (vault.fileCount > 0 ? vault.usedBytes / vault.fileCount : 0))
const poolTotal = computed(() => vault.poolRows.reduce((sum, row) => sum + row.bytes, 0))
const maxPool = computed(() => Math.max(1, ...vault.poolRows.map((row) => row.bytes)))
const operator = computed(() => session.identity?.nick || session.identity?.name || 'account')

const poolColors = ['#c7b08a', '#7577ba', '#8fa6c9', '#8db39a', '#c08579']

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
  <div class="min-h-0 flex-1 overflow-y-auto scroll-region bg-base-100">
    <div class="px-5 py-6 sm:px-7 sm:py-8">
      <PageHeader
        title="Storage"
        description="One volume, split across object pools. Brass marks the quota actually consumed; every other reading is a proportion of that same budget."
      >
        <template #actions>
          <span
            v-if="vault.sample"
            class="flex items-center gap-1.5 border border-brass/30 bg-brass/10 px-2 py-1 text-2xs uppercase tracking-[0.12em] text-brass"
          >
            <span class="dot bg-brass" aria-hidden="true" />
            sample data
          </span>
          <button
            class="btn btn-outline btn-sm"
            type="button"
            :disabled="vault.volumeLoading"
            title="Refresh storage readings"
            @click="vault.loadVolume()"
          >
            <Renew16 class="mr-1.5" :class="vault.volumeLoading ? 'animate-spin' : ''" />
            Refresh
          </button>
        </template>
      </PageHeader>

      <div
        v-if="vault.volumeError"
        class="mb-4 flex items-start gap-2 border border-error/30 bg-error/5 px-3 py-2.5 text-2xs text-error"
      >
        <WarningAlt16 class="mt-0.5 h-3.5 w-3.5 shrink-0" />
        <div>
          <span class="font-medium">Readings are incomplete.</span>
          <span class="text-error/80"> {{ vault.volumeError }}</span>
        </div>
      </div>

      <div class="grid gap-4 xl:grid-cols-[400px_1fr]">
        <ConsolePanel title="Volume platter" note="volume a">
          <div class="flex flex-col items-center">
            <VolumePlatter size="lg" :legend="true" caption="volume a" />
            <div class="mt-5 w-full space-y-1.5">
              <div class="flex items-center justify-between text-2xs text-faint">
                <span>total volume</span>
                <span class="tabular-nums text-dim">{{ formatBytes(vault.quotaBytes) }}</span>
              </div>
              <div class="flex items-center justify-between text-2xs text-faint">
                <span>capacity state</span>
                <span class="flex items-center gap-1.5">
                  <Checkmark16 class="h-3 w-3 text-success" />
                  nominal
                </span>
              </div>
            </div>
          </div>
        </ConsolePanel>

        <div class="flex flex-col gap-4">
          <div class="grid grid-cols-2 gap-3">
            <div class="border border-line bg-base-200/30 p-4">
              <div class="eyebrow">consumed</div>
              <div class="title mt-1.5 text-lg tabular-nums text-base-content">
                {{ formatBytes(vault.usedBytes) }}
              </div>
              <div class="mt-0.5 text-2xs text-faint">
                {{ formatPercent(vault.usedBytes, vault.quotaBytes, 1) }} of quota
              </div>
            </div>
            <div class="border border-line bg-base-200/30 p-4">
              <div class="eyebrow">remaining</div>
              <div class="title mt-1.5 text-lg tabular-nums text-base-content">
                {{ formatBytes(vault.remainingBytes) }}
              </div>
              <div class="mt-0.5 text-2xs text-faint">available before throttle</div>
            </div>
            <div class="border border-line bg-base-200/30 p-4">
              <div class="eyebrow">indexed objects</div>
              <div class="title mt-1.5 text-lg tabular-nums text-base-content">
                {{ formatCount(vault.fileCount) }}
              </div>
              <div class="mt-0.5 text-2xs text-faint">across {{ vault.pools.length }} pools</div>
            </div>
            <div class="border border-line bg-base-200/30 p-4">
              <div class="eyebrow">average object</div>
              <div class="title mt-1.5 text-lg tabular-nums text-base-content">
                {{ formatBytes(averageObject) }}
              </div>
              <div class="mt-0.5 text-2xs text-faint">mean stored payload</div>
            </div>
          </div>

          <ConsolePanel
            class="flex-1"
            title="Allocation"
            :note="`${formatBytes(totalQuotaMb * MiB)} total`"
          >
            <div v-if="quotaRows.length === 0" class="text-2xs text-faint">
              Quota breakdown unavailable.
            </div>
            <div v-else class="space-y-3">
              <div v-for="row in quotaRows" :key="row.label">
                <div class="flex items-baseline justify-between gap-3 text-2xs">
                  <span class="text-dim">{{ row.label }}</span>
                  <span class="tabular-nums text-base-content">{{
                    formatBytes(row.mb * MiB)
                  }}</span>
                </div>
                <div class="mt-1.5 flex items-center gap-2">
                  <div class="h-[3px] flex-1 bg-line-soft">
                    <div class="h-full bg-dim/50" :style="{ width: quotaWidth(row.mb) }" />
                  </div>
                  <span class="w-10 text-right text-2xs tabular-nums text-faint">
                    {{ formatPercent(row.mb, totalQuotaMb, 0) }}
                  </span>
                </div>
              </div>

              <div class="flex items-baseline justify-between gap-3 pt-1 text-2xs">
                <span class="uppercase tracking-[0.12em] text-faint">total quota</span>
                <span class="tabular-nums text-base-content">{{
                  formatBytes(totalQuotaMb * MiB)
                }}</span>
              </div>
            </div>
          </ConsolePanel>
        </div>
      </div>

      <ConsolePanel
        class="mt-4"
        title="Object pools"
        :note="`${vault.pools.length} visible · ${formatBytes(poolTotal)} measured`"
      >
        <div class="table-bleed overflow-x-auto">
          <table class="ledger min-w-[700px]">
            <thead>
              <tr>
                <th scope="col">Pool</th>
                <th scope="col">Bucket</th>
                <th scope="col">Stored</th>
                <th scope="col">Share</th>
                <th scope="col">Multiplier</th>
                <th scope="col">Access</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(row, index) in vault.poolRows" :key="row.pool.id">
                <td>
                  <div class="flex items-center gap-2">
                    <span
                      class="dot"
                      :style="{ backgroundColor: poolColors[index % poolColors.length] }"
                      aria-hidden="true"
                    />
                    <div>
                      <div class="text-xs text-base-content">{{ row.pool.name }}</div>
                      <div class="text-2xs text-faint">
                        {{ row.pool.description || 'no description' }}
                      </div>
                    </div>
                  </div>
                </td>
                <td class="font-mono text-2xs text-faint">
                  {{ row.pool.storage_config.bucket || '—' }}
                </td>
                <td>
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
                <td class="text-2xs tabular-nums text-faint">{{ shareOfDrive(row.bytes) }}</td>
                <td class="text-2xs tabular-nums text-faint">
                  ×{{ row.pool.billing_config.cost_multiplier ?? 1 }}
                </td>
                <td>
                  <span
                    class="chip"
                    :class="
                      row.pool.policy_config.public_usable ? 'border-brass/30 text-brass' : ''
                    "
                  >
                    {{ row.pool.policy_config.public_usable ? 'public' : 'private' }}
                  </span>
                </td>
              </tr>
              <tr v-if="vault.poolRows.length === 0">
                <td colspan="6" class="py-8 text-center text-2xs text-faint">
                  No visible pools were reported.
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </ConsolePanel>

      <div class="mt-4 grid gap-4 md:grid-cols-2">
        <ConsolePanel title="Reclaim">
          <div class="flex items-start gap-3">
            <span class="grid size-9 shrink-0 place-items-center border border-line bg-base-200/50">
              <TrashCan16 class="h-4 w-4 text-brass" />
            </span>
            <div class="min-w-0">
              <h3 class="title text-base text-base-content">
                Recycled objects still hold capacity.
              </h3>
              <p class="voice mt-1 text-2xs">
                Purge the bin to release storage back to the volume. Restore instead if the payload
                still matters.
              </p>
              <RouterLink
                to="/recycle"
                class="mt-2 inline-flex items-center gap-1 text-2xs text-primary hover:text-base-content"
              >
                Open recycle bin
                <ArrowRight16 class="h-3 w-3" />
              </RouterLink>
            </div>
          </div>
        </ConsolePanel>

        <ConsolePanel title="Account" :note="session.sample ? 'sample data' : 'live account'">
          <div class="flex items-center gap-2.5">
            <span
              class="grid size-8 place-items-center border border-line bg-base-300 text-2xs font-semibold text-dim"
            >
              {{ initialOf(operator) }}
            </span>
            <div class="min-w-0">
              <div class="truncate text-xs text-base-content">{{ operator }}</div>
              <div class="text-2xs uppercase tracking-[0.12em] text-faint">
                {{ session.identity?.is_superuser ? 'superuser' : 'authenticated' }}
              </div>
            </div>
            <span class="ml-auto dot bg-success" aria-hidden="true" />
          </div>
          <div class="mt-3 space-y-2 text-2xs">
            <div class="flex items-center justify-between text-faint">
              <span>account</span>
              <span class="font-mono text-dim">{{ session.identity?.name || '—' }}</span>
            </div>
            <div class="flex items-center justify-between text-faint">
              <span>state</span>
              <span class="text-dim">{{ session.sample ? 'sample data' : 'live account' }}</span>
            </div>
          </div>
        </ConsolePanel>
      </div>
    </div>
  </div>
</template>
