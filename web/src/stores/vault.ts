import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { DriveError } from '@/lib/api/client'
import { driveApi, thumbnailUrl } from '@/lib/api/drive'
import type {
  BreadcrumbItem,
  CloudFile,
  FileOrder,
  FilePermission,
  FilePool,
  KindFilter,
  QuotaSummary,
  UsageSummary,
  ViewMode,
} from '@/lib/api/types'
import {
  isSampleData,
  sampleBreadcrumb,
  sampleCreateFolder,
  sampleFile as findSampleFile,
  sampleFiles,
  sampleList,
  sampleMove,
  samplePermissions,
  samplePoolUsage,
  samplePools,
  samplePurge,
  sampleQuota,
  sampleRecycle,
  sampleRename,
  sampleSetVisibility,
  sampleUsage,
} from '@/lib/demo'
import { formatCount, formatBytes } from '@/lib/format'
import { translate } from '@/lib/i18n'

import { useNoticeStore } from './notice'

export type BrowsingMode = 'drive' | 'unindexed' | 'recycle'

export interface Crumb {
  id: string | null
  name: string
}

const VIEW_KEY = 'snfs.view'
const MiB = 1024 * 1024

const VIEW_MODES: Record<ViewMode, true> = {
  list: true,
  grid: true,
  column: true,
  waterfall: true,
}

function readView(): ViewMode {
  try {
    const stored = localStorage.getItem(VIEW_KEY) as ViewMode | null
    return stored && VIEW_MODES[stored] ? stored : 'list'
  } catch {
    return 'list'
  }
}

function errorMessage(caught: unknown): string {
  if (caught instanceof DriveError) {
    if (caught.status === 0) return caught.message
    if (caught.status === 403) return translate('notice.forbidden')
    return caught.message
  }
  if (caught instanceof Error) return caught.message
  return translate('notice.noAnswer')
}

function arrangeFoldersFirst(items: CloudFile[]): CloudFile[] {
  const folders = items.filter((item) => item.is_folder)
  const files = items.filter((item) => !item.is_folder)
  return [...folders, ...files]
}

function unique(items: CloudFile[]): CloudFile[] {
  const seen = new Set<string>()
  return items.filter((item) => {
    if (seen.has(item.id)) return false
    seen.add(item.id)
    return true
  })
}

export const useVaultStore = defineStore('vault', () => {
  const notice = useNoticeStore()

  const mode = ref<BrowsingMode>('drive')
  const folderId = ref<string | null>(null)
  const folder = ref<CloudFile | null>(null)
  const crumbs = ref<Crumb[]>([{ id: null, name: translate('nav.drive') }])

  const items = ref<CloudFile[]>([])
  const total = ref(0)
  const loading = ref(false)
  const loadingMore = ref(false)
  const error = ref('')
  const actionBusy = ref<string | null>(null)

  const query = ref('')
  const kind = ref<KindFilter>('all')
  const order = ref<FileOrder>('date')
  const orderDesc = ref(true)
  const poolFilter = ref<string | null>(null)
  const viewMode = ref<ViewMode>(readView())

  // Selection is the checkbox's business alone; activating an object is a
  // separate gesture that opens a tab.
  const selection = ref<string[]>([])
  /** The file whose tab is active: what the inspector and preview describe. */
  const previewFile = ref<CloudFile | null>(null)
  let lastSelected: string | null = null
  let searchTimer: number | undefined
  let loadSeq = 0

  const usage = ref<UsageSummary | null>(null)
  const quota = ref<QuotaSummary | null>(null)
  const pools = ref<FilePool[]>([])
  const poolUsage = ref<Record<string, number>>({})
  const volumeLoading = ref(false)
  const volumeError = ref('')
  const sample = ref(isSampleData())

  const poolRows = computed(() =>
    pools.value.map((pool) => ({
      pool,
      mb: poolUsage.value[pool.id] ?? 0,
      bytes: (poolUsage.value[pool.id] ?? 0) * MiB,
    })),
  )

  const usedBytes = computed(() => {
    if (!usage.value) return 0
    return usage.value.total_usage_bytes > 0
      ? usage.value.total_usage_bytes
      : usage.value.used_quota * MiB
  })
  const quotaBytes = computed(() => {
    const totalMb = quota.value?.total_quota ?? usage.value?.total_quota ?? 0
    return totalMb * MiB
  })
  const extraBytes = computed(() => (quota.value?.extra_quota ?? 0) * MiB)
  const baseBytes = computed(() => Math.max(0, quotaBytes.value - extraBytes.value))
  const remainingBytes = computed(() => Math.max(0, quotaBytes.value - usedBytes.value))
  const usedFraction = computed(() =>
    quotaBytes.value > 0 ? Math.min(1, usedBytes.value / quotaBytes.value) : 0,
  )
  const fileCount = computed(() => usage.value?.total_file_count ?? 0)

  const selectedItems = computed(() => {
    const ids = new Set(selection.value)
    return items.value.filter((item) => ids.has(item.id))
  })
  const selectionCount = computed(() => selection.value.length)

  const selectedBytes = computed(() =>
    selectedItems.value.reduce((sum, item) => sum + (item.object?.size ?? 0), 0),
  )
  const activePool = computed(
    () => pools.value.find((pool) => pool.id === poolFilter.value) ?? null,
  )
  const canGoUp = computed(() => (folder.value ? (folder.value.parent_id ?? null) : null))
  const isEmpty = computed(() => !loading.value && !error.value && items.value.length === 0)
  const hasMore = computed(() => items.value.length < total.value)
  const showingLabel = computed(() => {
    if (loading.value) return translate('status.reading')
    if (total.value === 0) return translate('status.noObjects')
    if (items.value.length < total.value) {
      return translate('status.objectsOf', {
        shown: formatCount(items.value.length),
        total: formatCount(total.value),
      })
    }
    return translate(total.value === 1 ? 'status.oneObject' : 'status.objectsCount', {
      n: formatCount(total.value),
    })
  })

  function storeView(next: ViewMode) {
    viewMode.value = next
    try {
      localStorage.setItem(VIEW_KEY, next)
    } catch {
      // Preference persistence is optional.
    }
  }

  const VIEW_CYCLE: ViewMode[] = ['list', 'grid', 'column', 'waterfall']

  function cycleView() {
    const index = VIEW_CYCLE.indexOf(viewMode.value)
    storeView(VIEW_CYCLE[(index + 1) % VIEW_CYCLE.length] ?? 'list')
  }

  function clearSelection() {
    selection.value = []
    lastSelected = null
  }

  /** Ticks or unticks one checkbox. Shift extends the range from the anchor. */
  function toggle(id: string, range = false) {
    if (range && lastSelected) {
      const ids = items.value.map((entry) => entry.id)
      const from = ids.indexOf(lastSelected)
      const to = ids.indexOf(id)
      if (from >= 0 && to >= 0) {
        const [start, end] = from < to ? [from, to] : [to, from]
        selection.value = ids.slice(start, end + 1)
        return
      }
    }

    const next = new Set(selection.value)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    selection.value = [...next]
    lastSelected = id
  }

  function selectAll() {
    selection.value = items.value.map((item) => item.id)
    lastSelected = selection.value[selection.value.length - 1] ?? null
  }

  /** Records the file a tab is showing, for the preview body and inspector. */
  function setPreviewFile(file: CloudFile | null) {
    previewFile.value = file
  }

  function patchItem(id: string, changes: Partial<CloudFile>) {
    items.value = items.value.map((item) => (item.id === id ? { ...item, ...changes } : item))
    if (previewFile.value?.id === id) previewFile.value = { ...previewFile.value, ...changes }
  }

  function buildOptions(offset: number) {
    const options = {
      offset,
      take: 50,
      query: query.value,
      order: order.value,
      orderDesc: orderDesc.value,
      pool_id: poolFilter.value ?? undefined,
      is_folder: kind.value === 'folders' ? true : kind.value === 'files' ? false : undefined,
    }
    if (mode.value === 'recycle') return { ...options, recycled: true }
    if (mode.value === 'unindexed') return { ...options, recycled: false }
    return { ...options, recycled: false }
  }

  async function fetchPage(offset: number, signal?: AbortSignal) {
    const options = buildOptions(offset)
    if (sample.value) {
      const result = sampleList(mode.value, folderId.value, {
        query: options.query,
        isFolder: options.is_folder,
        poolId: options.pool_id ?? null,
        order: options.order,
        orderDesc: options.orderDesc,
        offset,
        take: options.take,
      })
      return result
    }
    if (mode.value === 'recycle') return driveApi.owned(options, signal)
    if (mode.value === 'unindexed') return driveApi.unindexed(options, signal)
    return folderId.value
      ? driveApi.children(folderId.value, options, signal)
      : driveApi.root(options, signal)
  }

  async function load() {
    const seq = ++loadSeq
    loading.value = true
    error.value = ''
    try {
      const page = await fetchPage(0)
      if (seq !== loadSeq) return
      items.value = arrangeFoldersFirst(page.items)
      total.value = page.total
      // Selections do not survive a reload of the listing; the tab's file is
      // refreshed separately by the preview view.
      clearSelection()
    } catch (caught) {
      if (seq !== loadSeq) return
      if (caught instanceof DOMException && caught.name === 'AbortError') return
      error.value = errorMessage(caught)
      items.value = []
      total.value = 0
    } finally {
      if (seq === loadSeq) loading.value = false
    }
  }

  async function loadMore() {
    if (loading.value || loadingMore.value || !hasMore.value) return
    loadingMore.value = true
    try {
      const page = await fetchPage(items.value.length)
      const merged = unique([...items.value, ...page.items])
      items.value = arrangeFoldersFirst(merged)
      total.value = page.total
    } catch (caught) {
      notice.error(errorMessage(caught))
    } finally {
      loadingMore.value = false
    }
  }

  async function open(nextMode: BrowsingMode, nextFolderId: string | null = null) {
    sample.value = isSampleData()
    mode.value = nextMode
    folderId.value = nextFolderId
    query.value = ''
    kind.value = 'all'
    if (nextMode !== 'drive') poolFilter.value = null
    clearSelection()

    if (nextMode !== 'drive') {
      folder.value = null
      crumbs.value = [
        {
          id: null,
          name: translate(nextMode === 'unindexed' ? 'nav.unindexed' : 'nav.recycle'),
        },
      ]
      await load()
      return
    }

    if (!nextFolderId) {
      folder.value = null
      crumbs.value = [{ id: null, name: translate('nav.drive') }]
      await load()
      return
    }

    loading.value = true
    error.value = ''
    try {
      if (sample.value) {
        const found = findSampleFile(nextFolderId)
        if (!found || !found.is_folder) {
          throw new DriveError(404, 'This folder is not in the drive.')
        }
        folder.value = found
        crumbs.value = [
          { id: null, name: translate('nav.drive') },
          ...sampleBreadcrumb(nextFolderId).map((crumb: BreadcrumbItem) => ({
            id: crumb.id,
            name: crumb.name,
          })),
        ]
      } else {
        const [info, chain] = await Promise.all([
          driveApi.file(nextFolderId),
          driveApi.breadcrumb(nextFolderId),
        ])
        folder.value = info
        crumbs.value = [
          { id: null, name: translate('nav.drive') },
          ...chain.map((crumb) => ({ id: crumb.id, name: crumb.name })),
        ]
      }
    } catch (caught) {
      error.value = errorMessage(caught)
      folder.value = null
      crumbs.value = [{ id: null, name: translate('nav.drive') }]
    } finally {
      loading.value = false
    }
    await load()
  }

  async function loadVolume() {
    volumeLoading.value = true
    volumeError.value = ''
    try {
      if (sample.value || isSampleData()) {
        usage.value = sampleUsage
        quota.value = sampleQuota
        pools.value = samplePools
        poolUsage.value = { ...samplePoolUsage }
        return
      }

      const [usageResult, quotaResult, poolsResult] = await Promise.allSettled([
        driveApi.usage(),
        driveApi.quota(),
        driveApi.pools(),
      ])

      if (usageResult.status === 'fulfilled') usage.value = usageResult.value
      if (quotaResult.status === 'fulfilled') quota.value = quotaResult.value
      if (poolsResult.status === 'fulfilled') {
        pools.value = poolsResult.value.items.filter((pool) => !pool.is_hidden)
      }

      const entries = await Promise.allSettled(
        pools.value.map(async (pool) => {
          const usageResult = await driveApi.poolUsage(pool.id)
          return [pool.id, usageResult.total_quota] as const
        }),
      )
      const next: Record<string, number> = {}
      for (const entry of entries) {
        if (entry.status === 'fulfilled') {
          const [id, mb] = entry.value
          next[id] = mb
        }
      }
      poolUsage.value = next

      if (
        usageResult.status === 'rejected' &&
        quotaResult.status === 'rejected' &&
        poolsResult.status === 'rejected'
      ) {
        volumeError.value = errorMessage(usageResult.reason)
      }
    } catch (caught) {
      volumeError.value = errorMessage(caught)
    } finally {
      volumeLoading.value = false
    }
  }

  async function refreshAll() {
    await Promise.all([load(), loadVolume()])
  }

  function setQuery(value: string) {
    query.value = value
    if (searchTimer) window.clearTimeout(searchTimer)
    if (!value.trim()) {
      void load()
      return
    }
    searchTimer = window.setTimeout(() => {
      void load()
    }, 240)
  }

  function setKind(next: KindFilter) {
    if (kind.value === next) return
    kind.value = next
    void load()
  }

  function setOrder(next: FileOrder) {
    if (order.value === next) {
      orderDesc.value = !orderDesc.value
    } else {
      order.value = next
      orderDesc.value = next === 'name' ? false : true
    }
    void load()
  }

  function setPoolFilter(poolId: string | null) {
    poolFilter.value = poolId
    void load()
  }

  function setFolderName(name: string) {
    if (!folder.value) return
    folder.value = { ...folder.value, name }
    const last = crumbs.value[crumbs.value.length - 1]
    if (last) last.name = name
  }

  async function rename(file: CloudFile, name: string) {
    const trimmed = name.trim()
    if (!trimmed || trimmed === file.name) return
    actionBusy.value = 'rename'
    try {
      if (sample.value) sampleRename(file.id, trimmed)
      else await driveApi.rename(file.id, trimmed)
      if (folder.value?.id === file.id) setFolderName(trimmed)
      notice.ok(translate('notice.renamed', { name: trimmed }))
      await load()
    } catch (caught) {
      notice.error(errorMessage(caught))
      throw caught
    } finally {
      actionBusy.value = null
    }
  }

  async function createFolder(name: string) {
    const trimmed = name.trim()
    if (!trimmed) return
    actionBusy.value = 'new-folder'
    try {
      if (sample.value) sampleCreateFolder(trimmed, folderId.value)
      else await driveApi.createFolder(trimmed, folderId.value)
      notice.ok(translate('notice.folderCreated', { name: trimmed }))
      await Promise.all([load(), loadVolume()])
    } catch (caught) {
      notice.error(errorMessage(caught))
      throw caught
    } finally {
      actionBusy.value = null
    }
  }

  async function move(files: CloudFile[], parentId: string | null) {
    if (files.length === 0) return
    const ids = files.map((file) => file.id)
    actionBusy.value = 'move'
    try {
      if (sample.value) sampleMove(ids, parentId)
      else await driveApi.move(ids, parentId)
      notice.ok(
        files.length === 1
          ? translate('notice.oneMoved', { name: files[0]?.name ?? translate('dialog.object') })
          : translate('notice.manyMoved', { n: formatCount(files.length) }),
      )
      clearSelection()
      await load()
    } catch (caught) {
      notice.error(errorMessage(caught))
      throw caught
    } finally {
      actionBusy.value = null
    }
  }

  async function recycle(files: CloudFile[]) {
    if (files.length === 0) return
    const ids = files.map((file) => file.id)
    actionBusy.value = 'recycle'
    try {
      if (sample.value) sampleRecycle(ids, true)
      else await driveApi.recycle(ids)
      notice.ok(
        files.length === 1
          ? translate('notice.oneRecycled', {
              name: files[0]?.name ?? translate('dialog.object'),
            })
          : translate('notice.manyRecycled', { n: formatCount(files.length) }),
      )
      clearSelection()
      await Promise.all([load(), loadVolume()])
    } catch (caught) {
      notice.error(errorMessage(caught))
      throw caught
    } finally {
      actionBusy.value = null
    }
  }

  async function restore(files: CloudFile[]) {
    if (files.length === 0) return
    const ids = files.map((file) => file.id)
    actionBusy.value = 'restore'
    try {
      if (sample.value) sampleRecycle(ids, false)
      else await driveApi.restore(ids)
      notice.ok(
        files.length === 1
          ? translate('notice.oneRestored', {
              name: files[0]?.name ?? translate('dialog.object'),
            })
          : translate('notice.manyRestored', { n: formatCount(files.length) }),
      )
      clearSelection()
      await Promise.all([load(), loadVolume()])
    } catch (caught) {
      notice.error(errorMessage(caught))
      throw caught
    } finally {
      actionBusy.value = null
    }
  }

  async function purge(files: CloudFile[]) {
    if (files.length === 0) return
    const ids = files.map((file) => file.id)
    actionBusy.value = 'purge'
    try {
      if (sample.value) samplePurge(ids)
      else await driveApi.purge(ids)
      notice.ok(
        files.length === 1
          ? translate('notice.onePurged', {
              name: files[0]?.name ?? translate('dialog.object'),
            })
          : translate('notice.manyPurged', { n: formatCount(files.length) }),
      )
      clearSelection()
      await Promise.all([load(), loadVolume()])
    } catch (caught) {
      notice.error(errorMessage(caught))
      throw caught
    } finally {
      actionBusy.value = null
    }
  }

  async function permissionsFor(file: CloudFile): Promise<FilePermission[]> {
    if (sample.value) return samplePermissions(file.id)
    return driveApi.permissions(file.id)
  }

  async function setVisibility(file: CloudFile, isPublic: boolean) {
    actionBusy.value = 'visibility'
    try {
      const current = await permissionsFor(file)
      const next = current.filter(
        (permission) =>
          permission.subject_type !== 'public' && permission.subject_type !== 'private',
      )
      next.push({
        id: undefined,
        file_id: file.id,
        subject_type: isPublic ? 'public' : 'private',
        subject_id: '',
        permission: 'read',
      })
      if (sample.value) sampleSetVisibility(file.id, isPublic)
      else await driveApi.setPermissions(file.id, next)
      notice.ok(translate(isPublic ? 'notice.published' : 'notice.unpublished'))
      await load()
    } catch (caught) {
      notice.error(errorMessage(caught))
      throw caught
    } finally {
      actionBusy.value = null
    }
  }

  async function listFolders(parentId: string | null): Promise<CloudFile[]> {
    const options = {
      is_folder: true,
      take: 200,
      order: 'name' as const,
      orderDesc: false,
      recycled: false,
    }
    if (sample.value) {
      return sampleFiles.filter(
        (file) =>
          file.is_folder && !file.is_marked_recycle && (file.parent_id ?? null) === parentId,
      )
    }
    const page = parentId
      ? await driveApi.children(parentId, options)
      : await driveApi.root(options)
    return page.items
  }

  function previewFor(file: CloudFile): string {
    return thumbnailUrl(file.id)
  }

  function errorSummary(): string {
    if (volumeError.value) return volumeError.value
    return `${formatBytes(usedBytes.value)} of ${formatBytes(quotaBytes.value)} used`
  }

  return {
    mode,
    folderId,
    folder,
    crumbs,
    items,
    total,
    loading,
    loadingMore,
    error,
    actionBusy,
    query,
    kind,
    order,
    orderDesc,
    poolFilter,
    viewMode,
    selection,
    previewFile,
    usage,
    quota,
    pools,
    poolUsage,
    poolRows,
    volumeLoading,
    volumeError,
    sample,
    usedBytes,
    quotaBytes,
    extraBytes,
    baseBytes,
    remainingBytes,
    usedFraction,
    fileCount,
    selectedItems,
    selectionCount,
    selectedBytes,
    activePool,
    canGoUp,
    isEmpty,
    hasMore,
    showingLabel,
    open,
    load,
    loadMore,
    refreshAll,
    loadVolume,
    setQuery,
    setKind,
    setOrder,
    setPoolFilter,
    toggle,
    selectAll,
    clearSelection,
    setPreviewFile,
    patchItem,
    cycleView,
    storeView,
    rename,
    createFolder,
    move,
    recycle,
    restore,
    purge,
    permissionsFor,
    setVisibility,
    listFolders,
    previewFor,
    errorSummary,
    errorMessage,
  }
})
