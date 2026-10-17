<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { Link16 } from '@carbon/icons-vue'

import FileGlyph from './FileGlyph.vue'
import type { CloudFile } from '@/lib/api/types'
import { openUrl } from '@/lib/api/drive'
import { displayName, formatBytes, kindLabel, modifiedLabel } from '@/lib/format'
import { useVaultStore } from '@/stores/vault'

const vault = useVaultStore()
const router = useRouter()

const allSelected = computed(
  () => vault.items.length > 0 && vault.selection.length === vault.items.length,
)
const someSelected = computed(
  () => vault.selection.length > 0 && vault.selection.length < vault.items.length,
)

function poolName(file: CloudFile): string {
  if (!file.pool_id) return 'default'
  return vault.pools.find((pool) => pool.id === file.pool_id)?.name ?? 'default'
}

function isPublic(file: CloudFile): boolean {
  return file.permission_status?.visibility === 'public'
}

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
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    const row = event.currentTarget as HTMLElement
    const sibling = event.key === 'ArrowDown' ? row.nextElementSibling : row.previousElementSibling
    if (sibling instanceof HTMLElement) sibling.focus()
  }
}

function toggleAll() {
  if (allSelected.value) vault.clearSelection()
  else vault.selectAll()
}

function sortBy(key: 'name' | 'size' | 'date') {
  vault.setOrder(key)
}

function indicator(key: 'name' | 'size' | 'date') {
  if (vault.order !== key) return ''
  return vault.orderDesc ? '↓' : '↑'
}

function sortState(key: 'name' | 'size' | 'date') {
  if (vault.order !== key) return 'none'
  return vault.orderDesc ? 'descending' : 'ascending'
}
</script>

<template>
  <div class="min-h-0 flex-1 overflow-auto scroll-region">
    <table class="w-full table-fixed border-collapse">
      <thead class="sticky top-0 z-10 bg-base-100">
        <tr class="border-b border-line text-[10px] uppercase tracking-[0.14em] text-faint">
          <th scope="col" class="w-9 pl-3 pr-1 text-left">
            <span class="sr-only">Select</span>
            <input
              type="checkbox"
              class="checkbox checkbox-xs rounded-[2px] border-line"
              :checked="allSelected"
              :indeterminate.prop="someSelected"
              :disabled="vault.items.length === 0"
              aria-label="Select all objects"
              @change="toggleAll"
            />
          </th>
          <th scope="col" class="min-w-0 text-left" :aria-sort="sortState('name')">
            <button
              type="button"
              class="flex h-8 w-full items-center gap-1 uppercase tracking-[0.14em] transition-colors hover:text-dim"
              :class="vault.order === 'name' ? 'text-base-content' : ''"
              @click="sortBy('name')"
            >
              Name
              <span v-if="indicator('name')" class="text-primary">{{ indicator('name') }}</span>
            </button>
          </th>
          <th scope="col" class="hidden w-[92px] text-left md:table-cell">Kind</th>
          <th
            scope="col"
            class="hidden w-[86px] pr-3 text-right sm:table-cell"
            :aria-sort="sortState('size')"
          >
            <button
              type="button"
              class="flex h-8 w-full items-center justify-end gap-1 uppercase tracking-[0.14em] transition-colors hover:text-dim"
              :class="vault.order === 'size' ? 'text-base-content' : ''"
              @click="sortBy('size')"
            >
              Size
              <span v-if="indicator('size')" class="text-primary">{{ indicator('size') }}</span>
            </button>
          </th>
          <th
            scope="col"
            class="hidden w-[124px] pr-3 text-right lg:table-cell"
            :aria-sort="sortState('date')"
          >
            <button
              type="button"
              class="flex h-8 w-full items-center justify-end gap-1 uppercase tracking-[0.14em] transition-colors hover:text-dim"
              :class="vault.order === 'date' ? 'text-base-content' : ''"
              @click="sortBy('date')"
            >
              Modified
              <span v-if="indicator('date')" class="text-primary">{{ indicator('date') }}</span>
            </button>
          </th>
        </tr>
      </thead>

      <tbody v-if="vault.loading && vault.items.length === 0">
        <tr v-for="row in 7" :key="`skeleton-${row}`" class="border-b border-line-soft">
          <td class="pl-3 py-3"><span class="block h-3 w-3 bg-base-300" /></td>
          <td class="py-3">
            <div class="flex items-center gap-2.5">
              <span class="size-6 bg-base-300" />
              <span class="h-3 w-40 bg-base-300 pulse-soft" />
            </div>
          </td>
          <td class="hidden md:table-cell"><span class="block h-3 w-12 bg-base-300" /></td>
          <td class="hidden sm:table-cell"><span class="ml-auto block h-3 w-10 bg-base-300" /></td>
          <td class="hidden lg:table-cell"><span class="ml-auto block h-3 w-14 bg-base-300" /></td>
        </tr>
      </tbody>

      <tbody v-else>
        <tr
          v-for="file in vault.items"
          :key="file.id"
          tabindex="0"
          class="group h-[46px] cursor-default border-b border-line-soft transition-colors focus-visible:bg-primary/10"
          :class="vault.selection.includes(file.id) ? 'bg-primary/10' : 'hover:bg-base-200/50'"
          :aria-selected="vault.selection.includes(file.id)"
          @click="onClick($event, file)"
          @dblclick="open(file)"
          @keydown="onKeydown($event, file)"
        >
          <td class="pl-3 pr-1">
            <input
              type="checkbox"
              class="checkbox checkbox-xs rounded-[2px] border-line"
              :checked="vault.selection.includes(file.id)"
              :aria-label="`Select ${file.name}`"
              @change="vault.select(file.id, { toggle: true })"
              @click.stop
            />
          </td>
          <td class="min-w-0 py-1.5">
            <div class="flex min-w-0 items-center gap-2.5">
              <span
                class="grid size-7 shrink-0 place-items-center border border-line-soft bg-base-200/40"
                aria-hidden="true"
              >
                <FileGlyph :file="file" />
              </span>
              <div class="min-w-0">
                <div class="flex items-center gap-1.5">
                  <span
                    class="truncate text-xs"
                    :class="
                      vault.selection.includes(file.id)
                        ? 'text-base-content'
                        : 'text-base-content/90'
                    "
                    :title="displayName(file)"
                  >
                    {{ displayName(file) }}
                  </span>
                  <Link16
                    v-if="isPublic(file)"
                    class="h-3 w-3 shrink-0 text-brass"
                    title="Published by link"
                  />
                </div>
                <div class="flex items-center gap-1.5 text-2xs text-faint">
                  <span class="truncate">{{ poolName(file) }}</span>
                  <span aria-hidden="true">·</span>
                  <span>{{ modifiedLabel(file.updated_at) }}</span>
                  <template v-if="!file.is_folder">
                    <span class="sm:hidden" aria-hidden="true">·</span>
                    <span class="sm:hidden">{{ formatBytes(file.object?.size) }}</span>
                  </template>
                </div>
              </div>
            </div>
          </td>
          <td class="hidden text-2xs uppercase tracking-[0.06em] text-faint md:table-cell">
            {{ kindLabel(file) }}
          </td>
          <td class="hidden pr-3 text-right text-2xs tabular-nums text-dim sm:table-cell">
            {{ file.is_folder ? '—' : formatBytes(file.object?.size) }}
          </td>
          <td
            class="hidden whitespace-nowrap pr-3 text-right text-2xs tabular-nums text-faint lg:table-cell"
          >
            {{ modifiedLabel(file.updated_at) }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
