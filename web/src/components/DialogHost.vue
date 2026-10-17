<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { Close16, WarningAlt16 } from '@carbon/icons-vue'

import CopyButton from './CopyButton.vue'
import FolderPicker from './FolderPicker.vue'
import IconButton from './IconButton.vue'
import { openUrl } from '@/lib/api/drive'
import { formatBytes, formatCount } from '@/lib/format'
import { useUiStore } from '@/stores/ui'
import { useVaultStore } from '@/stores/vault'

const ui = useUiStore()
const vault = useVaultStore()

const dialog = computed(() => ui.dialog)
const folderName = ref('')
const renameName = ref('')
const destination = ref<string | null>(null)
const destinationLabel = ref('My Drive')
const folderInput = ref<HTMLInputElement | null>(null)
const renameInput = ref<HTMLInputElement | null>(null)

const moveFiles = computed(() => (dialog.value?.kind === 'move' ? dialog.value.files : []))
const renameFile = computed(() => (dialog.value?.kind === 'rename' ? dialog.value.file : null))
const confirmFiles = computed(() =>
  dialog.value?.kind === 'recycle' || dialog.value?.kind === 'purge' ? dialog.value.files : [],
)
const shareFile = computed(() => (dialog.value?.kind === 'share' ? dialog.value.file : null))
const shareIsPublic = computed(() => shareFile.value?.permission_status?.visibility === 'public')
const shareLink = computed(() =>
  shareFile.value ? `${window.location.origin}${openUrl(shareFile.value.id)}` : '',
)
const busy = computed(() => vault.actionBusy !== null)
const totalBytes = computed(() =>
  confirmFiles.value.reduce((sum, file) => sum + (file.object?.size ?? 0), 0),
)
const selectionLabel = computed(() => {
  const files = moveFiles.value
  if (files.length === 1) return files[0]?.name ?? 'object'
  return `${formatCount(files.length)} objects`
})
const confirmLabel = computed(() => {
  const files = confirmFiles.value
  if (files.length === 1) return files[0]?.name ?? 'object'
  return `${formatCount(files.length)} objects`
})
const confirmNoun = computed(() => {
  const files = confirmFiles.value
  if (files.length === 1) return files[0]?.is_folder ? '1 folder' : '1 object'
  const folders = files.filter((file) => file.is_folder).length
  if (folders === files.length) return `${formatCount(files.length)} folders`
  return `${formatCount(files.length)} objects`
})
const confirmSize = computed(() =>
  totalBytes.value > 0 ? ` totalling ${formatBytes(totalBytes.value)}` : '',
)

watch(
  dialog,
  async (value) => {
    if (!value) return
    await nextTick()
    if (value.kind === 'new-folder') {
      folderName.value = ''
      folderInput.value?.focus()
    } else if (value.kind === 'rename') {
      renameName.value = value.file.name
      renameInput.value?.focus()
      renameInput.value?.select()
    } else if (value.kind === 'move') {
      destination.value = vault.folderId
      destinationLabel.value = vault.crumbs.map((crumb) => crumb.name).join(' / ')
    }
  },
  { immediate: true },
)

function close() {
  if (busy.value) return
  ui.closeDialog()
}

function onDestination(payload: { id: string | null; path: string }) {
  destination.value = payload.id
  destinationLabel.value = payload.path
}

async function submitFolder() {
  const name = folderName.value.trim()
  if (!name || busy.value) return
  try {
    await vault.createFolder(name)
    ui.closeDialog()
  } catch {
    // The store reports the failure and keeps the dialog open.
  }
}

async function submitRename() {
  const file = renameFile.value
  const name = renameName.value.trim()
  if (!file || !name || busy.value) return
  try {
    await vault.rename(file, name)
    ui.closeDialog()
  } catch {
    // Keep the dialog open.
  }
}

async function submitMove() {
  if (moveFiles.value.length === 0 || busy.value) return
  try {
    await vault.move(moveFiles.value, destination.value)
    ui.closeDialog()
  } catch {
    // Keep the dialog open.
  }
}

async function submitRecycle() {
  if (confirmFiles.value.length === 0 || busy.value) return
  try {
    await vault.recycle(confirmFiles.value)
    ui.closeDialog()
  } catch {
    // Keep the dialog open.
  }
}

async function submitPurge() {
  if (confirmFiles.value.length === 0 || busy.value) return
  try {
    await vault.purge(confirmFiles.value)
    ui.closeDialog()
  } catch {
    // Keep the dialog open.
  }
}

async function toggleShare() {
  if (!shareFile.value || busy.value) return
  try {
    await vault.setVisibility(shareFile.value, !shareIsPublic.value)
  } catch {
    // The store reports the failure.
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="opacity-0"
      leave-active-class="transition duration-100 ease-in"
      leave-to-class="opacity-0"
    >
      <div
        v-if="dialog"
        class="fixed inset-0 z-[75] flex items-center justify-center overflow-y-auto bg-black/50 px-4 py-8 backdrop-blur-sm"
        @mousedown.self="close"
      >
        <div
          class="palette-in relative w-full rounded-xl bg-base-200 shadow-2xl"
          :class="dialog.kind === 'shortcuts' ? 'max-w-[540px]' : 'max-w-[440px]'"
          role="dialog"
          aria-modal="true"
          aria-labelledby="dialog-title"
        >
          <IconButton class="absolute right-2 top-2 z-10" label="Close dialog" @click="close">
            <Close16 />
          </IconButton>

          <template v-if="dialog.kind === 'new-folder'">
            <form @submit.prevent="submitFolder">
              <div class="px-5 pb-4 pt-5">
                <h2 id="dialog-title" class="title text-[18px] text-base-content">New folder</h2>
                <p class="voice mt-1">It will appear in the current location.</p>
                <label class="field-label mt-4">Folder name</label>
                <input
                  ref="folderInput"
                  v-model="folderName"
                  class="input"
                  type="text"
                  placeholder="e.g. orbital-8"
                  autocomplete="off"
                  maxlength="160"
                />
              </div>
              <div class="flex justify-end gap-2 border-t border-line px-5 py-3">
                <button class="btn btn-ghost btn-sm" type="button" @click="close">Cancel</button>
                <button
                  class="btn btn-primary btn-sm"
                  type="submit"
                  :disabled="!folderName.trim() || busy"
                >
                  {{ busy ? 'Creating…' : 'Create folder' }}
                </button>
              </div>
            </form>
          </template>

          <template v-else-if="dialog.kind === 'rename'">
            <form @submit.prevent="submitRename">
              <div class="px-5 pb-4 pt-5">
                <h2 id="dialog-title" class="title text-[18px] text-base-content">Rename file</h2>
                <p class="voice mt-1">Links and IDs survive the rename; the path changes.</p>
                <label class="field-label mt-4">Name</label>
                <input
                  ref="renameInput"
                  v-model="renameName"
                  class="input"
                  type="text"
                  autocomplete="off"
                  maxlength="255"
                />
              </div>
              <div class="flex justify-end gap-2 border-t border-line px-5 py-3">
                <button class="btn btn-ghost btn-sm" type="button" @click="close">Cancel</button>
                <button
                  class="btn btn-primary btn-sm"
                  type="submit"
                  :disabled="!renameName.trim() || renameName.trim() === renameFile?.name || busy"
                >
                  {{ busy ? 'Renaming…' : 'Rename' }}
                </button>
              </div>
            </form>
          </template>

          <template v-else-if="dialog.kind === 'move'">
            <div class="px-5 pb-3 pt-5">
              <h2 id="dialog-title" class="title text-[18px] text-base-content">
                Move {{ selectionLabel }}
              </h2>
              <p class="voice mt-1">Choose a destination. The file keeps its identity.</p>
            </div>
            <div class="px-5 pb-4">
              <FolderPicker
                :initial-id="vault.folderId"
                :exclude="moveFiles.map((file) => file.id)"
                @change="onDestination"
              />
            </div>
            <div class="flex justify-end gap-2 border-t border-line px-5 py-3">
              <button class="btn btn-ghost btn-sm" type="button" @click="close">Cancel</button>
              <button
                class="btn btn-primary btn-sm"
                type="button"
                :disabled="busy"
                @click="submitMove"
              >
                {{ busy ? 'Moving…' : 'Move here' }}
              </button>
            </div>
          </template>

          <template v-else-if="dialog.kind === 'recycle'">
            <div class="px-5 py-5">
              <h2 id="dialog-title" class="title text-[18px] text-base-content">
                Recycle {{ confirmLabel }}?
              </h2>
              <p class="voice mt-1.5">
                {{ confirmNoun }}{{ confirmSize }} will leave the drive. You can restore it from
                the Recycle Bin until it is purged.
              </p>
              <div class="mt-3 rounded-lg bg-base-300/50 px-3 py-2 text-[11px] text-dim">
                <span class="text-faint">Recovery</span> · available from the Recycle Bin
              </div>
            </div>
            <div class="flex justify-end gap-2 border-t border-line px-5 py-3">
              <button class="btn btn-ghost btn-sm" type="button" @click="close">Cancel</button>
              <button
                class="btn btn-primary btn-sm"
                type="button"
                :disabled="busy"
                @click="submitRecycle"
              >
                {{ busy ? 'Recycling…' : 'Move to recycle' }}
              </button>
            </div>
          </template>

          <template v-else-if="dialog.kind === 'purge'">
            <div class="px-5 py-5">
              <h2 id="dialog-title" class="title text-[18px] text-error">
                Delete {{ confirmLabel }} forever?
              </h2>
              <p class="voice mt-1.5">
                {{ confirmNoun }}{{ confirmSize }} will be erased from storage and the index.
              </p>
              <div
                class="mt-3 flex items-center gap-2 rounded-lg bg-error/10 px-3 py-2 text-[11px] text-error"
              >
                <WarningAlt16 class="h-3.5 w-3.5 shrink-0" />
                This can’t be undone.
              </div>
            </div>
            <div class="flex justify-end gap-2 border-t border-line px-5 py-3">
              <button class="btn btn-ghost btn-sm" type="button" @click="close">Cancel</button>
              <button
                class="btn btn-error btn-sm"
                type="button"
                :disabled="busy"
                @click="submitPurge"
              >
                {{ busy ? 'Purging…' : 'Delete forever' }}
              </button>
            </div>
          </template>

          <template v-else-if="dialog.kind === 'share'">
            <div class="px-5 py-5 pr-12">
              <h2 id="dialog-title" class="title break-words text-[18px] text-base-content">
                {{ shareFile?.name }}
              </h2>
              <p class="voice mt-1">
                Anyone with the link can read this file. You can revoke the link at any time.
              </p>

              <div
                class="mt-4 flex items-center justify-between gap-3 rounded-lg bg-base-300/40 px-3.5 py-3"
              >
                <div>
                  <div class="text-[12px] text-base-content">Public link</div>
                  <div
                    class="mt-0.5 text-[11px]"
                    :class="shareIsPublic ? 'text-primary' : 'text-faint'"
                  >
                    {{ shareIsPublic ? 'On' : 'Off' }}
                  </div>
                </div>
                <button
                  type="button"
                  role="switch"
                  :aria-checked="shareIsPublic"
                  class="relative h-5 w-[34px] shrink-0 rounded-full transition-colors duration-150 disabled:opacity-50"
                  :class="shareIsPublic ? 'bg-primary' : 'bg-base-300'"
                  :disabled="busy"
                  @click="toggleShare"
                >
                  <span
                    class="absolute top-[3px] size-3.5 rounded-full transition-[left] duration-150"
                    :class="shareIsPublic ? 'left-[17px] bg-primary-content' : 'left-[3px] bg-faint'"
                  />
                </button>
              </div>

              <div v-if="shareIsPublic" class="mt-2 flex items-stretch gap-2">
                <input
                  :value="shareLink"
                  readonly
                  class="input h-8 min-w-0 flex-1 px-2.5 font-mono text-[11px] text-dim"
                  aria-label="Public link"
                  @focus="($event.target as HTMLInputElement).select()"
                />
                <CopyButton :value="shareLink" label="public link" />
              </div>
            </div>
            <div class="flex justify-end gap-2 border-t border-line px-5 py-3">
              <button class="btn btn-primary btn-sm" type="button" @click="close">Done</button>
            </div>
          </template>

          <template v-else>
            <div class="px-5 py-5 pr-12">
              <h2 id="dialog-title" class="title text-[18px] text-base-content">Keyboard shortcuts</h2>
              <p class="voice mt-1">Everything here works without a pointer.</p>
              <dl class="mt-4 divide-y divide-line-soft text-[11px]">
                <div
                  v-for="row in [
                    ['Command palette', '⌘ K'],
                    ['Close palette, dialog or inspector', 'Esc'],
                    ['New folder', 'N'],
                    ['Upload files', 'U'],
                    ['Recycle selection', 'Del'],
                    ['Open focused file', '↵'],
                    ['Range select', 'Shift click'],
                    ['Toggle selection', '⌘ click'],
                    ['Move through list', '↑ ↓'],
                  ]"
                  :key="row[0]"
                  class="flex items-center justify-between gap-4 py-2"
                >
                  <dt class="text-dim">{{ row[0] }}</dt>
                  <dd class="flex shrink-0 gap-1">
                    <span v-for="key in row[1].split(' ')" :key="key" class="kbd">{{ key }}</span>
                  </dd>
                </div>
              </dl>
            </div>
            <div class="flex justify-end border-t border-line px-5 py-3">
              <button class="btn btn-primary btn-sm" type="button" @click="close">Close</button>
            </div>
          </template>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
