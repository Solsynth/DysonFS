/**
 * Browsing state for one picker surface.
 *
 * This is deliberately not the drive's `vault` store: a picker may be open
 * beside the drive, and it must not move the drive's folder, filters, or
 * selection. Each mount gets its own instance, so two pickers on one page are
 * independent too.
 */

import { computed, ref, watch } from 'vue'

import { DriveError } from '@/lib/api/client'
import { driveApi } from '@/lib/api/drive'
import type { CloudFile } from '@/lib/api/types'
import { translate } from '@/lib/i18n'
import { isSelectable, listingFilters } from './filtering'
import type { PickerOptions } from './protocol'

export interface PickerCrumb {
  id: string | null
  name: string
}

const PAGE_SIZE = 100

function errorMessage(caught: unknown): string {
  if (caught instanceof DriveError) {
    if (caught.status === 403) return translate('picker.errors.forbidden')
    if (caught.status === 404) return translate('picker.errors.missing')
    return caught.message
  }
  if (caught instanceof Error) return caught.message
  return translate('picker.errors.unknown')
}

export function usePickerBrowser(options: () => PickerOptions) {
  const folderId = ref<string | null>(options().startFolderId ?? null)
  const crumbs = ref<PickerCrumb[]>([{ id: null, name: translate('picker.root') }])
  const items = ref<CloudFile[]>([])
  const total = ref(0)
  const loading = ref(false)
  const loadingMore = ref(false)
  const error = ref('')
  const query = ref('')

  let loadSeq = 0
  let searchTimer: number | undefined

  const hasMore = computed(() => items.value.length < total.value)
  const selectableCount = computed(
    () => items.value.filter((item) => isSelectable(item, options())).length,
  )
  const isEmpty = computed(() => !loading.value && !error.value && items.value.length === 0)

  async function load(append = false) {
    const seq = ++loadSeq
    if (append) loadingMore.value = true
    else loading.value = true
    if (!append) error.value = ''

    try {
      const listing = {
        ...listingFilters(options()),
        offset: append ? items.value.length : 0,
        take: PAGE_SIZE,
        query: query.value.trim() || undefined,
        order: 'name' as const,
        orderDesc: false,
        recycled: false,
      }
      const page = folderId.value
        ? await driveApi.children(folderId.value, listing)
        : await driveApi.root(listing)
      if (seq !== loadSeq) return
      items.value = append ? [...items.value, ...page.items] : page.items
      total.value = page.total
    } catch (caught) {
      if (seq !== loadSeq) return
      if (caught instanceof DOMException && caught.name === 'AbortError') return
      error.value = errorMessage(caught)
      if (!append) {
        items.value = []
        total.value = 0
      }
    } finally {
      if (seq === loadSeq) {
        loading.value = false
        loadingMore.value = false
      }
    }
  }

  /** Opens one folder, resolving its breadcrumb chain in parallel. */
  async function enter(id: string | null) {
    folderId.value = id
    query.value = ''
    if (id === null) {
      crumbs.value = [{ id: null, name: translate('picker.root') }]
      await load()
      return
    }

    const existing = crumbs.value.findIndex((crumb) => crumb.id === id)
    if (existing >= 0) crumbs.value = crumbs.value.slice(0, existing + 1)

    await load()
    try {
      const chain = await driveApi.breadcrumb(id)
      crumbs.value = [
        { id: null, name: translate('picker.root') },
        ...chain.map((crumb) => ({ id: crumb.id, name: crumb.name })),
      ]
    } catch {
      // The listing is already right; a missing chain only costs the trail.
    }
  }

  function jumpTo(index: number) {
    const crumb = crumbs.value[index]
    if (!crumb || crumb.id === folderId.value) return
    void enter(crumb.id)
  }

  function search(value: string) {
    query.value = value
    if (searchTimer) window.clearTimeout(searchTimer)
    searchTimer = window.setTimeout(() => {
      void load()
    }, 240)
  }

  // A new folder drops the search: a query that matched nothing here should
  // not follow the person into the next folder.
  watch(
    () => options().startFolderId,
    (next) => {
      if (next && next !== folderId.value) void enter(next)
    },
  )

  void enter(folderId.value)

  return {
    folderId,
    crumbs,
    items,
    total,
    loading,
    loadingMore,
    error,
    query,
    hasMore,
    isEmpty,
    selectableCount,
    load,
    enter,
    jumpTo,
    search,
    retry: () => load(),
    loadMore: () => load(true),
  }
}
