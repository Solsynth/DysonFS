import { defineStore } from 'pinia'
import { ref } from 'vue'

import type { CloudFile } from '@/lib/api/types'

export type DialogState =
  | { kind: 'new-folder' }
  | { kind: 'rename'; file: CloudFile }
  | { kind: 'move'; files: CloudFile[] }
  | { kind: 'recycle'; files: CloudFile[] }
  | { kind: 'purge'; files: CloudFile[] }
  | { kind: 'share'; file: CloudFile }
  | { kind: 'shortcuts' }
  | null

export const useUiStore = defineStore('ui', () => {
  const sidebarOpen = ref(false)
  const paletteOpen = ref(false)
  const dialog = ref<DialogState>(null)

  function openDialog(next: Exclude<DialogState, null>) {
    dialog.value = next
  }
  function closeDialog() {
    dialog.value = null
  }
  function toggleSidebar() {
    sidebarOpen.value = !sidebarOpen.value
  }

  return { sidebarOpen, paletteOpen, dialog, openDialog, closeDialog, toggleSidebar }
})
