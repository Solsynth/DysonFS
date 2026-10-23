<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowLeft16,
  Checkmark16,
  Copy16,
  Download16,
  Edit16,
  Folder16,
  Link16,
  Move16,
  OpenPanelLeft16,
  TextAlignLeft16,
  TrashCan16,
  Undo16,
} from '@carbon/icons-vue'

import CopyButton from '@/components/CopyButton.vue'
import FileGlyph from '@/components/FileGlyph.vue'
import { driveApi, fetchTextPreview, openUrl, previewUrl } from '@/lib/api/drive'
import type { CloudFile } from '@/lib/api/types'
import { previewMode } from '@/lib/fileKinds'
import {
  displayName,
  fileDimensions,
  fileDuration,
  formatBytes,
  formatCount,
  formatDateTime,
  kindLabel,
  mimeOf,
  shortHash,
} from '@/lib/format'
import { useNoticeStore } from '@/stores/notice'
import { useTabStore } from '@/stores/tabs'
import { useUiStore } from '@/stores/ui'
import { useVaultStore } from '@/stores/vault'

const { t } = useI18n()
const vault = useVaultStore()
const tabs = useTabStore()
const ui = useUiStore()
const notice = useNoticeStore()
const route = useRoute()
const router = useRouter()

const file = ref<CloudFile | null>(null)
const loading = ref(true)
const sourceError = ref('')
const error = ref('')
const mediaFailed = ref(false)
const source = shallowRef('')
const sourceState = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle')

const fileId = computed(() => String(route.params.id ?? ''))
const mode = computed(() => (file.value ? previewMode(file.value) : 'none'))
const inRecycle = computed(() => file.value?.is_marked_recycle === true)
const isPublic = computed(() => file.value?.permission_status?.visibility === 'public')
const dimensions = computed(() => (file.value ? fileDimensions(file.value) : null))
const duration = computed(() => (file.value ? fileDuration(file.value) : null))
const poolName = computed(() => {
  if (!file.value?.pool_id) return t('meta.poolDefault')
  return vault.pools.find((pool) => pool.id === file.value?.pool_id)?.name ?? t('meta.poolDefault')
})
const publicLink = computed(() =>
  file.value ? `${window.location.origin}${openUrl(file.value.id)}` : '',
)
const parentId = computed(() => file.value?.parent_id ?? null)
const sourceLines = computed(() => source.value.split('\n'))

async function load() {
  const id = fileId.value
  if (!id) {
    error.value = t('preview.noTabFile')
    loading.value = false
    return
  }
  loading.value = true
  error.value = ''
  mediaFailed.value = false
  source.value = ''
  sourceState.value = 'idle'
  try {
    file.value = await driveApi.file(id)
    vault.setPreviewFile(file.value)
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : t('preview.readFailed')
    file.value = null
  } finally {
    loading.value = false
  }
}

async function loadSource() {
  if (mode.value !== 'text') return
  if (sourceState.value === 'loading') return
  sourceState.value = 'loading'
  sourceError.value = ''
  try {
    source.value = await fetchTextPreview(fileId.value)
    sourceState.value = 'ready'
  } catch (caught) {
    sourceState.value = 'failed'
    sourceError.value = caught instanceof Error ? caught.message : t('preview.readFailed')
  }
}

watch(
  fileId,
  () => {
    void load()
  },
  { immediate: true },
)

watch(mode, (next) => {
  if (next === 'text') void loadSource()
})

// The preview is a tab like any other, so it registers itself rather than
// waiting for someone else to name it. A rename after the first sync only has
// to repaint the existing strip entry.
watch(
  [fileId, file],
  ([id, current]) => {
    if (typeof id !== 'string' || !id) return
    tabs.sync({
      id: route.path,
      kind: 'preview',
      name: current?.name ?? t('preview.title'),
      note: current ? kindLabel(current) : undefined,
    })
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  vault.setPreviewFile(null)
})

function backToFolder() {
  if (parentId.value) void router.push({ name: 'folder', params: { id: parentId.value } })
  else void router.push({ name: 'drive' })
}

function download() {
  if (!file.value) return
  // A same-origin anchor download streams the file in place; `window.open`
  // would leave an extra browser tab behind for a transfer the user only
  // asked to save.
  const link = document.createElement('a')
  link.href = openUrl(file.value.id, true)
  link.download = file.value.name || ''
  document.body.append(link)
  link.click()
  link.remove()
}

function rename() {
  if (file.value) ui.openDialog({ kind: 'rename', file: file.value })
}

function move() {
  if (file.value) ui.openDialog({ kind: 'move', files: [file.value] })
}

function recycle() {
  if (file.value) ui.openDialog({ kind: 'recycle', files: [file.value] })
}

function purge() {
  if (file.value) ui.openDialog({ kind: 'purge', files: [file.value] })
}

async function restore() {
  if (!file.value) return
  await vault.restore([file.value]).catch(() => undefined)
}

async function copyLink() {
  if (!publicLink.value) return
  try {
    await navigator.clipboard.writeText(publicLink.value)
    notice.ok(t('preview.linkCopied'))
  } catch {
    notice.error(t('preview.clipboardDenied'))
  }
}

async function toggleVisibility() {
  if (!file.value) return
  await vault.setVisibility(file.value, !isPublic.value).catch(() => undefined)
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col bg-ink">
    <div class="flex min-h-[44px] shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-line px-3 py-1.5 md:px-4">
      <button class="btn btn-ghost btn-xs" type="button" @click="backToFolder">
        <ArrowLeft16 />
        {{ t('preview.back') }}
      </button>
      <span class="truncate text-xs text-base-content" :title="file?.name">
        {{ file ? displayName(file) : t('preview.title') }}
      </span>
      <span
        v-if="file"
        class="chip"
        :class="isPublic ? 'text-primary' : ''"
        :title="isPublic ? t('preview.publicHint') : t('preview.privateHint')"
      >
        <Link16 class="h-3 w-3" aria-hidden="true" />
        {{ isPublic ? t('preview.public') : t('preview.private') }}
      </span>
      <span v-if="mode !== 'none'" class="chip">{{ mode }}</span>

      <div class="ml-auto flex items-center gap-1">
        <button v-if="!inRecycle" class="btn btn-outline btn-xs" type="button" @click="rename">
          <Edit16 />
          {{ t('preview.rename') }}
        </button>
        <button v-if="!inRecycle" class="btn btn-outline btn-xs" type="button" @click="move">
          <Move16 />
          {{ t('preview.move') }}
        </button>
        <button v-if="!inRecycle" class="btn btn-outline btn-xs" type="button" @click="recycle">
          <TrashCan16 />
          {{ t('preview.recycle') }}
        </button>
        <button v-else class="btn btn-outline btn-xs" type="button" @click="restore">
          <Undo16 />
          {{ t('preview.restore') }}
        </button>
        <button v-if="inRecycle" class="btn btn-error btn-xs" type="button" @click="purge">
          <TrashCan16 />
          {{ t('preview.deleteForever') }}
        </button>
        <button class="btn btn-outline btn-xs" type="button" @click="copyLink">
          <Copy16 />
          {{ t('preview.copyLink') }}
        </button>
        <button class="btn btn-primary btn-xs" type="button" @click="download">
          <Download16 />
          {{ t('preview.download') }}
        </button>
      </div>
    </div>

    <div class="flex min-h-0 flex-1">
      <!-- The bytes are the tab. Everything else in this route is supporting
           metadata, so the surface keeps the largest share of the space. -->
      <section class="flex min-w-0 flex-1 flex-col border-r border-line" :aria-label="t('preview.content')">
        <div v-if="loading" class="grid flex-1 place-items-center">
          <div class="w-full max-w-[320px] px-5">
            <div class="loading-scan h-[2px] w-full bg-line-soft" />
            <p class="voice mt-3 text-center">{{ t('preview.reading') }}</p>
          </div>
        </div>

        <div v-else-if="error && mode !== 'text'" class="grid flex-1 place-items-center px-6">
          <div class="max-w-[380px] text-center">
            <FileGlyph v-if="file" :file="file" size="lg" class="mx-auto opacity-60" />
            <h2 class="title mt-3 text-sm text-base-content">{{ t('preview.cannotRender') }}</h2>
            <p class="voice mt-2">{{ error }}</p>
            <button class="btn btn-primary btn-xs mt-5" type="button" @click="download">
              {{ t('preview.downloadInstead') }}
            </button>
          </div>
        </div>

        <template v-else-if="file && mode === 'image'">
          <div class="grid flex-1 place-items-center overflow-auto p-4">
            <img
              v-if="!mediaFailed"
              :src="previewUrl(file.id)"
              :alt="displayName(file)"
              class="max-h-full max-w-full object-contain"
              @error="mediaFailed = true"
            />
            <div v-else class="text-center">
              <FileGlyph :file="file" size="lg" class="mx-auto opacity-60" />
              <p class="voice mt-3">{{ t('preview.imageFailed') }}</p>
            </div>
          </div>
        </template>

        <template v-else-if="file && mode === 'video'">
          <div class="grid flex-1 place-items-center overflow-auto bg-ink p-4">
            <video :src="openUrl(file.id)" controls class="max-h-full max-w-full" />
          </div>
        </template>

        <template v-else-if="file && mode === 'audio'">
          <div class="grid flex-1 place-items-center px-6">
            <div class="w-full max-w-[420px] text-center">
              <FileGlyph :file="file" size="lg" class="mx-auto opacity-60" />
              <p class="mt-3 text-xs text-dim">{{ displayName(file) }}</p>
              <audio :src="openUrl(file.id)" controls class="mt-4 w-full" />
            </div>
          </div>
        </template>

        <template v-else-if="file && mode === 'pdf'">
          <iframe
            :src="openUrl(file.id)"
            :title="displayName(file)"
            class="min-h-0 flex-1 border-0 bg-base-200"
          />
        </template>

        <template v-else-if="file && mode === 'text'">
          <div class="flex shrink-0 items-center gap-2 border-b border-line px-4 py-1.5">
            <TextAlignLeft16 class="shrink-0 text-faint" aria-hidden="true" />
            <span class="text-2xs text-faint">
              {{
                sourceState === 'ready'
                  ? t('preview.lines', { n: formatCount(sourceLines.length) })
                  : t('preview.source')
              }}
            </span>
            <CopyButton
              v-if="sourceState === 'ready'"
              :value="source"
              label="source"
              class="ml-auto"
            />
          </div>
          <div class="min-h-0 flex-1 overflow-auto scroll-region bg-ink/60">
            <p v-if="sourceState === 'loading'" class="voice px-4 py-4">{{ t('preview.reading') }}</p>
            <p v-else-if="sourceState !== 'ready'" class="voice px-4 py-4 text-center">
              {{ sourceError || t('preview.nothingToRead') }}
            </p>
            <div v-else class="flex text-2xs leading-[1.7]">
              <div class="shrink-0 select-none border-r border-line-soft px-3 py-3 text-right tabular-nums text-faint">
                <div v-for="(_, index) in sourceLines" :key="index">{{ index + 1 }}</div>
              </div>
              <pre class="min-w-0 flex-1 overflow-x-auto px-4 py-3 text-base-content"><code>{{ source }}</code></pre>
            </div>
          </div>
        </template>

        <div v-else class="grid flex-1 place-items-center px-6">
          <div class="max-w-[380px] text-center">
            <FileGlyph v-if="file" :file="file" size="lg" class="mx-auto opacity-60" />
            <h2 class="title mt-3 text-sm text-base-content">
              {{ file ? kindLabel(file) : t('preview.nothingToShow') }}
            </h2>
            <p class="voice mt-2">
              {{ t('preview.noReader') }}
            </p>
            <button class="btn btn-primary btn-xs mt-5" type="button" @click="download">
              <Download16 />
              {{ t('preview.download') }}
            </button>
          </div>
        </div>
      </section>

      <aside
        class="hidden w-[312px] shrink-0 flex-col overflow-y-auto scroll-region lg:flex"
:aria-label="t('preview.details')"
      >
        <template v-if="file">
          <div class="border-b border-line px-4 py-4">
            <h1 class="title break-words text-sm text-base-content">{{ displayName(file) }}</h1>
            <p class="mt-1.5 text-2xs text-faint">
              {{ kindLabel(file) }} · {{ poolName }}
            </p>
          </div>

          <div class="border-b border-line px-4 py-3.5">
            <div class="flex items-start justify-between gap-3">
              <div>
                <div class="text-2xs text-base-content">{{ t('preview.publicLink') }}</div>
                <div class="mt-0.5 text-2xs text-faint">
                  {{ isPublic ? t('preview.publicLinkBody') : t('preview.privateLinkBody') }}
                </div>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="isPublic"
                :aria-label="isPublic ? t('preview.unpublish') : t('preview.publish')"
                class="relative mt-0.5 h-5 w-[34px] shrink-0 rounded-[2px] border border-line transition-colors disabled:opacity-50"
                :class="isPublic ? 'bg-primary' : 'bg-base-300'"
                :disabled="vault.actionBusy !== null"
                @click="toggleVisibility"
              >
                <span
                  class="absolute top-[2px] size-[14px] transition-[left] duration-150"
                  :class="isPublic ? 'left-[16px] bg-primary-content' : 'left-[2px] bg-faint'"
                />
              </button>
            </div>
            <div v-if="isPublic" class="mt-2.5 flex items-stretch gap-2">
              <input
                :value="publicLink"
                readonly
                class="input h-8 min-w-0 flex-1 px-2.5 text-2xs text-dim"
:aria-label="t('preview.publicLink')"
                @focus="($event.target as HTMLInputElement).select()"
              />
              <CopyButton :value="publicLink" label="publicLink" />
            </div>
          </div>

          <dl class="divide-y divide-line-soft">
            <div class="flex items-start justify-between gap-3 px-4 py-2">
              <dt class="shrink-0 text-2xs text-faint">{{ t('meta.type') }}</dt>
              <dd class="break-all text-right text-2xs text-dim">{{ mimeOf(file) }}</dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-2xs text-faint">{{ t('meta.size') }}</dt>
              <dd class="text-2xs tabular-nums text-dim">{{ formatBytes(file.object?.size) }}</dd>
            </div>
            <div v-if="dimensions" class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-2xs text-faint">{{ t('meta.dimensions') }}</dt>
              <dd class="text-2xs tabular-nums text-dim">{{ dimensions }}</dd>
            </div>
            <div v-if="duration" class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-2xs text-faint">{{ t('meta.duration') }}</dt>
              <dd class="text-2xs tabular-nums text-dim">{{ duration }}</dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-2xs text-faint">{{ t('meta.modified') }}</dt>
              <dd class="text-2xs tabular-nums text-dim">
                {{ formatDateTime(file.updated_at) }}
              </dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-2xs text-faint">{{ t('meta.created') }}</dt>
              <dd class="text-2xs tabular-nums text-dim">
                {{ formatDateTime(file.created_at) }}
              </dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-2xs text-faint">{{ t('meta.indexed') }}</dt>
              <dd class="flex items-center gap-1.5 text-2xs text-dim">
                <Checkmark16 v-if="file.indexed" class="h-3 w-3 text-success" />
                {{ file.indexed ? t('meta.yes') : t('meta.no') }}
              </dd>
            </div>
            <div class="flex items-center justify-between gap-3 px-4 py-2">
              <dt class="text-2xs text-faint">{{ t('meta.hash') }}</dt>
              <dd class="text-2xs tabular-nums text-dim" :title="file.object?.hash">
                {{ shortHash(file.object?.hash) }}
              </dd>
            </div>
          </dl>

          <div class="border-t border-line px-4 py-3">
            <button class="btn btn-outline btn-xs w-full" type="button" @click="backToFolder">
              <OpenPanelLeft16 />
              <Folder16 />
              {{ t('preview.showInFolder') }}
            </button>
          </div>
        </template>
      </aside>
    </div>
  </div>
</template>
