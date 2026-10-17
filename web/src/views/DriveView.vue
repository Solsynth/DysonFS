<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import {
  ArrowDown16,
  FolderAdd16,
  FolderOpen16,
  Search16,
  TrashCan16,
  Upload16,
  WarningAlt16,
} from '@carbon/icons-vue'

import FileGrid from '@/components/FileGrid.vue'
import FileTable from '@/components/FileTable.vue'
import FileToolbar from '@/components/FileToolbar.vue'
import { formatCount, plural } from '@/lib/format'
import { useUiStore } from '@/stores/ui'
import { useUploadStore } from '@/stores/upload'
import { useVaultStore, type BrowsingMode } from '@/stores/vault'

const vault = useVaultStore()
const uploads = useUploadStore()
const ui = useUiStore()
const route = useRoute()

const dragging = ref(false)
let dragDepth = 0

const mode = computed<BrowsingMode>(() => (route.meta.mode as BrowsingMode) ?? 'drive')
const folderParam = computed(() => (typeof route.params.id === 'string' ? route.params.id : null))
const onDrive = computed(() => mode.value === 'drive')
const folderCount = computed(() => vault.items.filter((file) => file.is_folder).length)
const viewKey = computed(() => `${route.fullPath}:${vault.viewMode}`)
const pageTitle = computed(() => {
  const last = vault.crumbs[vault.crumbs.length - 1]
  if (last?.name) return last.name
  return typeof route.meta.title === 'string' ? route.meta.title : 'My Drive'
})

const emptyState = computed(() => {
  if (vault.query.trim()) {
    return {
      eyebrow: 'query / no match',
      title: 'No match.',
      body: `Nothing in this location matches “${vault.query.trim()}”. Try a shorter term or clear the query.`,
      icon: Search16,
    }
  }
  if (vault.poolFilter) {
    return {
      eyebrow: 'pool / empty',
      title: 'This pool is quiet.',
      body: 'No objects in this location are stored on the selected pool. Clear the pool filter to see everything.',
      icon: FolderOpen16,
    }
  }
  if (mode.value === 'recycle') {
    return {
      eyebrow: 'recycle / clear',
      title: 'The bin is clear.',
      body: 'Recycled objects wait here until purged. Nothing has been discarded from this account.',
      icon: TrashCan16,
    }
  }
  if (mode.value === 'unindexed') {
    return {
      eyebrow: 'index / complete',
      title: 'Nothing unindexed.',
      body: 'Every object owned by this account has a place in the drive index. Uploads land here first if indexing is skipped.',
      icon: WarningAlt16,
    }
  }
  return {
    eyebrow: 'folder / empty',
    title: 'Empty folder.',
    body: 'Drop files anywhere in this pane, or create a folder to give the next payload a home.',
    icon: FolderOpen16,
  }
})

watch(
  [mode, folderParam],
  ([nextMode, nextFolder]) => {
    void vault.open(nextMode, nextFolder)
  },
  { immediate: true },
)

function requestUpload() {
  window.dispatchEvent(new Event('dyson:upload'))
}

function onDragEnter(event: DragEvent) {
  if (!onDrive.value || !event.dataTransfer?.types.includes('Files')) return
  event.preventDefault()
  dragDepth += 1
  dragging.value = true
}

function onDragOver(event: DragEvent) {
  if (!onDrive.value || !event.dataTransfer?.types.includes('Files')) return
  event.preventDefault()
  event.dataTransfer.dropEffect = 'copy'
}

function onDragLeave() {
  dragDepth = Math.max(0, dragDepth - 1)
  if (dragDepth === 0) dragging.value = false
}

function onDrop(event: DragEvent) {
  dragDepth = 0
  dragging.value = false
  if (!onDrive.value) return
  const list = Array.from(event.dataTransfer?.files ?? [])
  if (list.length === 0) return
  uploads.enqueue(list, vault.folderId)
}

function clearPool() {
  vault.setPoolFilter(null)
}

function retry() {
  void vault.load()
}
</script>

<template>
  <div
    class="relative flex min-h-0 flex-1 flex-col bg-base-100"
    @dragenter="onDragEnter"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop.prevent="onDrop"
  >
    <FileToolbar />

    <h1 class="sr-only">{{ pageTitle }}</h1>

    <div class="relative flex min-h-0 flex-1 flex-col">
      <div
        v-if="vault.loading && vault.items.length === 0 && vault.viewMode === 'grid'"
        class="grid flex-1 auto-rows-min grid-cols-2 gap-2.5 overflow-hidden p-3 sm:grid-cols-3 xl:grid-cols-4 2xl:grid-cols-5 md:p-4"
      >
        <div v-for="row in 8" :key="row" class="border border-line bg-base-200/25">
          <div class="aspect-[4/3] bg-base-300/60 pulse-soft" />
          <div class="space-y-2 p-2.5">
            <div class="h-2.5 w-3/4 bg-base-300/70" />
            <div class="h-2 w-1/2 bg-base-300/50" />
          </div>
        </div>
      </div>

      <FileGrid v-else-if="vault.viewMode === 'grid' && vault.items.length > 0" :key="viewKey" />
      <FileTable
        v-else-if="vault.viewMode === 'list' && (vault.items.length > 0 || vault.loading)"
        :key="viewKey"
      />

      <div
        v-if="vault.error && !vault.loading"
        class="absolute inset-0 grid place-items-center overflow-y-auto bg-base-100 px-5"
      >
        <div class="w-full max-w-[420px] border border-line bg-base-200/30 p-5 text-center">
          <WarningAlt16 class="mx-auto h-6 w-6 text-error" />
          <div class="eyebrow mt-3 text-error/80">link / read failure</div>
          <h2 class="title mt-1.5 text-lg text-base-content">The listing did not resolve.</h2>
          <p class="voice mt-1.5">{{ vault.error }}</p>
          <button class="btn btn-primary btn-sm mt-4" type="button" @click="retry">
            Retry listing
          </button>
        </div>
      </div>

      <div
        v-else-if="vault.isEmpty && !vault.loading"
        class="flex flex-1 flex-col items-center justify-center overflow-y-auto px-6 py-10 text-center"
      >
        <div class="relative grid size-14 place-items-center border border-line bg-base-200/30">
          <div class="absolute inset-1 border border-line-soft" aria-hidden="true" />
          <component :is="emptyState.icon" class="relative h-5 w-5 text-faint" />
        </div>
        <div class="eyebrow mt-4">{{ emptyState.eyebrow }}</div>
        <h2 class="title mt-1.5 text-[22px] text-base-content">{{ emptyState.title }}</h2>
        <p class="voice mt-2 max-w-[420px] text-balance">{{ emptyState.body }}</p>

        <div class="mt-5 flex flex-wrap items-center justify-center gap-2">
          <template v-if="vault.query.trim()">
            <button class="btn btn-outline btn-sm" type="button" @click="vault.setQuery('')">
              Clear query
            </button>
          </template>
          <template v-else-if="vault.poolFilter">
            <button class="btn btn-outline btn-sm" type="button" @click="clearPool">
              Clear pool filter
            </button>
          </template>
          <template v-else-if="onDrive">
            <button class="btn btn-outline btn-sm" type="button" @click="requestUpload">
              <Upload16 class="mr-1.5" />
              Upload files
            </button>
            <button
              class="btn btn-outline btn-sm"
              type="button"
              @click="ui.openDialog({ kind: 'new-folder' })"
            >
              <FolderAdd16 class="mr-1.5" />
              New folder
            </button>
          </template>
        </div>
      </div>
    </div>

    <footer
      v-if="vault.items.length > 0"
      class="flex h-9 shrink-0 items-center gap-3 bg-base-100 px-3 text-2xs text-faint md:px-4"
    >
      <span>{{ vault.showingLabel }}</span>
      <span v-if="folderCount > 0" class="hidden text-2xs sm:block">
        · {{ formatCount(folderCount) }} {{ plural(folderCount, 'folder') }}
      </span>
      <div class="ml-auto">
        <button
          v-if="vault.hasMore"
          class="btn btn-ghost btn-xs"
          type="button"
          :disabled="vault.loadingMore"
          @click="vault.loadMore()"
        >
          {{ vault.loadingMore ? 'Reading…' : 'Load more' }}
          <ArrowDown16 class="ml-1" />
        </button>
        <span v-else class="uppercase tracking-[0.12em]">end of listing</span>
      </div>
    </footer>

    <div
      v-if="dragging"
      class="pointer-events-none absolute inset-0 z-20 grid place-items-center bg-base-100/85 p-6 backdrop-blur-[1px]"
    >
      <div
        class="flex flex-col items-center gap-2.5 border border-dashed border-primary/70 bg-primary/[0.06] px-10 py-8 text-center"
      >
        <Upload16 class="h-7 w-7 text-primary" />
        <div class="title text-lg text-base-content">Drop to upload</div>
        <div class="voice">
          Files land
          {{ vault.folder ? `in ${vault.folder.name}` : 'at the root of My Drive' }}.
        </div>
      </div>
    </div>
  </div>
</template>
