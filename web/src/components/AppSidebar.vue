<script setup lang="ts">
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

import VolumePlatter from './VolumePlatter.vue'
import { formatBytes, formatCount, initialOf } from '@/lib/format'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import { useVaultStore } from '@/stores/vault'

const session = useSessionStore()
const ui = useUiStore()
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
    label: 'My Drive',
    to: '/drive',
    icon: Folder16,
    match: '/drive',
    badge: vault.fileCount > 0 ? formatCount(vault.fileCount) : undefined,
  },
  { label: 'Unindexed', to: '/unindexed', icon: WarningAlt16, match: '/unindexed' },
  { label: 'Recycle Bin', to: '/recycle', icon: TrashCan16, match: '/recycle' },
])

const poolColors = ['#c9ae83', '#8184c9', '#8fa6c9', '#8fbaa0', '#c98a7f']
const maxPool = computed(() =>
  Math.max(1, ...vault.poolRows.map((row) => row.bytes)),
)

const operator = computed(() => session.identity?.nick || session.identity?.name || 'operator')
const operatorRole = computed(() =>
  session.identity?.is_superuser ? 'superuser' : 'authenticated',
)

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

function barWidth(bytes: number): string {
  return `${Math.max(2, (bytes / maxPool.value) * 100)}%`
}
</script>

<template>
  <Transition
    enter-active-class="transition-opacity duration-150"
    leave-active-class="transition-opacity duration-150"
    enter-from-class="opacity-0"
    leave-to-class="opacity-0"
  >
    <button
      v-if="ui.sidebarOpen"
      type="button"
      class="fixed inset-0 z-40 bg-black/65 lg:hidden"
      aria-label="Close navigation"
      @click="ui.sidebarOpen = false"
    />
  </Transition>

  <aside
    class="fixed bottom-7 left-0 top-12 z-40 flex w-[250px] shrink-0 flex-col border-r border-line bg-ink transition-transform duration-200 lg:static lg:z-auto lg:h-full lg:translate-x-0"
    :class="ui.sidebarOpen ? 'translate-x-0' : '-translate-x-full'"
    aria-label="Drive navigation"
  >
    <div class="border-b border-line px-3.5 pb-3 pt-4">
      <div class="mb-2 flex items-center justify-between">
        <span class="eyebrow">capacity / volume a</span>
        <RouterLink
          to="/storage"
          class="text-3xs uppercase tracking-[0.12em] text-faint transition-colors hover:text-dim"
          @click="navigate"
        >
          details
        </RouterLink>
      </div>
      <RouterLink to="/storage" class="block" aria-label="Open storage details" @click="navigate">
        <VolumePlatter size="sm" :legend="false" caption="volume a" />
      </RouterLink>
      <div class="mt-2.5 flex items-center justify-between border-t border-line-soft pt-2">
        <span class="text-3xs text-faint">{{ formatBytes(vault.usedBytes) }} used</span>
        <span class="text-3xs text-dim">{{ formatBytes(vault.remainingBytes) }} free</span>
      </div>
    </div>

    <nav class="flex-1 overflow-y-auto px-2 py-3" aria-label="Locations">
      <div class="px-1.5 pb-1.5">
        <span class="eyebrow">locations</span>
      </div>
      <RouterLink
        v-for="item in nav"
        :key="item.to"
        :to="item.to"
        class="group flex items-center gap-2 px-2 py-[7px] text-xs transition-colors"
        :class="
          route.path === item.to || (item.match === '/drive' && route.path.startsWith('/drive'))
            ? 'bg-primary/10 text-base-content'
            : 'text-dim hover:bg-base-200/60 hover:text-base-content'
        "
        @click="navigate"
      >
        <span
          class="h-4 w-px shrink-0"
          :class="
            route.path === item.to || (item.match === '/drive' && route.path.startsWith('/drive'))
              ? 'bg-primary'
              : 'bg-transparent'
          "
          aria-hidden="true"
        />
        <component :is="item.icon" class="shrink-0 text-faint" aria-hidden="true" />
        <span class="truncate">{{ item.label }}</span>
        <span v-if="item.badge" class="ml-auto text-3xs tabular-nums text-faint">
          {{ item.badge }}
        </span>
      </RouterLink>

      <div class="mt-5 flex items-center justify-between px-1.5 pb-1.5">
        <span class="eyebrow">pools</span>
        <span class="text-3xs text-faint">{{ vault.pools.length }}</span>
      </div>

      <button
        type="button"
        class="group flex w-full items-center gap-2 px-2 py-[6px] text-left text-xs transition-colors"
        :class="
          vault.poolFilter === null
            ? 'text-base-content'
            : 'text-dim hover:bg-base-200/60 hover:text-base-content'
        "
        @click="selectPool(null)"
      >
        <CircleDash16 class="shrink-0 text-faint" aria-hidden="true" />
        <span>All pools</span>
        <span class="ml-auto text-3xs text-faint">default</span>
      </button>

      <button
        v-for="(row, index) in vault.poolRows"
        :key="row.pool.id"
        type="button"
        class="group w-full px-2 py-1.5 text-left transition-colors hover:bg-base-200/50"
        :class="vault.poolFilter === row.pool.id ? 'bg-base-200/70' : ''"
        :title="`Filter by ${row.pool.name}`"
        @click="selectPool(row.pool.id)"
      >
        <div class="flex items-center gap-2 text-xs">
          <span
            class="dot"
            :style="{ backgroundColor: poolColors[index % poolColors.length] }"
            aria-hidden="true"
          />
          <span
            class="truncate"
            :class="vault.poolFilter === row.pool.id ? 'text-base-content' : 'text-dim'"
          >
            {{ row.pool.name }}
          </span>
          <span class="ml-auto shrink-0 text-3xs tabular-nums text-faint">
            {{ formatBytes(row.bytes) }}
          </span>
        </div>
        <div class="mt-1.5 ml-3.5 h-[2px] bg-line-soft">
          <div
            class="h-full transition-[width] duration-500"
            :style="{
              width: barWidth(row.bytes),
              backgroundColor: poolColors[index % poolColors.length],
              opacity: 0.75,
            }"
          />
        </div>
      </button>

      <p v-if="vault.poolRows.length === 0 && !vault.volumeLoading" class="px-2 py-1 text-3xs text-faint">
        No visible pools.
      </p>

      <div class="mt-5 px-1.5 pb-1.5">
        <span class="eyebrow">system</span>
      </div>
      <RouterLink
        to="/storage"
        class="group flex items-center gap-2 px-2 py-[7px] text-xs transition-colors"
        :class="
          route.path === '/storage'
            ? 'bg-primary/10 text-base-content'
            : 'text-dim hover:bg-base-200/60 hover:text-base-content'
        "
        @click="navigate"
      >
        <span
          class="h-4 w-px shrink-0"
          :class="route.path === '/storage' ? 'bg-primary' : 'bg-transparent'"
          aria-hidden="true"
        />
        <StoragePool16 class="shrink-0 text-faint" aria-hidden="true" />
        <span>Storage</span>
      </RouterLink>
    </nav>

    <div class="mt-auto border-t border-line px-3.5 py-3">
      <div class="flex items-center gap-2.5">
        <span
          class="grid size-7 shrink-0 place-items-center border border-line bg-base-300 text-2xs font-semibold text-dim"
          aria-hidden="true"
        >
          {{ initialOf(operator) }}
        </span>
        <div class="min-w-0">
          <div class="truncate text-2xs text-base-content">{{ operator }}</div>
          <div class="text-3xs uppercase tracking-[0.12em] text-faint">{{ operatorRole }}</div>
        </div>
        <span class="ml-auto dot bg-success pulse-soft" title="Session active" aria-hidden="true" />
      </div>
      <div class="mt-2 flex items-center justify-between gap-2 border-t border-line-soft pt-2">
        <span class="text-3xs text-faint">{{ formatCount(vault.fileCount) }} objects indexed</span>
        <span v-if="session.sample" class="text-3xs uppercase tracking-[0.1em] text-brass">
          sample
        </span>
        <span v-else class="text-3xs uppercase tracking-[0.1em] text-faint">live</span>
      </div>
    </div>
  </aside>
</template>
