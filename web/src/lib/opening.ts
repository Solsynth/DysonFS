import { useRouter } from 'vue-router'

import type { CloudFile } from './api/types'
import { openUrl } from './api/drive'
import { useVaultStore } from '@/stores/vault'

/**
 * Activating an object: a folder becomes a new tab, a file becomes a preview
 * tab. Selection lives only on the checkbox, so every click on a row, card, or
 * column entry goes through here.
 */
export function useFileOpener() {
  const router = useRouter()
  const vault = useVaultStore()

  function open(file: CloudFile) {
    if (file.is_folder) {
      void router.push({ name: 'folder', params: { id: file.id } })
      return
    }
    void router.push({ name: 'preview', params: { id: file.id } })
  }

  /** Opens the file in a browser tab instead of an in-app tab. */
  function openExternal(file: CloudFile) {
    if (file.is_folder || vault.sample) return
    window.open(openUrl(file.id), '_blank', 'noopener')
  }

  return { open, openExternal }
}
