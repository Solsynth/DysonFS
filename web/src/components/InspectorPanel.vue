<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Checkmark16,
  Close16,
  CopyLink16,
  Download16,
  Edit16,
  FolderDetails16,
  Link16,
  Move16,
  TrashCan16,
  Undo16,
  View16,
} from '@carbon/icons-vue'

import FileGlyph from './FileGlyph.vue'
import { openUrl, previewUrl } from '@/lib/api/drive'
import type { CloudFile } from '@/lib/api/types'
import {
  displayName,
  fileDimensions,
  fileDuration,
  formatBytes,
  formatDateTime,
  kindLabel,
  mimeOf,
  shortHash,
  shortId,
} from '@/lib/format'
import { hasPreview } from '@/lib/fileKinds'
import { useNoticeStore } from '@/stores/notice'
import { useUiStore } from '@/stores/ui'
import { useVaultStore } from '@/stores/vault'

const vault = useVaultStore()
const ui = useUiStore()
const notice = useNoticeStore()
const route = useRoute()
const router = useRouter()

const previewFailed = ref(false)

const file = computed<CloudFile | null>(() => vault.primarySelected)
const count = computed(() => vault.selectedItems.length)
const multi = computed(() => count.value > 1)
const mode = computed(() => (route.meta.mode as 'drive' | 'unindexed' | 'recycle') ?? 'drive')
const inRecycle = computed(() => mode.value === 'recycle')
const showPreview = computed(
  () => !!file.value && !multi.value && !vault.sample && hasPreview(file.value) && !previewFailed.value,
)
const isPublic = computed(() => file.value?.permission_status?.visibility === 'public')
const dimensions = computed(() => (file.value ? fileDimensions(file.value) : null))
const duration = computed(() => (file.value ? fileDuration(file.value) : null))
const poolName = computed(() => {
  if (!file.value?.pool_id) return 'default'
  return vault.pools.find((pool) => pool.id === file.value?.pool_id)?.name ?? 'default'
})
const publicLink = computed(() => {
  if (!file.value) return ''
  return `${window.location.origin}${openUrl(file.value.id)}`
})
const busy = computed(() => vault.actionBusy === 'visibility')
const permissions = computed(() => file.value?.permission_status ?? null)

watch(
  () => file.value?.id,
  () => {
    previewFailed.value = false
  },
)

function close() {
  vault.closeInspector()
}

function openFile() {
  if (!file.value) return
  if (file.value.is_folder) {
    void router.push({ name: 'folder', params: { id: file.value.id } })
    return
  }
  if (vault.sample) {
    notice.info('Sample objects have no live bytes to open.')
    return
  }
  window.open(openUrl(file.value.id), '_blank', 'noopener')
}

function download() {
  if (!file.value) return
  if (file.value.is_folder) {
    notice.info('Folders download as archives only from the API.')
    return
  }
  if (vault.sample) {
    notice.info('Sample objects have no live bytes to download.')
    return
  }
  window.open(openUrl(file.value.id, true), '_blank', 'noopener')
}

function rename() {
  if (file.value) ui.openDialog({ kind: 'rename', file: file.value })
}

function move() {
  if (vault.selectedItems.length > 0) {
    ui.openDialog({ kind: 'move', files: [...vault.selectedItems] })
  }
}

function recycle() {
  if (vault.selectedItems.length > 0) {
    ui.openDialog({ kind: 'recycle', files: [...vault.selectedItems] })
  }
}

function purge() {
  if (vault.selectedItems.length > 0) {
    ui.openDialog({ kind: 'purge', files: [...vault.selectedItems] })
  }
}

function restore() {
  if (vault.selectedItems.length === 0) return
  void vault.restore([...vault.selectedItems]).catch(() => undefined)
}

async function linkAction() {
  if (!file.value) return
  if (!isPublic.value) {
    ui.openDialog({ kind: 'share', file: file.value })
    return
  }
  if (!publicLink.value) return
  try {
    await navigator.clipboard.writeText(publicLink.value)
    notice.ok('Public link copied.')
  } catch {
    notice.error('The browser refused clipboard access.')
  }
}

async function toggleVisibility() {
  if (!file.value || multi.value) return
  try {
    await vault.setVisibility(file.value, !isPublic.value)
  } catch {
    // The store already raised a notice.
  }
}
</script>

<template>
  <aside
    v-if="file"
    class="fixed bottom-7 right-0 top-12 z-40 flex w-full flex-col border-l border-line bg-ink shadow-[-16px_0_40px_rgba(0,0,0,0.35)] sm:w-[336px] sm:max-w-[92vw] xl:static xl:z-auto xl:h-full xl:shadow-none"
    aria-label="Object inspector"
  >
    <div class="flex h-10 shrink-0 items-center gap-2 border-b border-line px-3">
      <span class="eyebrow">inspector / {{ multi ? 'selection' : 'object' }}</span>
      <span class="ml-auto text-3xs tabular-nums text-faint">
        {{ multi ? `${count} objects` : shortId(file.id) }}
      </span>
      <button
        type="button"
        class="btn btn-ghost btn-xs px-1"
        aria-label="Close inspector"
        @click="close"
      >
        <Close16 />
      </button>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto scroll-region">
      <template v-if="multi">
        <div class="border-b border-line px-4 py-4">
          <div class="eyebrow">selection</div>
          <h2 class="title mt-1 text-xl text-base-content">{{ count }} objects</h2>
          <p class="mt-1 text-2xs text-faint">
            {{ formatBytes(vault.selectedBytes) }} across
            {{ vault.selectedItems.filter((item) => item.is_folder).length }} folders
          </p>
          <div class="mt-3 space-y-1">
            <div
              v-for="item in vault.selectedItems.slice(0, 6)"
              :key="item.id"
              class="flex items-center gap-2 text-2xs text-dim"
            >
              <FolderDetails16 class="h-3.5 w-3.5 shrink-0 text-faint" />
              <span class="truncate">{{ displayName(item) }}</span>
            </div>
            <div v-if="count > 6" class="text-3xs text-faint">and {{ count - 6 }} more</div>
          </div>
        </div>

        <div class="grid grid-cols-3 gap-px border-b border-line bg-line">
          <button class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200" type="button" @click="move">
            <Move16 class="text-faint" />
            Move
          </button>
          <button
            v-if="inRecycle"
            class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200"
            type="button"
            @click="restore"
          >
            <Undo16 class="text-faint" />
            Restore
          </button>
          <button
            v-else
            class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200"
            type="button"
            @click="recycle"
          >
            <TrashCan16 class="text-faint" />
            Recycle
          </button>
          <button
            v-if="inRecycle"
            class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-error transition-colors hover:bg-error/10"
            type="button"
            @click="purge"
          >
            <TrashCan16 />
            Delete
          </button>
          <button
            v-else
            class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200"
            type="button"
            @click="vault.clearSelection()"
          >
            <Close16 class="text-faint" />
            Clear
          </button>
        </div>
      </template>

      <template v-else>
        <div class="relative aspect-[4/3] overflow-hidden border-b border-line bg-base-200/50">
          <img
            v-if="showPreview && file"
            :src="previewUrl(file.id)"
            alt=""
            class="h-full w-full object-contain"
            @error="previewFailed = true"
          />
          <div v-else class="relative grid h-full place-items-center">
            <div class="absolute inset-0 opacity-50 ruler" aria-hidden="true" />
            <div class="relative flex flex-col items-center gap-3">
              <FileGlyph v-if="file" :file="file" size="lg" class="opacity-80" />
              <span class="text-3xs uppercase tracking-[0.16em] text-faint">
                {{ file ? kindLabel(file) : 'object' }}
              </span>
            </div>
          </div>
          <span
            v-if="isPublic"
            class="absolute left-2 top-2 flex items-center gap-1 border border-brass/40 bg-ink/85 px-1.5 py-0.5 text-3xs text-brass"
          >
            <Link16 class="h-3 w-3" />
            public
          </span>
        </div>

        <div class="border-b border-line px-4 py-3.5">
          <h2 class="break-words font-sans text-[15px] font-medium leading-snug text-base-content">
            {{ file ? displayName(file) : '' }}
          </h2>
          <div class="mt-1.5 flex flex-wrap items-center gap-1.5">
            <span class="chip">{{ file ? kindLabel(file) : '—' }}</span>
            <span class="chip">{{ poolName }}</span>
            <span v-if="file?.is_folder" class="chip border-brass/30 text-brass">
              {{ file.children_count ?? 0 }} objects
            </span>
          </div>
        </div>

        <div class="grid grid-cols-3 gap-px border-b border-line bg-line">
          <button class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200" type="button" @click="openFile">
            <View16 class="text-faint" />
            Open
          </button>
          <button class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200" type="button" @click="download">
            <Download16 class="text-faint" />
            Download
          </button>
          <button class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200" type="button" @click="rename">
            <Edit16 class="text-faint" />
            Rename
          </button>
          <button class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200" type="button" @click="move">
            <Move16 class="text-faint" />
            Move
          </button>
          <button
            v-if="inRecycle"
            class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200"
            type="button"
            @click="restore"
          >
            <Undo16 class="text-faint" />
            Restore
          </button>
          <button
            v-else
            class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200"
            type="button"
            @click="recycle"
          >
            <TrashCan16 class="text-faint" />
            Recycle
          </button>
          <button
            v-if="inRecycle"
            class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-error transition-colors hover:bg-error/10"
            type="button"
            @click="purge"
          >
            <TrashCan16 />
            Delete
          </button>
          <button
            v-else
            class="flex flex-col items-center gap-1.5 bg-ink py-3 text-3xs text-dim transition-colors hover:bg-base-200"
            type="button"
            @click="linkAction"
          >
            <CopyLink16 class="text-faint" />
            Copy link
          </button>
        </div>

        <div class="border-b border-line px-4 py-3.5">
          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="eyebrow">access</div>
              <div class="mt-1 text-2xs text-dim">
                {{ isPublic ? 'Anyone with the link can read.' : 'Private to this account.' }}
              </div>
            </div>
            <button
              type="button"
              role="switch"
              :aria-checked="isPublic"
              class="relative h-5 w-9 shrink-0 border transition-colors disabled:opacity-50"
              :class="isPublic ? 'border-brass/60 bg-brass/25' : 'border-line bg-base-300'"
              :disabled="busy"
              :aria-label="isPublic ? 'Unpublish file' : 'Publish file by link'"
              @click="toggleVisibility"
            >
              <span
                class="absolute top-[2px] size-3.5 transition-[left] duration-150"
                :class="isPublic ? 'left-[18px] bg-brass' : 'left-[2px] bg-faint'"
              />
            </button>
          </div>

          <div v-if="isPublic" class="mt-2.5 flex items-stretch border border-line bg-base-200/50">
            <input
              :value="publicLink"
              readonly
              class="min-w-0 flex-1 truncate bg-transparent px-2 py-1.5 font-mono text-3xs text-dim outline-none"
              aria-label="Public link"
              @focus="($event.target as HTMLInputElement).select()"
            />
            <button
              type="button"
              class="flex shrink-0 items-center border-l border-line px-2 text-3xs text-faint transition-colors hover:text-base-content"
              aria-label="Copy public link"
              @click="linkAction"
            >
              <CopyLink16 class="h-3.5 w-3.5" />
            </button>
          </div>
        </div>

        <dl class="divide-y divide-line-soft text-2xs">
          <div class="flex items-start justify-between gap-3 px-4 py-2">
            <dt class="shrink-0 text-faint">Type</dt>
            <dd class="break-all text-right text-dim">{{ file ? mimeOf(file) : '—' }}</dd>
          </div>
          <div class="flex items-center justify-between gap-3 px-4 py-2">
            <dt class="text-faint">Size</dt>
            <dd class="tabular-nums text-dim">
              {{ file?.is_folder ? '—' : formatBytes(file?.object?.size) }}
            </dd>
          </div>
          <div v-if="dimensions" class="flex items-center justify-between gap-3 px-4 py-2">
            <dt class="text-faint">Dimensions</dt>
            <dd class="tabular-nums text-dim">{{ dimensions }}</dd>
          </div>
          <div v-if="duration" class="flex items-center justify-between gap-3 px-4 py-2">
            <dt class="text-faint">Duration</dt>
            <dd class="tabular-nums text-dim">{{ duration }}</dd>
          </div>
          <div class="flex items-center justify-between gap-3 px-4 py-2">
            <dt class="text-faint">Modified</dt>
            <dd class="tabular-nums text-dim">{{ formatDateTime(file?.updated_at) }}</dd>
          </div>
          <div class="flex items-center justify-between gap-3 px-4 py-2">
            <dt class="text-faint">Created</dt>
            <dd class="tabular-nums text-dim">{{ formatDateTime(file?.created_at) }}</dd>
          </div>
          <div class="flex items-center justify-between gap-3 px-4 py-2">
            <dt class="text-faint">Pool</dt>
            <dd class="text-dim">{{ poolName }}</dd>
          </div>
          <div class="flex items-center justify-between gap-3 px-4 py-2">
            <dt class="text-faint">Indexed</dt>
            <dd class="flex items-center gap-1.5 text-dim">
              <Checkmark16 v-if="file?.indexed" class="h-3 w-3 text-success" />
              <Close16 v-else class="h-3 w-3 text-brass" />
              {{ file?.indexed ? 'yes' : 'no' }}
            </dd>
          </div>
          <div class="flex items-center justify-between gap-3 px-4 py-2">
            <dt class="text-faint">Object</dt>
            <dd class="font-mono text-dim">{{ shortId(file?.object_id) }}</dd>
          </div>
          <div class="flex items-center justify-between gap-3 px-4 py-2">
            <dt class="text-faint">Hash</dt>
            <dd class="font-mono text-dim">{{ shortHash(file?.object?.hash) }}</dd>
          </div>
        </dl>

        <div v-if="permissions" class="border-t border-line px-4 py-3.5">
          <div class="eyebrow">capabilities</div>
          <div class="mt-2 flex flex-wrap gap-1.5">
            <span class="chip" :class="permissions.readable ? 'border-success/30 text-success' : 'text-faint'">
              {{ permissions.readable ? 'read' : 'no read' }}
            </span>
            <span class="chip" :class="permissions.writable ? 'border-success/30 text-success' : 'text-faint'">
              {{ permissions.writable ? 'write' : 'no write' }}
            </span>
            <span class="chip" :class="permissions.manageable ? 'border-success/30 text-success' : 'text-faint'">
              {{ permissions.manageable ? 'manage' : 'no manage' }}
            </span>
          </div>
        </div>
      </template>
    </div>

    <div class="flex h-8 shrink-0 items-center justify-between border-t border-line px-3 text-3xs text-faint">
      <span class="uppercase tracking-[0.12em]">{{ vault.sample ? 'sample object' : 'live object' }}</span>
      <span class="font-mono">{{ file ? shortId(file.id) : '—' }}</span>
    </div>
  </aside>
</template>
