<script setup lang="ts">
import { onBeforeUnmount, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'

import AppSidebar from './AppSidebar.vue'
import CommandPalette from './CommandPalette.vue'
import DialogHost from './DialogHost.vue'
import InspectorPanel from './InspectorPanel.vue'
import StatusBar from './StatusBar.vue'
import TitleBar from './TitleBar.vue'
import ToastStack from './ToastStack.vue'
import UploadDock from './UploadDock.vue'
import { useUiStore } from '@/stores/ui'
import { useUploadStore } from '@/stores/upload'
import { useVaultStore } from '@/stores/vault'

const ui = useUiStore()
const vault = useVaultStore()
const uploads = useUploadStore()
const route = useRoute()

function typing(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  return ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
}

function onKeydown(event: KeyboardEvent) {
  const mod = event.metaKey || event.ctrlKey

  if (mod && event.key.toLowerCase() === 'k') {
    event.preventDefault()
    ui.paletteOpen = !ui.paletteOpen
    return
  }

  if (event.key === 'Escape') {
    if (ui.paletteOpen) {
      ui.paletteOpen = false
      return
    }
    if (ui.dialog) {
      ui.closeDialog()
      return
    }
    if (vault.inspectorOpen) {
      vault.closeInspector()
      return
    }
    if (ui.sidebarOpen) ui.sidebarOpen = false
    return
  }

  if (typing(event.target) || ui.paletteOpen || ui.dialog) return

  const onDrive = route.meta.mode === 'drive'
  const inRecycle = route.meta.mode === 'recycle'

  if (event.key === '?' || (event.shiftKey && event.key === '/')) {
    event.preventDefault()
    ui.openDialog({ kind: 'shortcuts' })
    return
  }

  if ((event.key === 'n' || event.key === 'N') && onDrive) {
    event.preventDefault()
    ui.openDialog({ kind: 'new-folder' })
    return
  }

  if ((event.key === 'u' || event.key === 'U') && onDrive) {
    event.preventDefault()
    window.dispatchEvent(new Event('dyson:upload'))
    return
  }

  if (event.key === 'Delete' && vault.selectedItems.length > 0) {
    event.preventDefault()
    if (inRecycle) ui.openDialog({ kind: 'purge', files: [...vault.selectedItems] })
    else ui.openDialog({ kind: 'recycle', files: [...vault.selectedItems] })
  }
}

watch(
  () => route.fullPath,
  () => {
    ui.sidebarOpen = false
  },
)

onMounted(() => {
  void vault.loadVolume()
  window.addEventListener('keydown', onKeydown)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  uploads.reset()
})
</script>

<template>
  <div class="flex h-dvh flex-col overflow-hidden bg-ink text-base-content">
    <a
      href="#main"
      class="sr-only focus:not-sr-only focus:absolute focus:left-3 focus:top-3 focus:z-[90] focus:border focus:border-line focus:bg-base-300 focus:px-3 focus:py-2 focus:text-xs"
    >
      Skip to content
    </a>

    <TitleBar />

    <div class="flex min-h-0 flex-1">
      <AppSidebar />
      <main id="main" class="relative flex min-w-0 flex-1 flex-col">
        <RouterView v-slot="{ Component }">
          <Transition
            mode="out-in"
            enter-active-class="transition duration-150 ease-out"
            enter-from-class="opacity-0 translate-y-[2px]"
            leave-active-class="transition duration-100 ease-in"
            leave-to-class="opacity-0"
          >
            <component :is="Component" />
          </Transition>
        </RouterView>
      </main>
      <InspectorPanel />
    </div>

    <StatusBar />
    <CommandPalette />
    <DialogHost />
    <UploadDock />
    <ToastStack />
  </div>
</template>
