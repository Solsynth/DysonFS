<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { CopyLink16 } from '@carbon/icons-vue'

import FileGlyph from './FileGlyph.vue'
import type { CloudFile } from '@/lib/api/types'
import { openUrl, previewUrl } from '@/lib/api/drive'
import { displayName, formatBytes, kindLabel, modifiedLabel } from '@/lib/format'
import { hasPreview } from '@/lib/fileKinds'
import { useVaultStore } from '@/stores/vault'

const vault = useVaultStore()
const router = useRouter()

const failed = ref<string[]>([])

function onClick(event: MouseEvent, file: CloudFile) {
  const target = event.target as HTMLElement | null
  if (target?.closest('input, button, a')) return
  vault.select(file.id, {
    toggle: event.metaKey || event.ctrlKey,
    range: event.shiftKey,
  })
}

function open(file: CloudFile) {
  if (file.is_folder) {
    void router.push({ name: 'folder', params: { id: file.id } })
    return
  }
  if (vault.sample) {
    vault.inspect(file.id)
    return
  }
  window.open(openUrl(file.id), '_blank', 'noopener')
}

function onKeydown(event: KeyboardEvent, file: CloudFile) {
  if (event.key === 'Enter') {
    event.preventDefault()
    open(file)
    return
  }
  if (event.key === ' ') {
    event.preventDefault()
    vault.select(file.id, { toggle: event.metaKey || event.ctrlKey })
    return
  }
  if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') {
    event.preventDefault()
    const card = event.currentTarget as HTMLElement
    const sibling = event.key === 'ArrowRight' ? card.nextElementSibling : card.previousElementSibling
    if (sibling instanceof HTMLElement) sibling.focus()
  }
}

function showPreview(file: CloudFile): boolean {
  return !vault.sample && hasPreview(file) && !failed.value.includes(file.id)
}

function markFailed(id: string) {
  if (!failed.value.includes(id)) failed.value = [...failed.value, id]
}

function isPublic(file: CloudFile): boolean {
  return file.permission_status?.visibility === 'public'
}
</script>

<template>
  <div class="min-h-0 flex-1 overflow-auto scroll-region p-3 md:p-4">
    <div
      class="grid grid-cols-2 gap-2.5 sm:grid-cols-3 xl:grid-cols-4 2xl:grid-cols-5"
      role="list"
      aria-label="Objects"
    >
      <article
        v-for="file in vault.items"
        :key="file.id"
        tabindex="0"
        role="listitem"
        class="group relative flex cursor-default flex-col border transition-colors focus:outline-none"
        :class="
          vault.selection.includes(file.id)
            ? 'border-primary/70 bg-primary/[0.08]'
            : 'border-line bg-base-200/25 hover:border-faint/60 hover:bg-base-200/50'
        "
        :aria-selected="vault.selection.includes(file.id)"
        @click="onClick($event, file)"
        @dblclick="open(file)"
        @keydown="onKeydown($event, file)"
      >
        <div class="relative aspect-[4/3] overflow-hidden border-b border-line-soft bg-base-200/60">
          <img
            v-if="showPreview(file)"
            :src="previewUrl(file.id)"
            :alt="''"
            loading="lazy"
            decoding="async"
            class="h-full w-full object-cover opacity-90 transition-opacity duration-200 group-hover:opacity-100"
            @error="markFailed(file.id)"
          />
          <div v-else class="relative grid h-full place-items-center">
            <div class="absolute inset-0 opacity-40 ruler" aria-hidden="true" />
            <FileGlyph :file="file" size="lg" class="relative opacity-80" />
          </div>

          <input
            type="checkbox"
            class="checkbox checkbox-xs absolute left-2 top-2 rounded-[2px] border-line bg-ink/80 opacity-0 transition-opacity focus-visible:opacity-100 group-hover:opacity-100"
            :class="vault.selection.includes(file.id) ? 'opacity-100' : ''"
            :checked="vault.selection.includes(file.id)"
            :aria-label="`Select ${file.name}`"
            @change="vault.select(file.id, { toggle: true })"
            @click.stop
          />

          <span
            v-if="isPublic(file)"
            class="absolute right-2 top-2 grid size-5 place-items-center border border-brass/40 bg-ink/85 text-brass"
            title="Published by link"
          >
            <CopyLink16 class="h-3 w-3" />
          </span>

          <span
            v-if="file.is_folder && (file.children_count ?? 0) > 0"
            class="absolute bottom-2 right-2 border border-line bg-ink/85 px-1.5 py-0.5 text-3xs tabular-nums text-dim"
          >
            {{ file.children_count }}
          </span>
        </div>

        <div class="flex min-w-0 flex-col gap-1 p-2.5">
          <span class="truncate text-xs text-base-content" :title="displayName(file)">
            {{ displayName(file) }}
          </span>
          <span class="flex items-center gap-1.5 text-3xs text-faint">
            <span class="uppercase tracking-[0.06em]">{{ kindLabel(file) }}</span>
            <span aria-hidden="true">·</span>
            <span>{{ file.is_folder ? modifiedLabel(file.updated_at) : formatBytes(file.object?.size) }}</span>
          </span>
        </div>
      </article>
    </div>
  </div>
</template>
