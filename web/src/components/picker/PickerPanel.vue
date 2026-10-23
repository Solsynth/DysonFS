<script setup lang="ts">
/**
 * The picker surface: a folder listing with a breadcrumb, a search scoped to
 * the open folder, a selection that survives navigation, and — when the host
 * asks for it — a drop target that uploads new files.
 *
 * New files are uploaded **unindexed**: they belong to the drive's object
 * store but not to its folder tree, so picking something into another app does
 * not also litter that app's user's drive. The row comes back to the host with
 * its id, so the app can reference the bytes immediately.
 *
 * It is a plain component rather than a store, so two embeds can live on one
 * page without sharing a folder or a selection.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ArrowUp16,
  Checkmark16,
  Close16,
  Folder16,
  Search16,
  Upload16,
  WarningAlt16,
} from '@carbon/icons-vue'

import BrandMark from '@/components/BrandMark.vue'
import FileGlyph from '@/components/FileGlyph.vue'
import type { CloudFile } from '@/lib/api/types'
import { openUrl, thumbnailUrl, uploadDirect } from '@/lib/api/drive'
import { displayName, extensionOf, formatBytes, kindLabel, mimeOf } from '@/lib/format'
import { usePickerBrowser } from '@/lib/picker/browse'
import { isSelectable } from '@/lib/picker/filtering'
import type { PickerFile, PickerOptions } from '@/lib/picker/protocol'

const props = withDefaults(
  defineProps<{
    options: PickerOptions
    /** Adds a Cancel control; an embed that owns its own dialog leaves it out. */
    dismissible?: boolean
  }>(),
  { dismissible: true },
)

const emit = defineEmits<{
  picked: [files: PickerFile[]]
  cancelled: []
  error: [message: string]
}>()

const { t } = useI18n()
const browser = usePickerBrowser(() => props.options)

const chosen = ref<Map<string, CloudFile>>(new Map())
/** Rows this picker uploaded; they live outside the folder listing. */
const uploads = ref<CloudFile[]>([])
const activeId = ref<string | null>(null)
const dragDepth = ref(0)
const uploading = ref(false)
const uploadPercent = ref(0)
const uploadName = ref('')
const uploadError = ref('')
const fileInput = ref<HTMLInputElement | null>(null)

const multiple = computed(() => props.options.multiple === true)
const chosenList = computed(() => [...chosen.value.values()])
const chosenCount = computed(() => chosen.value.size)
const canUpload = computed(() => props.options.upload === true)
const dragging = computed(() => dragDepth.value > 0)

const heading = computed(() => props.options.title || t('picker.title'))
const guidance = computed(() => props.options.description || t('picker.description'))

const canConfirm = computed(() => {
  if (chosenCount.value === 0) return false
  if (!multiple.value) return true
  return chosenList.value.every((file) => isSelectable(file, props.options))
})

const emptyMessage = computed(() => {
  const term = browser.query.value.trim()
  if (term) return t('picker.emptySearch', { query: term })
  if (props.options.emptyHint) return props.options.emptyHint
  if (props.options.view === 'files' && browser.items.value.length > 0) {
    return t('picker.emptyFiltered')
  }
  return t('picker.empty')
})

/**
 * Uploaded rows come first: they are the answer the person just created, and
 * they are not part of the folder listing (an unindexed row has no place in
 * the tree, which is the whole point of uploading it this way).
 */
const rows = computed(() => [...uploads.value, ...browser.items.value])

function isChosen(file: CloudFile): boolean {
  return chosen.value.has(file.id)
}

function isUpload(file: CloudFile): boolean {
  return file.indexed === false
}

function toggle(file: CloudFile) {
  if (!isSelectable(file, props.options)) return
  const next = new Map(chosen.value)
  if (next.has(file.id)) {
    next.delete(file.id)
  } else {
    if (!multiple.value) next.clear()
    next.set(file.id, file)
  }
  chosen.value = next
}

/** Activation: folders open, files are chosen (and confirmed when single). */
function activate(file: CloudFile) {
  if (file.is_folder && props.options.view !== 'folders') {
    void browser.enter(file.id)
    return
  }
  toggle(file)
  if (!file.is_folder && !multiple.value && chosen.value.has(file.id)) confirm()
}

function move(delta: number) {
  const list = rows.value
  if (list.length === 0) return
  const index = list.findIndex((item) => item.id === activeId.value)
  const next = index < 0 ? 0 : Math.min(list.length - 1, Math.max(0, index + delta))
  activeId.value = list[next]?.id ?? null
  const element = document.getElementById(`picker-row-${activeId.value}`)
  element?.focus()
  element?.scrollIntoView({ block: 'nearest' })
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    move(1)
    return
  }
  if (event.key === 'ArrowUp') {
    event.preventDefault()
    move(-1)
    return
  }
  if (event.key === 'Enter') {
    const file = rows.value.find((item) => item.id === activeId.value)
    if (!file) return
    event.preventDefault()
    activate(file)
  }
}

function describe(file: CloudFile): PickerFile {
  return {
    id: file.id,
    name: displayName(file),
    is_folder: file.is_folder,
    mime_type: mimeOf(file),
    size: file.object?.size ?? 0,
    extension: extensionOf(file.name),
    kind: kindLabel(file),
    parent_id: file.parent_id ?? null,
    created_at: file.created_at,
    updated_at: file.updated_at,
    url: openUrl(file.id),
    thumbnail_url: file.object?.has_thumbnail ? thumbnailUrl(file.id) : null,
    // postMessage runs the structured clone algorithm, which refuses Vue's
    // reactive proxies; a JSON round trip leaves plain data behind.
    file: JSON.parse(JSON.stringify(file)) as Record<string, unknown>,
  }
}

function confirm() {
  if (!canConfirm.value) return
  emit('picked', chosenList.value.map(describe))
}

function cancel() {
  emit('cancelled')
}

function clearSelection() {
  chosen.value = new Map()
}

function requestUpload() {
  fileInput.value?.click()
}

/* Drop handling counts enter/leave pairs: dragging over a child fires `leave`
   on the parent, and a boolean would flicker the drop hint. */
function onDragEnter(event: DragEvent) {
  if (!canUpload.value || !event.dataTransfer?.types.includes('Files')) return
  event.preventDefault()
  dragDepth.value += 1
}

function onDragOver(event: DragEvent) {
  if (!canUpload.value || !event.dataTransfer?.types.includes('Files')) return
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
}

function onDragLeave() {
  dragDepth.value = Math.max(0, dragDepth.value - 1)
}

function onDrop(event: DragEvent) {
  if (!canUpload.value) return
  event.preventDefault()
  dragDepth.value = 0
  const files = Array.from(event.dataTransfer?.files ?? [])
  if (files.length) void upload(files)
}

function onFilesPicked(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  if (files.length) void upload(files)
}

/**
 * Uploads unindexed rows and marks them chosen, so a person who dropped a file
 * only has to press Select. A file the host's rules refuse is uploaded anyway
 * but left unselected, with the reason in the footer: discarding the bytes
 * after transferring them would be worse than saying no.
 *
 * No `parent_id` is sent on purpose. The service promotes an upload into the
 * drive tree when its parent is indexed (`CreateWorkspaceUploadedFile`), so a
 * folder-relative upload would not be unindexed at all — the file would appear
 * in the person's drive as a side effect of attaching it to another app.
 */
async function upload(files: File[]) {
  uploading.value = true
  uploadError.value = ''
  const next = new Map(chosen.value)
  const created: CloudFile[] = []
  try {
    for (const [index, file] of files.entries()) {
      uploadName.value = file.name
      uploadPercent.value = 0
      const row = await uploadDirect(file, {
        indexed: false,
        onProgress: (percent) => {
          uploadPercent.value = percent
        },
      })
      created.push(row)
      if (isSelectable(row, props.options)) {
        if (!multiple.value) next.clear()
        next.set(row.id, row)
      }
      uploadPercent.value = Math.round(((index + 1) / files.length) * 100)
    }
    uploads.value = [...created, ...uploads.value]
    chosen.value = next
  } catch (caught) {
    uploadError.value = caught instanceof Error ? caught.message : t('picker.errors.upload')
    emit('error', uploadError.value)
  } finally {
    uploading.value = false
    uploadName.value = ''
  }
}

/**
 * Dropping an upload is also a withdrawal from the answer: the row's bytes
 * stay in the object store, so leaving it selected would hand the app a file
 * the person just took back.
 */
function removeUpload(file: CloudFile) {
  uploads.value = uploads.value.filter((entry) => entry.id !== file.id)
  const next = new Map(chosen.value)
  next.delete(file.id)
  chosen.value = next
}

// A folder change invalidates the anchor; the next arrow key starts at the top.
watch(
  () => browser.folderId.value,
  () => {
    activeId.value = null
  },
)

// A new option set is a new request: an embed that reopens the picker must not
// inherit the last answer.
watch(
  () => props.options,
  () => {
    clearSelection()
    uploads.value = []
    uploadError.value = ''
  },
)

defineExpose({ confirm, cancel, clearSelection })
</script>

<template>
  <div
    class="relative flex h-full min-h-0 flex-col bg-base-100 text-base-content"
    role="dialog"
    :aria-label="t('picker.aria')"
    @dragenter="onDragEnter"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
    <header class="flex shrink-0 items-start gap-2.5 border-b border-line px-4 py-3">
      <BrandMark :size="18" class="mt-0.5 text-dim" />
      <div class="min-w-0 flex-1">
        <h1 class="title truncate text-[16px] leading-tight">{{ heading }}</h1>
        <p class="voice mt-0.5 truncate text-[12px]">{{ guidance }}</p>
      </div>
      <button
        v-if="dismissible"
        type="button"
        class="btn btn-ghost btn-xs px-1 text-dim"
        :aria-label="t('picker.cancel')"
        :title="t('picker.cancel')"
        @click="cancel"
      >
        <Close16 />
      </button>
    </header>

    <!-- Breadcrumb: the trail is the only way back up without guessing. -->
    <div class="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-line px-3 py-1.5">
      <button
        type="button"
        class="btn btn-ghost btn-xs shrink-0 px-1"
        :aria-label="t('picker.up')"
        :title="t('picker.up')"
        :disabled="browser.crumbs.value.length <= 1"
        @click="browser.enter(browser.crumbs.value[browser.crumbs.value.length - 2]?.id ?? null)"
      >
        <ArrowUp16 />
      </button>
      <nav class="flex min-w-0 items-center gap-1 text-2xs" :aria-label="t('picker.breadcrumb')">
        <template v-for="(crumb, index) in browser.crumbs.value" :key="crumb.id ?? 'root'">
          <span v-if="index > 0" class="text-line" aria-hidden="true">/</span>
          <button
            type="button"
            class="max-w-[160px] truncate px-0.5 transition-colors"
            :class="
              index === browser.crumbs.value.length - 1
                ? 'text-base-content'
                : 'text-dim hover:text-base-content'
            "
            @click="browser.jumpTo(index)"
          >
            {{ crumb.name }}
          </button>
        </template>
      </nav>
    </div>

    <div class="flex shrink-0 items-center gap-2 border-b border-line px-3 py-1.5">
      <Search16 class="h-3.5 w-3.5 shrink-0 text-faint" aria-hidden="true" />
      <input
        class="input h-7 min-w-0 flex-1 border-0 bg-transparent px-0 text-[12px] focus:outline-none"
        type="search"
        :value="browser.query.value"
        :placeholder="t('picker.searchPlaceholder')"
        :aria-label="t('picker.search')"
        @input="browser.search(($event.target as HTMLInputElement).value)"
      />
      <template v-if="canUpload">
        <input
          ref="fileInput"
          type="file"
          class="sr-only"
          :multiple="multiple"
          @change="onFilesPicked"
        />
        <button
          type="button"
          class="btn btn-ghost btn-xs shrink-0 gap-1"
          :disabled="uploading"
          :title="t('picker.uploadHint')"
          @click="requestUpload"
        >
          <Upload16 class="h-3 w-3" aria-hidden="true" />
          {{ uploading ? t('picker.uploading', { percent: uploadPercent }) : t('picker.upload') }}
        </button>
      </template>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto scroll-region" @keydown="onKeydown">
      <div v-if="browser.loading.value" class="space-y-2 p-3">
        <div v-for="row in 6" :key="row" class="h-8 rounded bg-base-300/60 pulse-soft" />
      </div>

      <div v-else-if="browser.error.value" class="px-4 py-8 text-center">
        <WarningAlt16 class="mx-auto h-5 w-5 text-error" aria-hidden="true" />
        <p class="mt-2 text-[12px] text-error">{{ browser.error.value }}</p>
        <button type="button" class="btn btn-ghost btn-xs mt-2" @click="browser.retry()">
          {{ t('picker.retry') }}
        </button>
      </div>

      <div v-else-if="browser.isEmpty.value && chosenCount === 0" class="px-4 py-10 text-center">
        <Folder16 class="mx-auto h-5 w-5 text-faint" aria-hidden="true" />
        <p class="voice mt-2 text-[12px]">{{ emptyMessage }}</p>
        <button
          v-if="canUpload"
          type="button"
          class="btn btn-ghost btn-xs mt-3"
          @click="requestUpload"
        >
          <Upload16 class="h-3 w-3" aria-hidden="true" />
          {{ t('picker.upload') }}
        </button>
      </div>

      <template v-else>
        <ul class="divide-y divide-line-soft" role="listbox" :aria-multiselectable="multiple">
          <li
            v-for="file in rows"
            :id="`picker-row-${file.id}`"
            :key="file.id"
            tabindex="0"
            role="option"
            :aria-selected="isChosen(file)"
            :aria-disabled="!isSelectable(file, options)"
            class="group flex h-[42px] cursor-default items-center gap-2.5 px-3 text-[12.5px] transition-colors duration-100 focus-visible:bg-primary/[0.06]"
            :class="[
              isChosen(file) ? 'bg-primary/[0.06]' : 'hover:bg-base-200/50',
              isSelectable(file, options) ? '' : 'opacity-45',
            ]"
            @click="activate(file)"
            @focus="activeId = file.id"
          >
            <span
              class="flex h-4 w-4 shrink-0 items-center justify-center rounded-[2px] border"
              :class="
                isChosen(file)
                  ? 'border-primary bg-primary text-primary-content'
                  : 'border-line text-transparent'
              "
              aria-hidden="true"
            >
              <Checkmark16 class="h-3 w-3" />
            </span>
            <FileGlyph :file="file" />
            <span class="min-w-0 flex-1 truncate" :title="displayName(file)">
              {{ displayName(file) }}
            </span>
            <span
              v-if="isUpload(file)"
              class="shrink-0 rounded-[2px] border border-primary/40 px-1 text-2xs text-primary"
            >
              {{ t('picker.new') }}
            </span>
            <span
              v-else-if="!isSelectable(file, options) && !file.is_folder"
              class="hidden shrink-0 text-2xs text-faint sm:inline"
            >
              {{ t('picker.notAccepted') }}
            </span>
            <button
              v-if="isUpload(file)"
              type="button"
              class="shrink-0 px-1 text-2xs text-faint opacity-0 transition-opacity hover:text-error group-hover:opacity-100 focus-visible:opacity-100"
              :aria-label="t('picker.removeNew')"
              :title="t('picker.removeNew')"
              @click.stop="removeUpload(file)"
            >
              <Close16 class="h-3 w-3" />
            </button>
            <span class="w-[64px] shrink-0 text-right text-2xs tabular-nums text-faint">
              {{ file.is_folder ? '—' : formatBytes(file.object?.size) }}
            </span>
            <span class="w-[46px] shrink-0 text-right text-2xs text-faint">
              {{ kindLabel(file) }}
            </span>
          </li>
        </ul>

        <div v-if="browser.hasMore.value" class="border-t border-line-soft p-2 text-center">
          <button
            type="button"
            class="btn btn-ghost btn-xs"
            :disabled="browser.loadingMore.value"
            @click="browser.loadMore()"
          >
            {{ browser.loadingMore.value ? t('picker.loadingMore') : t('picker.loadMore') }}
          </button>
        </div>
      </template>
    </div>

    <!-- A drop target the whole panel, with the progress of what is landing. -->
    <div
      v-if="dragging || uploading"
      class="pointer-events-none absolute inset-0 z-20 flex flex-col items-center justify-center gap-3 border-2 border-dashed border-primary bg-base-100/85 text-center"
      role="status"
      aria-live="polite"
    >
      <Upload16 class="h-6 w-6 text-primary" aria-hidden="true" />
      <p class="voice text-[12px]">
        {{ uploading ? t('picker.uploadingFile', { name: uploadName }) : t('picker.drop') }}
      </p>
      <div v-if="uploading" class="h-[2px] w-40 overflow-hidden bg-line-soft">
        <div class="h-full bg-primary transition-[width]" :style="{ width: `${uploadPercent}%` }" />
      </div>
      <p v-else class="text-2xs text-faint">{{ t('picker.dropHint') }}</p>
    </div>

    <footer
      class="flex shrink-0 items-center justify-between gap-3 border-t border-line px-4 py-2.5"
    >
      <span
        class="min-w-0 flex-1 truncate text-2xs"
        :class="uploadError ? 'text-error' : 'text-faint'"
      >
        <template v-if="uploadError">{{ uploadError }}</template>
        <template v-else-if="chosenCount === 0">{{ t('picker.nothingSelected') }}</template>
        <template v-else-if="chosenCount === 1">
          {{ t('picker.selectedOne') }} · {{ chosenList[0]?.name }}
        </template>
        <template v-else>{{ t('picker.selectedMany', { n: chosenCount }) }}</template>
      </span>
      <div class="flex shrink-0 items-center gap-2">
        <button v-if="dismissible" type="button" class="btn btn-ghost btn-sm" @click="cancel">
          {{ t('picker.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="!canConfirm || uploading"
          @click="confirm"
        >
          {{ multiple ? t('picker.select') : t('picker.selectOne') }}
        </button>
      </div>
    </footer>
  </div>
</template>
