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
const viewKey = computed(() => `${route.fullPath}:${vault.viewMode}`)
const pageTitle = computed(() => {
  const last = vault.crumbs[vault.crumbs.length - 1]
  if (last?.name) return last.name
  return typeof route.meta.title === 'string' ? route.meta.title : 'My Drive'
})

const emptyState = computed(() => {
  if (vault.query.trim()) {
    return {
      title: 'No matches',
      body: `Nothing in this location matches “${vault.query.trim()}”. Try a shorter search or clear it.`,
      icon: Search16,
    }
  }
  if (vault.poolFilter) {
    return {
      title: 'No files in this pool',
      body: 'Nothing in this location is stored on the selected pool. Clear the filter to see everything again.',
      icon: FolderOpen16,
    }
  }
  if (mode.value === 'recycle') {
    return {
      title: 'Recycle Bin is empty',
      body: 'Files you recycle wait here until they are permanently deleted.',
      icon: TrashCan16,
    }
  }
  if (mode.value === 'unindexed') {
    return {
      title: 'Everything is indexed',
      body: 'Every file in this account has a place in the drive index.',
      icon: WarningAlt16,
    }
  }
  return {
    title: 'This folder is empty',
    body: 'Drop files anywhere in this pane, or create a folder to give them a home.',
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
  window.dispatchEvent(new Event('snfs:upload'))
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
    class="relative flex min-h-0 flex-1 flex-col bg-ink"
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
        class="grid flex-1 auto-rows-min grid-cols-2 gap-2.5 overflow-hidden p-3 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6 md:p-5"
      >
        <div v-for="row in 8" :key="row" class="rounded-lg bg-base-200/40 p-1.5">
          <div class="aspect-[4/3] rounded-lg bg-base-300/70 pulse-soft" />
          <div class="space-y-2 px-1 pb-1 pt-2.5">
            <div class="h-2.5 w-3/4 rounded bg-base-300/70" />
            <div class="h-2 w-1/2 rounded bg-base-300/50" />
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
        class="absolute inset-0 grid place-items-center overflow-y-auto bg-ink px-5"
      >
        <div class="w-full max-w-[380px] text-center">
          <WarningAlt16 class="mx-auto h-6 w-6 text-error" />
          <h2 class="title mt-3 text-[18px] text-base-content">Couldn’t load this folder.</h2>
          <p class="voice mt-1.5">{{ vault.error }}</p>
          <button class="btn btn-primary btn-sm mt-5" type="button" @click="retry">
            Try again
          </button>
        </div>
      </div>

      <div
        v-else-if="vault.isEmpty && !vault.loading"
        class="rise flex flex-1 flex-col items-center justify-center overflow-y-auto px-6 py-10 text-center"
      >
        <component :is="emptyState.icon" class="h-6 w-6 text-faint" aria-hidden="true" />
        <h2 class="title mt-3.5 text-[18px] text-base-content">{{ emptyState.title }}</h2>
        <p class="voice mt-1.5 max-w-[380px] text-balance">{{ emptyState.body }}</p>

        <div class="mt-5 flex flex-wrap items-center justify-center gap-2">
          <template v-if="vault.query.trim()">
            <button class="btn btn-outline btn-sm" type="button" @click="vault.setQuery('')">
              Clear search
            </button>
          </template>
          <template v-else-if="vault.poolFilter">
            <button class="btn btn-outline btn-sm" type="button" @click="clearPool">
              Clear pool filter
            </button>
          </template>
          <template v-else-if="onDrive">
            <button class="btn btn-outline btn-sm" type="button" @click="requestUpload">
              <Upload16 />
              Upload files
            </button>
            <button
              class="btn btn-outline btn-sm"
              type="button"
              @click="ui.openDialog({ kind: 'new-folder' })"
            >
              <FolderAdd16 />
              New folder
            </button>
          </template>
        </div>
      </div>
    </div>

    <footer
      v-if="vault.items.length > 0 && vault.hasMore"
      class="flex h-10 shrink-0 items-center justify-center bg-ink px-4"
    >
      <button
        class="btn btn-ghost btn-sm"
        type="button"
        :disabled="vault.loadingMore"
        @click="vault.loadMore()"
      >
        {{ vault.loadingMore ? 'Loading…' : 'Load more' }}
        <ArrowDown16 />
      </button>
    </footer>

    <Transition
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="opacity-0"
      leave-active-class="transition duration-100 ease-in"
      leave-to-class="opacity-0"
    >
      <div
        v-if="dragging"
        class="pointer-events-none absolute inset-0 z-20 grid place-items-center bg-ink/75 p-6 backdrop-blur-sm"
      >
        <div
          class="flex flex-col items-center gap-2.5 rounded-2xl border border-dashed border-primary/40 bg-base-200/60 px-10 py-8 text-center"
        >
          <Upload16 class="h-6 w-6 text-primary" />
          <div class="title text-[16px] text-base-content">Drop to upload</div>
          <div class="voice text-[12px]">
            Files go {{ vault.folder ? `to ${vault.folder.name}` : 'to My Drive' }}.
          </div>
        </div>
      </div>
    </Transition>
  </div>
</template>
