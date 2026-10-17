import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import type { CloudFile } from '@/lib/api/types'
import {
  LOCALE_KEY,
  THEME_KEY,
  applyLocale,
  applyTheme,
  readLocale,
  readTheme,
  type Locale,
  type Theme,
} from '@/lib/prefs'
import { i18n } from '@/lib/i18n'

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
  const locale = ref<Locale>(readLocale())

  const isDark = computed(() => theme.value === 'dark')

  function setTheme(next: Theme) {
    theme.value = next
    localStorage.setItem(THEME_KEY, next)
    applyTheme(next)
  }

  function toggleTheme() {
    setTheme(theme.value === 'dark' ? 'light' : 'dark')
  }

  function setLocale(next: Locale) {
    if (next === locale.value) return
    locale.value = next
    i18n.global.locale.value = next
    applyLocale(next)
    localStorage.setItem(LOCALE_KEY, next)
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
    locale,
    isDark,
    setTheme,
    toggleTheme,
    setLocale,
    openDialog,
    closeDialog,
    toggleSidebar,
  }
})
