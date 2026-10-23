import { useRouter } from 'vue-router'

import type { CloudFile } from './api/types'

/**
 * Activating an object: a folder becomes the browsing tab, a file becomes a
 * preview tab. Selection lives only on the checkbox, so every click on a row,
 * card, or column entry goes through here.
 */
export function useFileOpener() {
  const router = useRouter()

  function open(file: CloudFile) {
    if (file.is_folder) {
      void router.push({ name: 'folder', params: { id: file.id } })
      return
    }
    void router.push({ name: 'preview', params: { id: file.id } })
  }

  return { open }
}
