<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { Component } from 'vue'
import {
  CircleDash16,
  Folder16,
  StoragePool16,
  TrashCan16,
  WarningAlt16,
} from '@carbon/icons-vue'

import { formatBytes, formatCount } from '@/lib/format'
import { useUiStore } from '@/stores/ui'
import { useVaultStore } from '@/stores/vault'

const ui = useUiStore()
const { t } = useI18n()
const vault = useVaultStore()
const route = useRoute()
const router = useRouter()

interface NavItem {
  label: string
  to: string
  icon: Component
  match: string
  badge?: string
}

const nav = computed<NavItem[]>(() => [
  {
    label: t('nav.drive'),
    to: '/drive',
    icon: Folder16,
    match: '/drive',
    badge: vault.fileCount > 0 ? formatCount(vault.fileCount) : undefined,
  },
  { label: t('nav.unindexed'), to: '/unindexed', icon: WarningAlt16, match: '/unindexed' },
  { label: t('nav.recycle'), to: '/recycle', icon: TrashCan16, match: '/recycle' },
])

const ITEM_HEIGHT = 36

function isCurrent(item: NavItem): boolean {
  return route.path === item.to || (item.match === '/drive' && route.path.startsWith('/drive'))
}

const activeIndex = computed(() => nav.value.findIndex(isCurrent))
const storageCurrent = computed(() => route.path === '/storage')
const usedPercent = computed(() => Math.round(vault.usedFraction * 100))
const usedWidth = computed(() => `${Math.min(100, Math.max(0, usedPercent.value))}%`)

function navigate() {
  ui.sidebarOpen = false
}

function selectPool(poolId: string | null) {
  if (route.meta.mode !== 'drive') {
    void router.push('/drive')
  }
  vault.setPoolFilter(poolId)
  navigate()
}
</script>

<template>
  <button
    v-if="ui.sidebarOpen"
    type="button"
    class="fixed inset-0 z-40 bg-black/55 lg:hidden"
    :aria-label="t('nav.closeNavigation')"
    @click="ui.sidebarOpen = false"
  />

  <aside
    class="fixed bottom-8 left-0 top-[52px] z-40 flex w-[236px] shrink-0 flex-col border-r border-line bg-ink transition-transform duration-200 ease-out lg:static lg:z-auto lg:h-full lg:translate-x-0"
    :class="ui.sidebarOpen ? 'translate-x-0' : '-translate-x-full'"
    :aria-label="t('app.console')"
  >
    <nav
      class="flex-1 overflow-y-auto px-2.5 py-3 scroll-region"
      :aria-label="t('nav.locations')"
    >
      <div class="relative">
        <span
          v-if="activeIndex >= 0"
          class="nav-glider"
          :style="{ transform: `translateY(${activeIndex * ITEM_HEIGHT}px)` }"
          aria-hidden="true"
        />
        <RouterLink
          v-for="item in nav"
          :key="item.to"
          :to="item.to"
          class="relative flex h-9 items-center gap-2 pl-3.5 pr-2.5 text-xs transition-colors duration-150"
          :class="
            isCurrent(item) ? 'text-base-content' : 'text-dim hover:bg-base-200/60 hover:text-base-content'
          "
          :aria-current="isCurrent(item) ? 'page' : undefined"
          @click="navigate"
        >
          <component
            :is="item.icon"
            class="shrink-0 transition-colors"
            :class="isCurrent(item) ? 'text-primary' : 'text-faint'"
            aria-hidden="true"
          />
          <span class="truncate">{{ item.label }}</span>
          <span v-if="item.badge" class="ml-auto text-2xs tabular-nums text-faint">
            {{ item.badge }}
          </span>
        </RouterLink>
      </div>

      <div class="mt-6">
        <div class="flex items-center justify-between px-2.5 pb-1.5">
          <span class="eyebrow">{{ t('nav.pools') }}</span>
          <span class="text-2xs tabular-nums text-faint">{{ vault.pools.length }}</span>
        </div>

        <button
          type="button"
          class="flex w-full items-center gap-2 px-2.5 py-1 text-left text-2xs transition-colors duration-150"
          :class="
            vault.poolFilter === null
              ? 'bg-base-300 text-base-content'
              : 'text-dim hover:bg-base-200/60 hover:text-base-content'
          "
          @click="selectPool(null)"
        >
          <CircleDash16 class="shrink-0 text-faint" aria-hidden="true" />
          <span>{{ t('nav.allPools') }}</span>
          <span class="ml-auto text-2xs text-faint">{{ t('meta.poolDefault') }}</span>
        </button>

        <button
          v-for="row in vault.poolRows"
          :key="row.pool.id"
          type="button"
          class="flex w-full items-center gap-2 px-2.5 py-1 text-left text-2xs transition-colors duration-150"
          :class="
            vault.poolFilter === row.pool.id
              ? 'bg-base-300 text-base-content'
              : 'text-dim hover:bg-base-200/60 hover:text-base-content'
          "
          :title="t('drive.showPoolObjects', { pool: row.pool.name })"
          @click="selectPool(row.pool.id)"
        >
          <span class="min-w-0 flex-1 truncate">{{ row.pool.name }}</span>
          <span class="shrink-0 text-2xs tabular-nums text-faint">
            {{ formatBytes(row.bytes) }}
          </span>
        </button>

        <p v-if="vault.poolRows.length === 0 && !vault.volumeLoading" class="px-2.5 py-1 text-2xs text-faint">
          {{ t('storage.noPools') }}
        </p>
      </div>
    </nav>

    <div class="border-t border-line p-2.5">
      <RouterLink
        to="/storage"
        class="flex h-9 items-center gap-2 px-2.5 text-xs transition-colors duration-150"
        :class="
          storageCurrent
            ? 'bg-base-300 text-base-content'
            : 'text-dim hover:bg-base-200/60 hover:text-base-content'
        "
        :aria-current="storageCurrent ? 'page' : undefined"
        @click="navigate"
      >
        <StoragePool16
          class="shrink-0"
          :class="storageCurrent ? 'text-primary' : 'text-faint'"
          aria-hidden="true"
        />
        <span>{{ t('nav.storage') }}</span>
        <span class="ml-auto text-2xs tabular-nums text-faint">{{ usedPercent }}%</span>
      </RouterLink>

      <div class="px-2.5 pb-1 pt-2.5">
        <div class="h-[2px] w-full overflow-hidden bg-line-soft">
          <div class="h-full bg-primary transition-[width] duration-700 ease-out" :style="{ width: usedWidth }" />
        </div>
        <div class="mt-1.5 flex items-center justify-between text-2xs text-faint">
          <span>{{ formatBytes(vault.usedBytes) }} {{ t('storage.used') }}</span>
          <span>{{ formatBytes(vault.remainingBytes) }} {{ t('storage.remaining') }}</span>
        </div>
      </div>
    </div>
  </aside>
</template>
