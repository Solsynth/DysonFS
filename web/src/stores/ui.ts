import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import type { CloudFile } from '@/lib/api/types'
import { THEME_KEY, applyTheme, readTheme, type Theme } from '@/lib/prefs'

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
  const theme = ref<Theme>(readTheme())

  const isDark = computed(() => theme.value === 'dark')

  function setTheme(next: Theme) {
    theme.value = next
    localStorage.setItem(THEME_KEY, next)
    applyTheme(next)
  }

  function toggleTheme() {
    setTheme(theme.value === 'dark' ? 'light' : 'dark')
  }

  function openDialog(next: Exclude<DialogState, null>) {
    dialog.value = next
  }
  function closeDialog() {
    dialog.value = null
  }
  function toggleSidebar() {
    sidebarOpen.value = !sidebarOpen.value
  }

  return {
    sidebarOpen,
    paletteOpen,
    dialog,
    theme,
    isDark,
    setTheme,
    toggleTheme,
    openDialog,
    closeDialog,
    toggleSidebar,
  }
})
