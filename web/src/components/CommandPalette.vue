<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import type { Component } from 'vue'
import {
  Catalog16,
  Folder16,
  FolderAdd16,
  Keyboard16,
  Renew16,
  Search16,
  StoragePool16,
  TrashCan16,
  Upload16,
  WarningAlt16,
} from '@carbon/icons-vue'

import FileGlyph from './FileGlyph.vue'
import type { CloudFile } from '@/lib/api/types'
import { formatBytes, formatCount, kindLabel } from '@/lib/format'
import { useFileOpener } from '@/lib/opening'
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
const opener = useFileOpener()
const route = useRoute()
const router = useRouter()

const { t } = useI18n()

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
  opener.open(file)
}

const commands = computed<Command[]>(() => {
  const needle = query.value.trim().toLowerCase()
  const base: Command[] = [
    {
      id: 'nav-drive',
      label: t('palette.goDrive'),
      hint: t('palette.hintLocations'),
      group: t('palette.groupNavigate'),
      icon: Folder16,
      run: () => go('/drive'),
    },
    {
      id: 'nav-unindexed',
      label: t('palette.goUnindexed'),
      hint: t('palette.hintUnindexed'),
      group: t('palette.groupNavigate'),
      icon: WarningAlt16,
      run: () => go('/unindexed'),
    },
    {
      id: 'nav-recycle',
      label: t('palette.goRecycle'),
      hint: t('palette.hintRecycle'),
      group: t('palette.groupNavigate'),
      icon: TrashCan16,
      run: () => go('/recycle'),
    },
    {
      id: 'nav-storage',
      label: t('palette.goStorage'),
      hint: t('palette.hintStorage'),
      group: t('palette.groupNavigate'),
      icon: StoragePool16,
      run: () => go('/storage'),
    },
    {
      id: 'toggle-view',
      label: t('palette.switchView', { mode: vault.viewMode }),
      hint: t('palette.hintViews'),
      group: t('palette.groupView'),
      icon: Catalog16,
      run: () => {
        vault.cycleView()
        close()
      },
    },
    {
      id: 'refresh',
      label: t('palette.refresh'),
      hint: t('palette.hintRefresh'),
      group: t('palette.groupView'),
      icon: Renew16,
      run: () => {
        close()
        void vault.refreshAll()
      },
    },
    {
      id: 'shortcuts',
      label: t('palette.shortcuts'),
      hint: t('palette.hintShortcuts'),
      group: t('palette.groupHelp'),
      icon: Keyboard16,
      run: () => {
        close()
        ui.openDialog({ kind: 'shortcuts' })
      },
    },
  ]

  if (route.meta.mode === 'drive') {
    base.unshift(
      {
        id: 'upload',
        label: t('palette.upload'),
        hint: t('palette.hintUpload'),
        group: t('palette.groupActions'),
        icon: Upload16,
        run: () => {
          close()
          window.dispatchEvent(new Event('snfs:upload'))
        },
      },
      {
        id: 'new-folder',
        label: t('palette.newFolder'),
        hint: t('palette.hintNewFolder'),
        group: t('palette.groupActions'),
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
    label: t('palette.searchFor', { query: query.value.trim() }),
    hint: t('palette.hintSearch'),
    group: t('palette.groupSearch'),
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
      hint: `${kindLabel(file)} · ${
        file.is_folder
          ? (file.children_count ?? 0) === 1
            ? t('drive.oneItem')
            : t('drive.manyItems', { n: formatCount(file.children_count ?? 0) })
          : formatBytes(file.object?.size)
      }`,
      group: t('palette.groupFiles'),
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
        class="fixed inset-0 z-[80] flex justify-center bg-black/50 px-3 pt-[10vh] backdrop-blur-sm"
        @mousedown.self="close"
      >
        <div
          class="palette-in h-fit w-full max-w-[600px] rounded-xl bg-base-200 shadow-2xl"
          role="dialog"
          aria-modal="true"
          :aria-label="t('palette.aria')"
        >
          <div class="flex items-center gap-2.5 border-b border-line px-4">
            <Search16 class="shrink-0 text-faint" />
            <input
              ref="input"
              v-model="query"
              type="text"
              class="h-[52px] min-w-0 flex-1 bg-transparent text-[13.5px] text-base-content outline-none placeholder:text-faint"
              :placeholder="t('palette.placeholder')"
              autocomplete="off"
              spellcheck="false"
              :aria-label="t('palette.searchAria')"
              @keydown="onKeydown"
            />
            <span class="kbd">esc</span>
          </div>

          <div class="max-h-[52vh] overflow-y-auto px-2 py-2">
            <div v-if="commands.length === 0" class="px-4 py-8 text-center">
              <p class="voice">{{ t('palette.empty') }}</p>
            </div>

            <template v-for="group in grouped" :key="group.name">
              <div class="px-2 pb-1 pt-2">
                <span class="text-[11px] font-medium text-faint">{{ group.name }}</span>
              </div>
              <button
                v-for="entry in group.items"
                :id="`palette-item-${entry.index}`"
                :key="entry.command.id"
                type="button"
                class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors duration-100"
                :class="
                  selected === entry.index
                    ? 'bg-primary/[0.08] text-base-content'
                    : 'text-dim hover:bg-base-300/60'
                "
                @mousemove="selected = entry.index"
                @click="entry.command.run()"
              >
                <FileGlyph v-if="entry.command.file" :file="entry.command.file" />
                <component
                  :is="entry.command.icon"
                  v-else-if="entry.command.icon"
                  class="h-4 w-4 shrink-0 text-faint"
                />
                <span class="min-w-0 flex-1 truncate text-[13px]">{{ entry.command.label }}</span>
                <span class="hidden shrink-0 text-[11px] text-faint sm:block">{{
                  entry.command.hint
                }}</span>
                <span v-if="selected === entry.index" class="kbd shrink-0">↵</span>
              </button>
            </template>
          </div>

          <div class="flex items-center gap-3 border-t border-line px-4 py-2 text-[11px] text-faint">
            <span class="flex items-center gap-1"
              ><span class="kbd">↑</span><span class="kbd">↓</span> {{ t('palette.navigate') }}</span
            >
            <span class="flex items-center gap-1"><span class="kbd">↵</span> {{ t('palette.run') }}</span>
            <span class="ml-auto flex items-center gap-1"
              ><span class="kbd">⌘</span><span class="kbd">K</span> {{ t('palette.toggle') }}</span
            >
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
