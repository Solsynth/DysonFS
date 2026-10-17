<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  Menu16,
  Moon16,
  Search16,
  SidePanelClose16,
  SidePanelOpen16,
  Sun16,
  Upload16,
} from '@carbon/icons-vue'

import BrandMark from './BrandMark.vue'
import IconButton from './IconButton.vue'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import { useUploadStore } from '@/stores/upload'
import { useVaultStore } from '@/stores/vault'
import { initialOf } from '@/lib/format'

const session = useSessionStore()
const ui = useUiStore()
const uploads = useUploadStore()
const vault = useVaultStore()
const route = useRoute()

const input = ref<HTMLInputElement | null>(null)

const onDrive = computed(() => route.meta.mode === 'drive')
const canUpload = computed(() => onDrive.value)
const inspectorEnabled = computed(() => vault.primarySelected !== null)
const identityName = computed(() => session.identity?.nick || session.identity?.name || 'account')
const identityRole = computed(() =>
  session.identity?.is_superuser ? 'Superuser' : 'Solar Network account',
)

function openPicker() {
  if (!canUpload.value) return
  input.value?.click()
}

function onPicked(event: Event) {
  const target = event.target as HTMLInputElement
  const list = Array.from(target.files ?? [])
  target.value = ''
  if (list.length === 0) return
  uploads.enqueue(list, vault.folderId)
}

function onUploadRequest() {
  openPicker()
}

onMounted(() => {
  window.addEventListener('snfs:upload', onUploadRequest)
})
onBeforeUnmount(() => {
  window.removeEventListener('snfs:upload', onUploadRequest)
})
</script>

<template>
  <header
    class="relative z-30 flex h-[52px] shrink-0 items-center gap-2 border-b border-line bg-ink/95 px-3 md:px-4"
  >
    <IconButton class="lg:hidden" label="Open navigation" @click="ui.toggleSidebar()">
      <Menu16 />
    </IconButton>

    <RouterLink
      to="/drive"
      class="flex shrink-0 items-center gap-2.5 py-1"
      aria-label="SnFS home"
    >
      <BrandMark :size="21" class="text-base-content" />
      <span class="text-[14px] font-semibold tracking-[-0.01em] text-base-content">SnFS</span>
    </RouterLink>

    <div class="ml-2 hidden flex-1 md:flex">
      <button
        class="group flex h-8 w-full max-w-[360px] items-center gap-2 rounded-lg bg-base-200/70 px-3 text-left transition-colors duration-150 hover:bg-base-200"
        type="button"
        aria-label="Search files or run a command"
        @click="ui.paletteOpen = true"
      >
        <Search16 class="shrink-0 text-faint transition-colors group-hover:text-dim" />
        <span class="truncate text-[12.5px] text-faint transition-colors group-hover:text-dim">
          Search files or run a command
        </span>
        <span class="ml-auto hidden shrink-0 items-center gap-1 sm:flex">
          <span class="kbd">⌘</span>
          <span class="kbd">K</span>
        </span>
      </button>
    </div>

    <div class="ml-auto flex shrink-0 items-center gap-1">
      <span
        v-if="session.sample"
        class="mr-1 hidden items-center gap-1.5 text-[11px] text-faint lg:flex"
        title="Showing sample data. No live drive is connected."
      >
        <span class="dot bg-warning" aria-hidden="true" />
        Sample
      </span>

      <IconButton class="md:hidden" label="Search files" @click="ui.paletteOpen = true">
        <Search16 />
      </IconButton>

      <IconButton
        class="hidden sm:inline-flex"
        :label="ui.isDark ? 'Switch to light theme' : 'Switch to dark theme'"
        @click="ui.toggleTheme()"
      >
        <Sun16 v-if="ui.isDark" />
        <Moon16 v-else />
      </IconButton>

      <IconButton
        class="hidden sm:inline-flex"
        :label="inspectorEnabled ? 'Toggle inspector' : 'Select an object to inspect'"
        :disabled="!inspectorEnabled"
        @click="vault.toggleInspector()"
      >
        <SidePanelClose16 v-if="vault.inspectorOpen" />
        <SidePanelOpen16 v-else />
      </IconButton>

      <button
        class="btn btn-primary btn-sm ml-1"
        type="button"
        :disabled="!canUpload"
        :title="canUpload ? 'Upload files' : 'Open a drive folder to upload'"
        @click="openPicker"
      >
        <Upload16 />
        <span class="hidden sm:inline">Upload</span>
        <span v-if="uploads.activeCount > 0" class="text-[11px] tabular-nums opacity-80">
          {{ uploads.activeCount }}
        </span>
      </button>

      <span
        class="ml-1 grid size-7 shrink-0 place-items-center rounded-full bg-base-200 text-[11px] font-medium text-dim"
        :title="`${identityName} · ${identityRole}`"
        :aria-label="`Signed in as ${identityName}, ${identityRole}`"
      >
        {{ initialOf(identityName) }}
      </span>

      <input
        ref="input"
        type="file"
        multiple
        class="sr-only"
        tabindex="-1"
        aria-hidden="true"
        @change="onPicked"
      />
    </div>
  </header>
</template>
