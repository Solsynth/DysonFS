<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Checkmark16,
  Close16,
  Download16,
  Edit16,
  FolderDetails16,
  Link16,
  Move16,
  TrashCan16,
  Undo16,
  View16,
} from '@carbon/icons-vue'

import CopyButton from './CopyButton.vue'
import FileGlyph from './FileGlyph.vue'
import IconButton from './IconButton.vue'
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
  () =>
    !!file.value && !multi.value && !vault.sample && hasPreview(file.value) && !previewFailed.value,
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
    notice.info('Sample objects have no file to open.')
    return
  }
  window.open(openUrl(file.value.id), '_blank', 'noopener')
}

function download() {
  if (!file.value) return
  if (file.value.is_folder) {
    notice.info('Folders download as a single archive, not a file.')
    return
  }
  if (vault.sample) {
    notice.info('Sample objects cannot be downloaded.')
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
  <Transition
    enter-active-class="transition duration-200 ease-[cubic-bezier(0.22,1,0.36,1)]"
    enter-from-class="opacity-0 translate-x-3"
    leave-active-class="transition duration-150 ease-in"
    leave-to-class="opacity-0 translate-x-2"
  >
    <aside
      v-if="file"
      class="fixed bottom-8 right-0 top-[52px] z-40 flex w-full flex-col bg-base-200/40 sm:w-[336px] sm:max-w-[92vw] xl:static xl:z-auto xl:h-full"
      aria-label="Object inspector"
    >
      <div class="flex h-11 shrink-0 items-center gap-2 border-b border-line-soft px-4">
        <span class="text-[11px] font-medium text-faint">Inspector</span>
        <span class="ml-auto text-[11px] tabular-nums text-faint">
          {{ multi ? `${count} objects` : shortId(file.id) }}
        </span>
        <IconButton label="Close inspector" @click="close">
          <Close16 />
        </IconButton>
      </div>

      <div class="min-h-0 flex-1 overflow-y-auto scroll-region">
        <template v-if="multi">
          <div class="border-b border-line-soft px-4 py-4">
            <h2 class="title text-[18px] text-base-content">{{ count }} objects</h2>
            <p class="mt-1 text-[11px] text-faint">
              {{ formatBytes(vault.selectedBytes) }} across
              {{ vault.selectedItems.filter((item) => item.is_folder).length }} folders
            </p>
            <div class="mt-3 space-y-1.5">
              <div
                v-for="item in vault.selectedItems.slice(0, 6)"
                :key="item.id"
                class="flex items-center gap-2 text-[12px] text-dim"
              >
                <FolderDetails16 class="h-3.5 w-3.5 shrink-0 text-faint" />
                <span class="truncate">{{ displayName(item) }}</span>
              </div>
              <div v-if="count > 6" class="text-[11px] text-faint">
                and {{ count - 6 }} more
              </div>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-1 px-3 py-2.5">
            <button
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="move"
            >
              <Move16 class="text-faint" />
              Move
            </button>
            <button
              v-if="inRecycle"
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="restore"
            >
              <Undo16 class="text-faint" />
              Restore
            </button>
            <button
              v-else
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="recycle"
            >
              <TrashCan16 class="text-faint" />
              Recycle
            </button>
            <button
              v-if="inRecycle"
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-error transition-colors hover:bg-error/10"
              type="button"
              @click="purge"
            >
              <TrashCan16 />
              Delete forever
            </button>
            <button
              v-else
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="vault.clearSelection()"
            >
              <Close16 class="text-faint" />
              Clear
            </button>
          </div>
        </template>

        <template v-else>
          <div class="relative aspect-[4/3] overflow-hidden border-b border-line-soft bg-base-200/50">
            <img
              v-if="showPreview && file"
              :src="previewUrl(file.id)"
              alt=""
              class="h-full w-full object-contain"
              @error="previewFailed = true"
            />
            <div v-else class="grid h-full place-items-center">
              <div class="flex flex-col items-center gap-2">
                <FileGlyph v-if="file" :file="file" size="lg" class="opacity-70" />
                <span class="text-[11px] text-faint">
                  {{ file ? kindLabel(file) : 'object' }}
                </span>
              </div>
            </div>
            <span
              v-if="isPublic"
              class="absolute left-2 top-2 flex items-center gap-1 rounded-full bg-ink/85 px-2 py-0.5 text-[11px] text-primary"
            >
              <Link16 class="h-3 w-3" />
              Public
            </span>
          </div>

          <div class="border-b border-line-soft px-4 py-4">
            <h2 class="break-words text-[15px] font-medium leading-snug text-base-content">
              {{ file ? displayName(file) : '' }}
            </h2>
            <div class="mt-1.5 flex flex-wrap items-center gap-x-1.5 text-[11px] text-faint">
              <span>{{ file ? kindLabel(file) : '—' }}</span>
              <span aria-hidden="true">·</span>
              <span>{{ poolName }}</span>
              <template v-if="file?.is_folder">
                <span aria-hidden="true">·</span>
                <span>{{ file.children_count ?? 0 }} items</span>
              </template>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-1 px-3 py-3">
            <button
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="openFile"
            >
              <View16 class="text-faint" />
              Open
            </button>
            <button
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="download"
            >
              <Download16 class="text-faint" />
              Download
            </button>
            <button
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="rename"
            >
              <Edit16 class="text-faint" />
              Rename
            </button>
            <button
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="move"
            >
              <Move16 class="text-faint" />
              Move
            </button>
            <button
              v-if="inRecycle"
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="restore"
            >
              <Undo16 class="text-faint" />
              Restore
            </button>
            <button
              v-else
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="recycle"
            >
              <TrashCan16 class="text-faint" />
              Recycle
            </button>
            <button
              v-if="inRecycle"
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-error transition-colors hover:bg-error/10"
              type="button"
              @click="purge"
            >
              <TrashCan16 />
              Delete forever
            </button>
            <button
              v-else
              class="flex items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-dim transition-colors hover:bg-base-200 hover:text-base-content"
              type="button"
              @click="linkAction"
            >
              <Link16 class="text-faint" />
              Copy link
            </button>
          </div>

          <div class="border-b border-line-soft px-4 py-4">
            <div class="flex items-start justify-between gap-3">
              <div>
                <div class="text-[12px] text-base-content">Public link</div>
                <div class="mt-0.5 text-[11px] text-faint">
                  {{ isPublic ? 'Anyone with the link can read.' : 'Private to this account.' }}
                </div>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="isPublic"
                class="relative mt-0.5 h-5 w-[34px] shrink-0 rounded-full transition-colors duration-150 disabled:opacity-50"
                :class="isPublic ? 'bg-primary' : 'bg-base-300'"
                :disabled="busy"
                :aria-label="isPublic ? 'Unpublish file' : 'Publish file by link'"
                @click="toggleVisibility"
              >
                <span
                  class="absolute top-[3px] size-3.5 rounded-full transition-[left] duration-150"
                  :class="isPublic ? 'left-[17px] bg-primary-content' : 'left-[3px] bg-faint'"
                />
              </button>
            </div>

            <div v-if="isPublic" class="mt-2.5 flex items-stretch gap-2">
              <input
                :value="publicLink"
                readonly
                class="input h-8 min-w-0 flex-1 truncate px-2.5 font-mono text-[11px] text-dim"
                aria-label="Public link"
                @focus="($event.target as HTMLInputElement).select()"
              />
              <CopyButton :value="publicLink" label="public link" />
            </div>
          </div>

          <dl class="divide-y divide-line-soft">
            <div class="flex items-start justify-between gap-3 px-4 py-2">
              <dt class="shrink-0 text-[11px] text-faint">Type</dt>
              <dd class="break-all text-right text-[11px] text-dim">{{ file ? mimeOf(file) : '—' }}</dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-[11px] text-faint">Size</dt>
              <dd class="text-[11px] tabular-nums text-dim">
                {{ file?.is_folder ? '—' : formatBytes(file?.object?.size) }}
              </dd>
            </div>
            <div v-if="dimensions" class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-[11px] text-faint">Dimensions</dt>
              <dd class="text-[11px] tabular-nums text-dim">{{ dimensions }}</dd>
            </div>
            <div v-if="duration" class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-[11px] text-faint">Duration</dt>
              <dd class="text-[11px] tabular-nums text-dim">{{ duration }}</dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-[11px] text-faint">Modified</dt>
              <dd class="text-[11px] tabular-nums text-dim">{{ formatDateTime(file?.updated_at) }}</dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-[11px] text-faint">Created</dt>
              <dd class="text-[11px] tabular-nums text-dim">{{ formatDateTime(file?.created_at) }}</dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-[11px] text-faint">Pool</dt>
              <dd class="text-[11px] text-dim">{{ poolName }}</dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-[11px] text-faint">Indexed</dt>
              <dd class="flex items-center gap-1.5 text-[11px] text-dim">
                <Checkmark16 v-if="file?.indexed" class="h-3 w-3 text-success" />
                {{ file?.indexed ? 'Yes' : 'No' }}
              </dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-[11px] text-faint">Object ID</dt>
              <dd class="font-mono text-[11px] text-dim" :title="file?.object_id ?? undefined">
                {{ shortId(file?.object_id) }}
              </dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-[11px] text-faint">Hash</dt>
              <dd class="font-mono text-[11px] text-dim" :title="file?.object?.hash ?? undefined">
                {{ shortHash(file?.object?.hash) }}
              </dd>
            </div>
          </dl>

          <div v-if="permissions" class="border-t border-line-soft px-4 py-3.5">
            <div class="text-[11px] font-medium text-faint">Capabilities</div>
            <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1.5 text-[11px]">
              <span
                class="flex items-center gap-1.5"
                :class="permissions.readable ? 'text-dim' : 'text-faint'"
              >
                <span
                  class="dot"
                  :class="permissions.readable ? 'bg-success' : 'bg-line'"
                  aria-hidden="true"
                />
                Read
              </span>
              <span
                class="flex items-center gap-1.5"
                :class="permissions.writable ? 'text-dim' : 'text-faint'"
              >
                <span
                  class="dot"
                  :class="permissions.writable ? 'bg-success' : 'bg-line'"
                  aria-hidden="true"
                />
                Write
              </span>
              <span
                class="flex items-center gap-1.5"
                :class="permissions.manageable ? 'text-dim' : 'text-faint'"
              >
                <span
                  class="dot"
                  :class="permissions.manageable ? 'bg-success' : 'bg-line'"
                  aria-hidden="true"
                />
                Manage
              </span>
            </div>
          </div>
        </template>
      </div>
    </aside>
  </Transition>
</template>
