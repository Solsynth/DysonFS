<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ArrowRight16, Checkmark16, Renew16, TrashCan16, WarningAlt16 } from '@carbon/icons-vue'

import ConsolePanel from '@/components/ConsolePanel.vue'
import PageHeader from '@/components/PageHeader.vue'
import VolumePlatter from '@/components/VolumePlatter.vue'
import { formatBytes, formatCount, formatPercent, initialOf } from '@/lib/format'
import { useSessionStore } from '@/stores/session'
import { useVaultStore } from '@/stores/vault'

const { t } = useI18n()
const vault = useVaultStore()
const session = useSessionStore()

const MiB = 1024 * 1024

const quota = computed(() => vault.quota)

const quotaRows = computed(() => {
  const source = quota.value
  if (!source) return []
  return [
    { label: t('storage.baseAllocation'), note: t('storage.plan'), mb: source.based_quota },
    { label: t('storage.levelingBonus'), note: t('storage.level'), mb: source.leveling_quota },
    { label: t('storage.perkAllocation'), note: t('storage.perks'), mb: source.perk_quota },
    { label: t('storage.addonQuota'), note: t('storage.purchased'), mb: source.extra_quota },
  ]
})

const totalQuotaMb = computed(() => quota.value?.total_quota ?? 0)
const averageObject = computed(() => (vault.fileCount > 0 ? vault.usedBytes / vault.fileCount : 0))
const poolTotal = computed(() => vault.poolRows.reduce((sum, row) => sum + row.bytes, 0))
const maxPool = computed(() => Math.max(1, ...vault.poolRows.map((row) => row.bytes)))
const operator = computed(
  () => session.identity?.nick || session.identity?.name || t('storage.account'),
)


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
        :title="t('storage.title')"
        :description="t('storage.description')"
      >
        <template #actions>
          <span
            v-if="vault.sample"
            class="flex items-center gap-1.5 text-[11px] text-faint"
          >
            <span class="dot bg-primary" aria-hidden="true" />
            {{ t('storage.sampleData') }}
          </span>
          <button
            class="btn btn-outline btn-sm"
            type="button"
            :disabled="vault.volumeLoading"
            :title="t('storage.refresh')"
            @click="vault.loadVolume()"
          >
            <Renew16 class="mr-1.5" :class="vault.volumeLoading ? 'animate-spin' : ''" />
            {{ t('storage.refreshAction') }}
          </button>
        </template>
      </PageHeader>

      <div
        v-if="vault.volumeError"
        class="mb-4 flex items-start gap-2 rounded-lg bg-error/10 px-3 py-2.5 text-[11px] text-error"
      >
        <WarningAlt16 class="mt-0.5 h-3.5 w-3.5 shrink-0" />
        <div>
          <span class="font-medium">{{ t('storage.readingsIncomplete') }}</span>
          <span class="text-error/80"> {{ vault.volumeError }}</span>
        </div>
      </div>

      <div class="grid gap-4 xl:grid-cols-[380px_1fr]">
        <ConsolePanel
          :title="t('storage.volume')"
          :note="t('storage.total', { size: formatBytes(vault.quotaBytes) })"
        >
          <div class="flex flex-col items-center pt-2">
            <VolumePlatter size="lg" :legend="false" />
            <div class="mt-8 flex w-full items-center justify-between text-[11px] text-faint">
              <span>{{ t('storage.capacityState') }}</span>
              <span class="flex items-center gap-1.5 text-dim">
                <Checkmark16 class="h-3 w-3 text-success" />
                {{ t('storage.healthy') }}
              </span>
            </div>
          </div>
        </ConsolePanel>

        <div class="flex flex-col gap-4">
          <ConsolePanel :title="t('storage.readings')">
            <div class="grid grid-cols-2 gap-x-8 gap-y-7">
              <div>
                <div class="text-[11px] text-faint">{{ t('storage.consumed') }}</div>
                <div class="title mt-1 text-[22px] tabular-nums text-base-content">
                  {{ formatBytes(vault.usedBytes) }}
                </div>
                <div class="mt-1 text-[11px] text-faint">
                  {{ t('storage.ofQuota', { percent: formatPercent(vault.usedBytes, vault.quotaBytes, 1) }) }}
                </div>
              </div>
              <div>
                <div class="text-[11px] text-faint">{{ t('storage.remaining') }}</div>
                <div class="title mt-1 text-[22px] tabular-nums text-base-content">
                  {{ formatBytes(vault.remainingBytes) }}
                </div>
                <div class="mt-1 text-[11px] text-faint">{{ t('storage.beforeThrottle') }}</div>
              </div>
              <div>
                <div class="text-[11px] text-faint">{{ t('storage.indexedFiles') }}</div>
                <div class="title mt-1 text-[22px] tabular-nums text-base-content">
                  {{ formatCount(vault.fileCount) }}
                </div>
                <div class="mt-1 text-[11px] text-faint">
                  {{ t('storage.acrossPools', { n: vault.pools.length }) }}
                </div>
              </div>
              <div>
                <div class="text-[11px] text-faint">{{ t('storage.averageFile') }}</div>
                <div class="title mt-1 text-[22px] tabular-nums text-base-content">
                  {{ formatBytes(averageObject) }}
                </div>
                <div class="mt-1 text-[11px] text-faint">{{ t('storage.meanPayload') }}</div>
              </div>
            </div>
          </ConsolePanel>

          <ConsolePanel
            class="flex-1"
            :title="t('storage.allocation')"
            :note="t('storage.total', { size: formatBytes(totalQuotaMb * MiB) })"
          >
            <div v-if="quotaRows.length === 0" class="text-[11px] text-faint">
              {{ t('storage.quotaUnavailable') }}
            </div>
            <div v-else class="space-y-4">
              <div v-for="row in quotaRows" :key="row.label">
                <div class="flex items-baseline justify-between gap-3 text-[12px]">
                  <span class="text-dim">{{ row.label }}</span>
                  <span class="tabular-nums text-base-content">{{
                    formatBytes(row.mb * MiB)
                  }}</span>
                </div>
                <div class="mt-2 flex items-center gap-3">
                  <div class="h-1 flex-1 overflow-hidden rounded-full bg-line-soft">
                    <div
                      class="h-full rounded-full bg-primary/70"
                      :style="{ width: quotaWidth(row.mb) }"
                    />
                  </div>
                  <span class="w-10 text-right text-[11px] tabular-nums text-faint">
                    {{ formatPercent(row.mb, totalQuotaMb, 0) }}
                  </span>
                </div>
              </div>

              <div class="flex items-baseline justify-between gap-3 border-t border-line-soft pt-3 text-[12px]">
                <span class="text-faint">{{ t('storage.totalQuota') }}</span>
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
        :title="t('storage.pools')"
        :note="t('storage.poolsNote', { n: vault.pools.length, size: formatBytes(poolTotal) })"
      >
        <div class="table-bleed overflow-x-auto">
          <table class="ledger min-w-[700px]">
            <thead>
              <tr>
                <th scope="col">{{ t('storage.pool') }}</th>
                <th scope="col">{{ t('storage.bucket') }}</th>
                <th scope="col">{{ t('storage.stored') }}</th>
                <th scope="col">{{ t('storage.share') }}</th>
                <th scope="col">{{ t('storage.multiplier') }}</th>
                <th scope="col">{{ t('storage.access') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in vault.poolRows" :key="row.pool.id">
                <td>
                  <div class="text-[13px] text-base-content">{{ row.pool.name }}</div>
                  <div class="text-[11px] text-faint">
                    {{ row.pool.description || t('storage.noDescription') }}
                  </div>
                </td>
                <td class="font-mono text-[11px] text-faint">
                  {{ row.pool.storage_config.bucket || '—' }}
                </td>
                <td>
                  <div class="text-[12px] tabular-nums text-dim">{{ formatBytes(row.bytes) }}</div>
                  <div class="mt-2 h-1 w-[110px] overflow-hidden rounded-full bg-line-soft">
                    <div
                      class="h-full rounded-full bg-primary/50"
                      :style="{ width: poolWidth(row.bytes) }"
                    />
                  </div>
                </td>
                <td class="text-[11px] tabular-nums text-faint">{{ shareOfDrive(row.bytes) }}</td>
                <td class="text-[11px] tabular-nums text-faint">
                  ×{{ row.pool.billing_config.cost_multiplier ?? 1 }}
                </td>
                <td>
                  <span
                    class="text-[11px]"
                    :class="row.pool.policy_config.public_usable ? 'text-primary' : 'text-faint'"
                  >
                    {{
                      row.pool.policy_config.public_usable
                        ? t('storage.public')
                        : t('storage.private')
                    }}
                  </span>
                </td>
              </tr>
              <tr v-if="vault.poolRows.length === 0">
                <td colspan="6" class="py-8 text-center text-[11px] text-faint">
                  {{ t('storage.noPools') }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </ConsolePanel>

      <div class="mt-4 grid gap-4 md:grid-cols-2">
        <ConsolePanel :title="t('storage.reclaim')">
          <div class="flex items-start gap-3">
            <TrashCan16 class="mt-0.5 h-4 w-4 shrink-0 text-faint" />
            <div class="min-w-0">
              <h3 class="title text-[14px] text-base-content">
                {{ t('storage.reclaimTitle') }}
              </h3>
              <p class="voice mt-1 text-[12px]">
                {{ t('storage.reclaimBody') }}
              </p>
              <RouterLink
                to="/recycle"
                class="mt-2 inline-flex items-center gap-1 text-[12px] text-primary hover:text-base-content"
              >
                {{ t('storage.openRecycle') }}
                <ArrowRight16 class="h-3 w-3" />
              </RouterLink>
            </div>
          </div>
        </ConsolePanel>

        <ConsolePanel
          :title="t('storage.account')"
          :note="session.sample ? t('storage.sampleData') : t('storage.liveAccount')"
        >
          <div class="flex items-center gap-2.5">
            <span
              class="grid size-8 place-items-center rounded-full bg-base-300 text-[11px] font-semibold text-dim"
            >
              {{ initialOf(operator) }}
            </span>
            <div class="min-w-0">
              <div class="truncate text-[13px] text-base-content">{{ operator }}</div>
              <div class="text-[11px] text-faint">
                {{ session.identity?.is_superuser ? t('storage.superuser') : t('storage.authenticated') }}
              </div>
            </div>
            <span class="ml-auto dot bg-success" aria-hidden="true" />
          </div>
          <div class="mt-4 space-y-2 text-[11px]">
            <div class="flex items-center justify-between text-faint">
              <span>{{ t('storage.account') }}</span>
              <span class="font-mono text-dim">{{ session.identity?.name || '—' }}</span>
            </div>
            <div class="flex items-center justify-between text-faint">
              <span>{{ t('storage.state') }}</span>
              <span class="text-dim">{{
                session.sample ? t('storage.sampleData') : t('storage.liveAccount')
              }}</span>
            </div>
          </div>
        </ConsolePanel>
      </div>
    </div>
  </div>
</template>
