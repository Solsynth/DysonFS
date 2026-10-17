<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  Menu16,
  Search16,
  SidePanelClose16,
  SidePanelOpen16,
  Upload16,
} from '@carbon/icons-vue'

import BrandMark from './BrandMark.vue'
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
const identityName = computed(() => session.identity?.nick || session.identity?.name || 'operator')

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
  window.addEventListener('dyson:upload', onUploadRequest)
})
onBeforeUnmount(() => {
  window.removeEventListener('dyson:upload', onUploadRequest)
})
</script>

<template>
  <header
    class="relative z-30 flex h-12 shrink-0 items-center gap-3 border-b border-line bg-ink/95 px-3 md:px-4"
  >
    <button
      class="btn btn-ghost btn-sm px-2 lg:hidden"
      type="button"
      aria-label="Open navigation"
      @click="ui.toggleSidebar()"
    >
      <Menu16 />
    </button>

    <RouterLink to="/drive" class="flex min-w-0 items-center gap-2.5 text-base-content" aria-label="Dyson Drive home">
      <BrandMark :size="23" />
      <span class="flex items-baseline gap-2">
        <span class="text-[12px] font-semibold tracking-[0.26em]">DYSON</span>
        <span class="hidden text-[12px] font-normal tracking-[0.16em] text-dim sm:inline">DRIVE</span>
      </span>
    </RouterLink>

    <span class="hidden h-4 w-px bg-line md:block" aria-hidden="true" />
    <span class="hidden text-3xs uppercase tracking-[0.18em] text-faint md:block">
      solar network / node 01
    </span>

    <div class="hidden flex-1 justify-center px-2 md:flex">
      <button
        class="group flex h-8 w-full max-w-[440px] items-center gap-2 border border-line bg-base-200/50 px-2.5 text-left transition-colors hover:border-faint hover:bg-base-300/60"
        type="button"
        aria-label="Search the drive or run a command"
        @click="ui.paletteOpen = true"
      >
        <Search16 class="shrink-0 text-faint transition-colors group-hover:text-dim" />
        <span class="truncate text-2xs text-faint group-hover:text-dim">
          Search the drive or run a command
        </span>
        <span class="ml-auto hidden shrink-0 items-center gap-1 text-3xs text-faint sm:flex">
          <span class="kbd">⌘</span>
          <span class="kbd">K</span>
        </span>
      </button>
    </div>

    <div class="ml-auto flex shrink-0 items-center gap-1.5">
      <span
        v-if="session.sample"
        class="hidden items-center gap-1.5 border border-brass/30 bg-brass/10 px-2 py-1 text-3xs uppercase tracking-[0.14em] text-brass md:flex"
        title="Showing sample telemetry. No live drive is connected."
      >
        <span class="dot bg-brass pulse-soft" aria-hidden="true" />
        sample
      </span>

      <button
        class="btn btn-ghost btn-sm px-2 md:hidden"
        type="button"
        aria-label="Search the drive or run a command"
        @click="ui.paletteOpen = true"
      >
        <Search16 />
      </button>

      <button
        class="btn btn-ghost btn-sm hidden px-2 sm:flex"
        type="button"
        :disabled="!inspectorEnabled"
        :aria-label="ui.dialog ? 'Close dialog' : 'Toggle inspector'"
        :title="inspectorEnabled ? 'Toggle inspector' : 'Select an object to inspect'"
        @click="vault.toggleInspector()"
      >
        <SidePanelClose16 v-if="vault.inspectorOpen" />
        <SidePanelOpen16 v-else />
      </button>

      <button
        class="btn btn-primary btn-sm"
        type="button"
        :disabled="!canUpload"
        :title="canUpload ? 'Upload files' : 'Open a drive folder to upload'"
        @click="openPicker"
      >
        <Upload16 />
        <span class="hidden sm:inline">Upload</span>
        <span
          v-if="uploads.activeCount > 0"
          class="ml-0.5 border border-primary-content/30 bg-primary-content/10 px-1 text-3xs"
        >
          {{ uploads.activeCount }}
        </span>
      </button>

      <span class="ml-1 hidden h-5 w-px bg-line sm:block" aria-hidden="true" />

      <div
        class="hidden items-center gap-2 pl-1 sm:flex"
        :title="session.identity?.is_superuser ? 'Superuser session' : 'Account session'"
      >
        <span
          class="grid size-6 place-items-center border border-line bg-base-300 text-3xs font-semibold text-dim"
          aria-hidden="true"
        >
          {{ initialOf(identityName) }}
        </span>
        <span class="hidden max-w-[120px] truncate text-2xs text-dim xl:block">
          {{ identityName }}
        </span>
      </div>

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
