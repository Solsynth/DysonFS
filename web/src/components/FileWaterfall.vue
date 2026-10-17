<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ArrowDown16 } from '@carbon/icons-vue'

import FileGlyph from './FileGlyph.vue'
import type { CloudFile } from '@/lib/api/types'
import { previewUrl, thumbnailUrl } from '@/lib/api/drive'
import { fileKind, hasPreview } from '@/lib/fileKinds'
import { displayName, formatBytes, formatCount, kindLabel, parseMeta } from '@/lib/format'
import { useFileOpener } from '@/lib/opening'
import { useVaultStore } from '@/stores/vault'

interface Tile {
  file: CloudFile
  /** The media box's real proportions, or the 4:3 fallback. */
  ratio: string
  /** The image to paint, or null when the kind glyph stands in for it. */
  url: string | null
}

const vault = useVaultStore()
const { open } = useFileOpener()
const { t } = useI18n()

/** Tiles whose media refused to load fall back to the kind glyph. */
const broken = ref(new Set<string>())

// A folder is not a picture: it becomes a chip in the band above the waterfall
// rather than a tile that would break the column rhythm.
const folders = computed(() => vault.items.filter((item) => item.is_folder))

const tiles = computed<Tile[]>(() =>
  vault.items
    .filter((item) => !item.is_folder)
    .map((file) => ({
      file,
      ratio: mediaRatio(file),
      url: mediaUrl(file),
    })),
)

/**
 * The one inline style the waterfall permits. A panorama or a needle-wide
 * strip would dwarf or erase its neighbours, so the ratio is clamped to the
 * same range the drive uses; otherwise the stored pixel proportions carry,
 * and 4:3 is the last resort.
 */
function mediaRatio(file: CloudFile): string {
  const meta = parseMeta(file)
  const width = Number(meta.width ?? 0)
  const height = Number(meta.height ?? 0)
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return '4 / 3'
  }
  return `${Math.min(4, Math.max(0.35, width / height))} / 1`
}

/** Pictures get the wider derivative; anything else uses the small thumbnail. */
function mediaUrl(file: CloudFile): string | null {
  if (broken.value.has(file.id) || !hasPreview(file)) return null
  return fileKind(file).group === 'image' ? previewUrl(file.id) : thumbnailUrl(file.id)
}

function markBroken(id: string) {
  // A fresh Set, because mutating one in place would not repaint the tile.
  broken.value = new Set([...broken.value, id])
}

function isSelected(file: CloudFile): boolean {
  return vault.selection.includes(file.id)
}

/** Arrow keys walk the masonry in document order, which is column flow here. */
const ARROW_STEP: Record<string, number> = {
  ArrowDown: 1,
  ArrowRight: 1,
  ArrowUp: -1,
  ArrowLeft: -1,
}

function onKeydown(event: KeyboardEvent, file: CloudFile) {
  if (event.key === 'Enter') {
    event.preventDefault()
    open(file)
    return
  }
  if (event.key === ' ') {
    event.preventDefault()
    vault.toggle(file.id)
    return
  }
  const step = ARROW_STEP[event.key]
  if (step === undefined) return
  event.preventDefault()
  const tile = event.currentTarget as HTMLElement
  const sibling = step > 0 ? tile.nextElementSibling : tile.previousElementSibling
  if (sibling instanceof HTMLElement) sibling.focus()
}
</script>

<template>
  <div class="min-h-0 flex-1 overflow-auto scroll-region p-3 md:p-5">
    <section v-if="folders.length > 0" class="mb-4" :aria-label="t('drive.kindFolders')">
      <h2 class="eyebrow">{{ t('drive.kindFolders') }}</h2>
      <div class="mt-2 flex flex-wrap gap-1.5">
        <button
          v-for="folder in folders"
          :key="folder.id"
          type="button"
          class="chip max-w-full transition-colors hover:bg-base-200/60 hover:text-base-content focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
          :title="displayName(folder)"
          @click="open(folder)"
        >
          <FileGlyph :file="folder" />
          <span class="max-w-[15rem] truncate">{{ displayName(folder) }}</span>
          <span class="tabular-nums text-faint">{{ formatCount(folder.children_count ?? 0) }}</span>
        </button>
      </div>
    </section>

    <div
      class="columns-2 gap-2.5 sm:columns-3 lg:columns-4 xl:columns-5 2xl:columns-6"
      role="list"
      :aria-label="t('drive.objects')"
    >
      <article
        v-for="tile in tiles"
        :key="tile.file.id"
        tabindex="0"
        role="listitem"
        class="group relative mb-2.5 cursor-default break-inside-avoid rounded-[2px] border border-line bg-base-200/30 transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        :class="isSelected(tile.file) ? 'border-primary/40' : 'hover:bg-base-200/60'"
        :aria-selected="isSelected(tile.file)"
        @click="open(tile.file)"
        @keydown="onKeydown($event, tile.file)"
      >
        <div class="relative overflow-hidden bg-base-300/40" :style="{ aspectRatio: tile.ratio }">
          <img
            v-if="tile.url"
            :src="tile.url"
            alt=""
            loading="lazy"
            decoding="async"
            class="h-full w-full object-cover"
            @error="markBroken(tile.file.id)"
          />
          <div v-else class="grid h-full w-full place-items-center">
            <FileGlyph :file="tile.file" size="lg" />
          </div>

          <input
            type="checkbox"
            class="checkbox checkbox-xs absolute left-2 top-2 rounded-[2px] bg-ink/80 transition-opacity duration-150"
            :class="
              isSelected(tile.file)
                ? 'opacity-100'
                : 'opacity-0 focus-visible:opacity-100 group-hover:opacity-100'
            "
            :checked="isSelected(tile.file)"
            :aria-label="t('drive.selectObject', { name: displayName(tile.file) })"
            @change="vault.toggle(tile.file.id, ($event as MouseEvent).shiftKey)"
            @click.stop
          />
        </div>

        <div class="flex min-w-0 flex-col gap-0.5 px-2 py-1.5">
          <span class="truncate text-2xs text-base-content" :title="displayName(tile.file)">
            {{ displayName(tile.file) }}
          </span>
          <span class="truncate text-2xs text-faint tabular-nums">
            {{ kindLabel(tile.file) }} · {{ formatBytes(tile.file.object?.size) }}
          </span>
        </div>
      </article>
    </div>

    <footer v-if="vault.hasMore" class="flex items-center justify-center pt-2">
      <button
        class="btn btn-ghost btn-sm"
        type="button"
        :disabled="vault.loadingMore"
        @click="vault.loadMore()"
      >
        {{ vault.loadingMore ? t('drive.loadingMore') : t('drive.loadMore') }}
        <ArrowDown16 aria-hidden="true" />
      </button>
    </footer>
  </div>
</template>
