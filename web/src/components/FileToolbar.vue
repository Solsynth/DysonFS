<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowUp16,
  Checkmark16,
  Close16,
  FolderAdd16,
  Grid16,
  List16,
  Move16,
  Renew16,
  Search16,
  TrashCan16,
  Undo16,
  Upload16,
} from '@carbon/icons-vue'

import type { FileOrder, KindFilter } from '@/lib/api/types'
import { formatBytes, formatCount } from '@/lib/format'
import IconButton from './IconButton.vue'
import { useUiStore } from '@/stores/ui'
import { useVaultStore, type BrowsingMode } from '@/stores/vault'

const vault = useVaultStore()
const ui = useUiStore()
const route = useRoute()
const router = useRouter()

const search = ref(vault.query)
const mode = computed<BrowsingMode>(() => (route.meta.mode as BrowsingMode) ?? 'drive')
const onDrive = computed(() => mode.value === 'drive')

const kinds: { key: KindFilter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'folders', label: 'Folders' },
  { key: 'files', label: 'Files' },
]

const sorts: { key: FileOrder; label: string }[] = [
  { key: 'date', label: 'Date' },
  { key: 'name', label: 'Name' },
  { key: 'size', label: 'Size' },
]

const hasSelection = computed(() => vault.selectedItems.length > 0)
const selectionCount = computed(() => vault.selectedItems.length)

watch(
  () => vault.query,
  (value) => {
    if (value !== search.value) search.value = value
  },
)

function onSearchInput() {
  vault.setQuery(search.value)
}

function clearSearch() {
  search.value = ''
  vault.setQuery('')
}

function onSearchKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && search.value) {
    event.stopPropagation()
    clearSearch()
  }
}

function up() {
  if (!onDrive.value) return
  const parent = vault.folder?.parent_id ?? null
  if (parent) void router.push({ name: 'folder', params: { id: parent } })
  else void router.push({ name: 'drive' })
}

function goCrumb(id: string | null) {
  if (id) void router.push({ name: 'folder', params: { id } })
  else void router.push({ name: 'drive' })
}

function sortBy(key: FileOrder) {
  vault.setOrder(key)
}

function newFolder() {
  ui.openDialog({ kind: 'new-folder' })
}

function requestUpload() {
  window.dispatchEvent(new Event('dyson:upload'))
}

function refresh() {
  void vault.refreshAll()
}

function moveSelection() {
  ui.openDialog({ kind: 'move', files: [...vault.selectedItems] })
}

function recycleSelection() {
  ui.openDialog({ kind: 'recycle', files: [...vault.selectedItems] })
}

function purgeSelection() {
  ui.openDialog({ kind: 'purge', files: [...vault.selectedItems] })
}

function restoreSelection() {
  void vault.restore([...vault.selectedItems]).catch(() => undefined)
}
</script>

<template>
  <div class="shrink-0 border-b border-line bg-base-100">
    <div class="flex flex-wrap items-center gap-x-2 gap-y-1.5 px-3 py-2 md:px-4">
      <IconButton v-if="onDrive && vault.folder" label="Go up" @click="up">
        <ArrowUp16 />
      </IconButton>

      <nav class="flex min-w-0 items-center gap-1 overflow-hidden text-2xs" aria-label="Breadcrumb">
        <template v-for="(crumb, index) in vault.crumbs" :key="crumb.id ?? 'root'">
          <span v-if="index > 0" class="shrink-0 text-line" aria-hidden="true">/</span>
          <button
            v-if="index < vault.crumbs.length - 1"
            type="button"
            class="max-w-[160px] shrink-0 truncate px-0.5 py-0.5 text-dim transition-colors hover:text-base-content"
            @click="goCrumb(crumb.id)"
          >
            {{ crumb.name }}
          </button>
          <span
            v-else
            class="max-w-[220px] truncate px-0.5 py-0.5 font-medium text-base-content sm:max-w-[320px]"
            aria-current="page"
          >
            {{ crumb.name }}
          </span>
        </template>
      </nav>

      <span
        v-if="vault.activePool"
        class="chip ml-1 border-brass/30 text-brass"
        :title="`Filtered to ${vault.activePool.name}`"
      >
        <span class="dot bg-brass" aria-hidden="true" />
        {{ vault.activePool.name }}
        <button
          type="button"
          class="-my-1 -mr-1 ml-0.5 grid size-6 place-items-center text-brass/70 transition-colors hover:text-brass"
          aria-label="Clear pool filter"
          title="Clear pool filter"
          @click="vault.setPoolFilter(null)"
        >
          <Close16 class="h-3 w-3" />
        </button>
      </span>

      <div class="ml-auto flex items-center gap-1">
        <IconButton
          :label="vault.loading ? 'Refreshing listing' : 'Refresh listing'"
          :disabled="vault.loading"
          @click="refresh"
        >
          <Renew16 :class="vault.loading ? 'animate-spin' : ''" />
        </IconButton>
        <template v-if="onDrive">
          <button class="btn btn-outline btn-sm" type="button" @click="newFolder">
            <FolderAdd16 class="mr-1.5" />
            <span class="hidden sm:inline">New folder</span>
          </button>
          <button class="btn btn-outline btn-sm" type="button" @click="requestUpload">
            <Upload16 class="mr-1.5" />
            <span class="hidden sm:inline">Upload</span>
          </button>
        </template>
        <button
          v-else-if="mode === 'recycle' && vault.total > 0"
          class="btn btn-outline btn-sm"
          type="button"
          @click="vault.selectAll()"
        >
          <Checkmark16 class="mr-1.5" />
          Select all
        </button>
      </div>
    </div>

    <div
      class="flex flex-wrap items-center gap-2 border-t border-line-soft px-3 py-2 md:px-4"
      :class="hasSelection ? 'bg-primary/[0.07]' : ''"
    >
      <template v-if="hasSelection">
        <span class="flex items-center gap-1.5 text-2xs text-base-content">
          <Checkmark16 class="text-primary" aria-hidden="true" />
          {{ formatCount(selectionCount) }} selected
          <span class="text-faint">· {{ formatBytes(vault.selectedBytes) }}</span>
        </span>

        <div class="ml-auto flex flex-wrap items-center gap-1">
          <template v-if="mode === 'recycle'">
            <button class="btn btn-outline btn-sm" type="button" @click="restoreSelection">
              <Undo16 class="mr-1.5" />
              Restore
            </button>
            <button class="btn btn-error btn-sm" type="button" @click="purgeSelection">
              <TrashCan16 class="mr-1.5" />
              Delete forever
            </button>
          </template>
          <template v-else>
            <button class="btn btn-outline btn-sm" type="button" @click="moveSelection">
              <Move16 class="mr-1.5" />
              Move
            </button>
            <button class="btn btn-outline btn-sm" type="button" @click="recycleSelection">
              <TrashCan16 class="mr-1.5" />
              Recycle
            </button>
          </template>
          <IconButton label="Clear selection" @click="vault.clearSelection()">
            <Close16 />
          </IconButton>
        </div>
      </template>

      <template v-else>
        <label class="relative flex h-8 w-full max-w-[340px] items-center">
          <span class="sr-only">Search this location</span>
          <Search16 class="pointer-events-none absolute left-2.5 text-faint" aria-hidden="true" />
          <input
            v-model="search"
            type="text"
            class="input h-8 pl-8 pr-8"
            :placeholder="mode === 'recycle' ? 'Search recycled objects' : 'Search this location'"
            autocomplete="off"
            spellcheck="false"
            @input="onSearchInput"
            @keydown="onSearchKeydown"
          />
          <button
            v-if="search"
            type="button"
            class="absolute right-1.5 grid size-5 place-items-center text-faint transition-colors hover:text-base-content"
            aria-label="Clear search"
            title="Clear search"
            @click="clearSearch"
          >
            <Close16 class="h-3.5 w-3.5" />
          </button>
        </label>

        <div class="flex items-center border border-line" role="group" aria-label="Filter by kind">
          <button
            v-for="entry in kinds"
            :key="entry.key"
            type="button"
            class="h-8 px-2.5 text-2xs uppercase tracking-[0.08em] transition-colors"
            :class="
              vault.kind === entry.key
                ? 'bg-base-300 text-base-content'
                : 'text-faint hover:bg-base-200/60 hover:text-dim'
            "
            :aria-pressed="vault.kind === entry.key"
            @click="vault.setKind(entry.key)"
          >
            {{ entry.label }}
          </button>
        </div>

        <div class="ml-auto flex items-center gap-2">
          <div class="hidden items-center gap-1.5 sm:flex" role="group" aria-label="Sort order">
            <span class="eyebrow hidden md:inline">sort</span>
            <button
              v-for="entry in sorts"
              :key="entry.key"
              type="button"
              class="flex h-8 items-center gap-1 px-2 text-2xs uppercase tracking-[0.08em] transition-colors"
              :class="vault.order === entry.key ? 'text-base-content' : 'text-faint hover:text-dim'"
              :aria-pressed="vault.order === entry.key"
              @click="sortBy(entry.key)"
            >
              {{ entry.label }}
              <span v-if="vault.order === entry.key" class="text-primary" aria-hidden="true">
                {{ vault.orderDesc ? '↓' : '↑' }}
              </span>
            </button>
          </div>

          <div class="flex items-center border border-line" role="group" aria-label="View mode">
            <button
              type="button"
              class="grid h-8 w-8 place-items-center transition-colors"
              :class="
                vault.viewMode === 'list'
                  ? 'bg-base-300 text-base-content'
                  : 'text-faint hover:text-dim'
              "
              :aria-pressed="vault.viewMode === 'list'"
              aria-label="List view"
              title="List view"
              @click="vault.storeView('list')"
            >
              <List16 />
            </button>
            <button
              type="button"
              class="grid h-8 w-8 place-items-center transition-colors"
              :class="
                vault.viewMode === 'grid'
                  ? 'bg-base-300 text-base-content'
                  : 'text-faint hover:text-dim'
              "
              :aria-pressed="vault.viewMode === 'grid'"
              aria-label="Grid view"
              title="Grid view"
              @click="vault.storeView('grid')"
            >
              <Grid16 />
            </button>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
