<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import type { Component } from 'vue'
import {
  Folder16,
  FolderAdd16,
  Grid16,
  Keyboard16,
  List16,
  Renew16,
  Search16,
  SidePanelOpen16,
  StoragePool16,
  TrashCan16,
  Upload16,
  WarningAlt16,
} from '@carbon/icons-vue'

import FileGlyph from './FileGlyph.vue'
import type { CloudFile } from '@/lib/api/types'
import { openUrl } from '@/lib/api/drive'
import { formatBytes, kindLabel } from '@/lib/format'
import { useUiStore } from '@/stores/ui'
import { useVaultStore } from '@/stores/vault'

interface Command {
  id: string
  label: string
  hint: string
  group: string
  icon?: Component
  file?: CloudFile
  run: () => void
}

const ui = useUiStore()
const vault = useVaultStore()
const router = useRouter()

const query = ref('')
const selected = ref(0)
const input = ref<HTMLInputElement | null>(null)

function close() {
  ui.paletteOpen = false
  query.value = ''
  selected.value = 0
}

function go(path: string) {
  close()
  void router.push(path)
}

function openFile(file: CloudFile) {
  close()
  if (file.is_folder) {
    void router.push({ name: 'folder', params: { id: file.id } })
    return
  }
  if (vault.sample) vault.inspect(file.id)
  else window.open(openUrl(file.id), '_blank', 'noopener')
}

const commands = computed<Command[]>(() => {
  const needle = query.value.trim().toLowerCase()
  const base: Command[] = [
    {
      id: 'nav-drive',
      label: 'Go to My Drive',
      hint: 'locations',
      group: 'Navigate',
      icon: Folder16,
      run: () => go('/drive'),
    },
    {
      id: 'nav-unindexed',
      label: 'Go to Unindexed',
      hint: 'objects outside the index',
      group: 'Navigate',
      icon: WarningAlt16,
      run: () => go('/unindexed'),
    },
    {
      id: 'nav-recycle',
      label: 'Go to Recycle Bin',
      hint: 'recoverable objects',
      group: 'Navigate',
      icon: TrashCan16,
      run: () => go('/recycle'),
    },
    {
      id: 'nav-storage',
      label: 'Go to Storage',
      hint: 'capacity and pools',
      group: 'Navigate',
      icon: StoragePool16,
      run: () => go('/storage'),
    },
    {
      id: 'toggle-view',
      label: vault.viewMode === 'list' ? 'Switch to grid view' : 'Switch to list view',
      hint: 'layout',
      group: 'View',
      icon: vault.viewMode === 'list' ? Grid16 : List16,
      run: () => {
        vault.toggleView()
        close()
      },
    },
    {
      id: 'refresh',
      label: 'Refresh listing and storage',
      hint: 'read again',
      group: 'View',
      icon: Renew16,
      run: () => {
        close()
        void vault.refreshAll()
      },
    },
    {
      id: 'shortcuts',
      label: 'Keyboard shortcuts',
      hint: 'reference',
      group: 'Help',
      icon: Keyboard16,
      run: () => {
        close()
        ui.openDialog({ kind: 'shortcuts' })
      },
    },
  ]

  if (vault.inspectorOpen && vault.primarySelected) {
    base.push({
      id: 'focus-inspector',
      label: 'Focus inspector',
      hint: 'metadata and access',
      group: 'View',
      icon: SidePanelOpen16,
      run: () => {
        close()
        vault.toggleInspector()
      },
    })
  }

  if (router.currentRoute.value.meta.mode === 'drive') {
    base.unshift(
      {
        id: 'upload',
        label: 'Upload files',
        hint: 'to the current folder',
        group: 'Actions',
        icon: Upload16,
        run: () => {
          close()
          window.dispatchEvent(new Event('dyson:upload'))
        },
      },
      {
        id: 'new-folder',
        label: 'Create a new folder',
        hint: 'in the current location',
        group: 'Actions',
        icon: FolderAdd16,
        run: () => {
          close()
          ui.openDialog({ kind: 'new-folder' })
        },
      },
    )
  }

  if (!needle) return base

  const searchCommand: Command = {
    id: 'search',
    label: `Search this location for “${query.value.trim()}”`,
    hint: 'in this folder',
    group: 'Search',
    icon: Search16,
    run: () => {
      vault.setQuery(query.value.trim())
      close()
    },
  }

  const matches = vault.items
    .filter((file) => file.name.toLowerCase().includes(needle))
    .slice(0, 6)
    .map<Command>((file) => ({
      id: `file-${file.id}`,
      label: file.name,
      hint: `${kindLabel(file)} · ${file.is_folder ? `${file.children_count ?? 0} objects` : formatBytes(file.object?.size)}`,
      group: 'Objects in view',
      file,
      run: () => openFile(file),
    }))

  const filtered = base.filter(
    (command) =>
      command.label.toLowerCase().includes(needle) || command.hint.toLowerCase().includes(needle),
  )

  return [searchCommand, ...matches, ...filtered]
})

const grouped = computed(() => {
  const groups: { name: string; items: { command: Command; index: number }[] }[] = []
  commands.value.forEach((command, index) => {
    let group = groups.find((entry) => entry.name === command.group)
    if (!group) {
      group = { name: command.group, items: [] }
      groups.push(group)
    }
    group.items.push({ command, index })
  })
  return groups
})

watch(query, () => {
  selected.value = 0
})

watch(
  () => ui.paletteOpen,
  (open) => {
    if (!open) return
    query.value = ''
    selected.value = 0
    void nextTick(() => input.value?.focus())
  },
)

watch(
  () => commands.value.length,
  (length) => {
    if (selected.value >= length) selected.value = Math.max(0, length - 1)
  },
)

function move(delta: number) {
  if (commands.value.length === 0) return
  selected.value = (selected.value + delta + commands.value.length) % commands.value.length
  void nextTick(() => {
    document.getElementById(`palette-item-${selected.value}`)?.scrollIntoView({ block: 'nearest' })
  })
}

function runSelected() {
  const command = commands.value[selected.value]
  if (command) command.run()
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    move(1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    move(-1)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    runSelected()
  } else if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    close()
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
        v-if="ui.paletteOpen"
        class="fixed inset-0 z-[80] flex justify-center bg-black/60 px-3 pt-[10vh] backdrop-blur-[2px]"
        @mousedown.self="close"
      >
        <div
          class="palette-in h-fit w-full max-w-[620px] border border-line bg-base-100 shadow-[0_24px_80px_rgba(0,0,0,0.45)]"
          role="dialog"
          aria-modal="true"
          aria-label="Command palette"
        >
          <div class="flex items-center gap-2.5 border-b border-line px-3.5">
            <Search16 class="shrink-0 text-faint" />
            <input
              ref="input"
              v-model="query"
              type="text"
              class="h-12 min-w-0 flex-1 bg-transparent text-[13px] text-base-content outline-none placeholder:text-faint"
              placeholder="Search objects, or type a command…"
              autocomplete="off"
              spellcheck="false"
              aria-label="Search or run a command"
              @keydown="onKeydown"
            />
            <span class="kbd">esc</span>
          </div>

          <div class="max-h-[52vh] overflow-y-auto py-1.5">
            <div v-if="commands.length === 0" class="px-4 py-8 text-center">
              <p class="voice">No command or object matches that query.</p>
            </div>

            <template v-for="group in grouped" :key="group.name">
              <div class="px-3.5 pb-1 pt-2.5">
                <span class="eyebrow">{{ group.name }}</span>
              </div>
              <button
                v-for="entry in group.items"
                :id="`palette-item-${entry.index}`"
                :key="entry.command.id"
                type="button"
                class="flex w-full items-center gap-3 px-3.5 py-2 text-left transition-colors"
                :class="
                  selected === entry.index
                    ? 'bg-primary/10 text-base-content'
                    : 'text-dim hover:bg-base-200/60'
                "
                @mousemove="selected = entry.index"
                @click="entry.command.run()"
              >
                <span
                  class="grid size-6 shrink-0 place-items-center border border-line-soft bg-base-200/50"
                >
                  <FileGlyph v-if="entry.command.file" :file="entry.command.file" />
                  <component
                    :is="entry.command.icon"
                    v-else-if="entry.command.icon"
                    class="h-3.5 w-3.5 text-faint"
                  />
                </span>
                <span class="min-w-0 flex-1 truncate text-xs">{{ entry.command.label }}</span>
                <span class="hidden shrink-0 text-2xs text-faint sm:block">{{
                  entry.command.hint
                }}</span>
                <span v-if="selected === entry.index" class="kbd shrink-0">↵</span>
              </button>
            </template>
          </div>

          <div class="flex items-center gap-3 border-t border-line px-3.5 py-2 text-2xs text-faint">
            <span class="flex items-center gap-1"
              ><span class="kbd">↑</span><span class="kbd">↓</span> navigate</span
            >
            <span class="flex items-center gap-1"><span class="kbd">↵</span> run</span>
            <span class="ml-auto flex items-center gap-1"
              ><span class="kbd">⌘</span><span class="kbd">K</span> toggle</span
            >
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
