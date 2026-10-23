import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

/**
 * Open surfaces in the drive, in strip order. A tab is a location the operator
 * visited (a folder, the recycle bin, storage) or a file opened for preview.
 * The route path is the tab's identity, so the strip, a deep link, and a
 * reload all agree without a second source of truth.
 */
export type TabKind = 'folder' | 'preview' | 'recycle' | 'unindexed' | 'storage'

export interface DriveTab {
  /** Route path, e.g. `/drive/folder/3f9a`. */
  id: string
  kind: TabKind
  name: string
  note?: string
}

const STORAGE_KEY = 'snfs.tabs'
const LIMIT = 20

/** Previews need a live signed URL, so they never survive a reload. */
function keepable(tab: DriveTab): boolean {
  return tab.kind !== 'preview'
}

function restore(): DriveTab[] {
  const tabs: DriveTab[] = []
  const seen = new Set<string>()
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return tabs
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return tabs
    for (const entry of parsed) {
      const candidate = entry as Partial<DriveTab>
      const id = normalizeId(candidate.id)
      if (!id || seen.has(id)) continue
      if (typeof candidate.name !== 'string' || typeof candidate.kind !== 'string') continue
      seen.add(id)
      const kind = candidate.kind as TabKind
      // Older builds stored a tab per folder. Only the last visited location
      // is kept, so a restore lands on the browsing tab this build maintains.
      if (kind !== 'preview') {
        const previous = tabs.findIndex((tab) => tab.kind !== 'preview')
        if (previous >= 0) tabs.splice(previous, 1)
      }
      tabs.push({ id, kind, name: candidate.name, note: candidate.note })
    }
  } catch {
    return []
  }
  return tabs
}

/** Tabs only ever key on a path, so a restored entry drops any query. */
function normalizeId(raw: unknown): string | null {
  if (typeof raw !== 'string' || !raw.startsWith('/')) return null
  const [path] = raw.split('?')
  return path || null
}

export const useTabStore = defineStore('tabs', () => {
  const open = ref<DriveTab[]>(restore().filter(keepable))
  const previewCount = computed(() => open.value.filter((tab) => tab.kind === 'preview').length)

  function persist() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(open.value.filter(keepable)))
    } catch {
      // Preference persistence is optional.
    }
  }

  /**
   * Records the tab for the current route. Called by each view once it knows
   * what it is showing, so a folder tab carries the folder's name rather than
   * its id.
   */
  function sync(tab: DriveTab) {
    const existing = open.value.findIndex((entry) => entry.id === tab.id)
    if (existing >= 0) {
      const current = open.value[existing]
      if (current?.name === tab.name && current.note === tab.note && current.kind === tab.kind) {
        return
      }
      open.value = open.value.map((entry, index) => (index === existing ? tab : entry))
      persist()
      return
    }

    if (tab.kind === 'preview') {
      open.value = [...open.value, tab].slice(-LIMIT)
      persist()
      return
    }

    // Browsing reuses one tab: folders, My Drive, the bin, and Storage all
    // replace the same strip entry instead of accumulating one per location.
    const browsing = open.value.findIndex((entry) => entry.kind !== 'preview')
    open.value =
      browsing >= 0
        ? open.value.map((entry, index) => (index === browsing ? tab : entry))
        : [...open.value, tab]
    persist()
  }

  /** Renames the tab whose path is `id`, e.g. after a rename or a move. */
  function rename(id: string, name: string) {
    if (!open.value.some((tab) => tab.id === id)) return
    open.value = open.value.map((tab) => (tab.id === id ? { ...tab, name } : tab))
    persist()
  }

  /** Forgets a tab; returns the path that should be activated in its place. */
  function close(id: string): string | null {
    const index = open.value.findIndex((tab) => tab.id === id)
    if (index < 0) return null
    const remaining = open.value.filter((tab) => tab.id !== id)
    open.value = remaining
    persist()
    const neighbour = remaining[Math.min(index, remaining.length - 1)] ?? remaining[index - 1]
    return neighbour?.id ?? null
  }

  function closeOthers(id: string) {
    open.value = open.value.filter((tab) => tab.id === id)
    persist()
  }

  function closeAll() {
    open.value = []
    persist()
  }

  function closePreviews() {
    open.value = open.value.filter((tab) => tab.kind !== 'preview')
    persist()
  }

  /** Drag-to-reorder on the strip. */
  function reorder(from: number, to: number) {
    if (from === to) return
    const next = [...open.value]
    const [moved] = next.splice(from, 1)
    if (!moved) return
    next.splice(to, 0, moved)
    open.value = next
    persist()
  }

  return {
    open,
    previewCount,
    sync,
    rename,
    close,
    closeOthers,
    closeAll,
    closePreviews,
    reorder,
  }
})
