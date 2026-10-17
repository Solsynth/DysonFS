import type {
  AccountIdentity,
  BreadcrumbItem,
  CloudFile,
  FileObject,
  FilePermission,
  FilePool,
  QuotaSummary,
  UsageSummary,
} from './api/types'

const GiB = 1024 * 1024 * 1024
const MiB = 1024 * 1024

export type DemoMode = 'drive' | 'unindexed' | 'recycle'

interface DemoContext {
  query?: string
  isFolder?: boolean
  poolId?: string | null
  order?: 'date' | 'name' | 'size'
  orderDesc?: boolean
  offset?: number
  take?: number
}

let demoActive = false
const STORAGE_KEY = 'dysonfs:sample'

export function enableSampleData(active = true) {
  demoActive = active
  try {
    if (active) sessionStorage.setItem(STORAGE_KEY, '1')
    else sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    // Storage can be unavailable in hardened browsers.
  }
}

export function isSampleData(): boolean {
  if (demoActive) return true
  if (import.meta.env.VITE_DEMO === '1') return true
  try {
    if (new URLSearchParams(window.location.search).get('demo') === '1') return true
    return sessionStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    return false
  }
}

export const sampleIdentity: AccountIdentity = {
  id: 'demo-account',
  name: 'solar-ops',
  nick: 'Solar Ops',
  is_superuser: true,
  session_id: 'demo-session',
}

function object(
  size: number,
  mime: string,
  meta: Record<string, unknown> = {},
  thumbnail = false,
): FileObject {
  return {
    id: `obj-${Math.random().toString(36).slice(2, 10)}`,
    size,
    mime_type: mime,
    hash: '5f2c9a1e8b7d4c3a0e6f1b2d9c8a7e6d5f4c3b2a1d0e9f8a7b6c5d4e3f2a1b0c',
    meta,
    has_thumbnail: thumbnail,
    has_compression: false,
    created_at: '2026-10-14T09:12:00Z',
    updated_at: '2026-10-16T18:44:00Z',
  }
}

function file(partial: Partial<CloudFile> & { id: string; name: string }): CloudFile {
  return {
    account_id: 'demo-account',
    workspace_id: null,
    pool_id: 'pool-asterism',
    object_id: null,
    parent_id: null,
    indexed: true,
    is_folder: false,
    is_marked_recycle: false,
    expired_at: null,
    storage_url: null,
    storage_key: null,
    file_meta: null,
    user_meta: null,
    usage: null,
    application_type: null,
    status: 3,
    created_at: '2026-10-11T08:00:00Z',
    updated_at: '2026-10-16T18:00:00Z',
    object: null,
    children: null,
    children_count: 0,
    permission_status: {
      readable: true,
      writable: true,
      manageable: true,
      visibility: 'private',
    },
    ...partial,
  }
}

function folder(
  id: string,
  name: string,
  parentId: string | null,
  count: number,
  updatedAt: string,
  poolId = 'pool-asterism',
): CloudFile {
  return file({
    id,
    name,
    parent_id: parentId,
    is_folder: true,
    pool_id: poolId,
    children_count: count,
    updated_at: updatedAt,
    permission_status: {
      readable: true,
      writable: true,
      manageable: true,
      visibility: 'private',
    },
  })
}

function doc(
  id: string,
  name: string,
  parentId: string | null,
  size: number,
  mime: string,
  updatedAt: string,
  extra: Partial<CloudFile> = {},
  meta: Record<string, unknown> = {},
): CloudFile {
  const obj = object(size, mime, meta, mime.startsWith('image/'))
  return file({
    id,
    name,
    parent_id: parentId,
    object_id: obj.id,
    object: obj,
    updated_at: updatedAt,
    ...extra,
  })
}

export const samplePools: FilePool[] = [
  {
    id: 'pool-asterism',
    name: 'asterism',
    description: 'Primary object storage for active work.',
    account_id: 'demo-account',
    storage_config: { enable_signed: true, enable_ssl: true, endpoint: 's3.internal', bucket: 'dyson-live' },
    billing_config: { cost_multiplier: 1 },
    policy_config: { public_usable: false },
    is_hidden: false,
    created_at: '2025-04-02T10:00:00Z',
    updated_at: '2026-10-01T10:00:00Z',
  },
  {
    id: 'pool-deep-field',
    name: 'deep-field',
    description: 'Everyday archive. Slower tier, lower cost.',
    account_id: 'demo-account',
    storage_config: { enable_signed: true, enable_ssl: true, endpoint: 's3-archive.internal', bucket: 'dyson-archive' },
    billing_config: { cost_multiplier: 0.6 },
    policy_config: { public_usable: false },
    is_hidden: false,
    created_at: '2025-04-02T10:00:00Z',
    updated_at: '2026-09-18T10:00:00Z',
  },
  {
    id: 'pool-nightfall',
    name: 'nightfall',
    description: 'Cold storage for completed missions.',
    account_id: 'demo-account',
    storage_config: { enable_signed: true, enable_ssl: true, endpoint: 's3-cold.internal', bucket: 'dyson-cold' },
    billing_config: { cost_multiplier: 0.25 },
    policy_config: { public_usable: false },
    is_hidden: false,
    created_at: '2025-06-11T10:00:00Z',
    updated_at: '2026-08-02T10:00:00Z',
  },
]

export const sampleQuota: QuotaSummary = {
  based_quota: 12 * 1024,
  leveling_quota: 5 * 1024,
  perk_quota: 7 * 1024,
  extra_quota: 3 * 1024,
  total_quota: 15 * 1024,
}

export const sampleUsage: UsageSummary = {
  used_quota: 10_107,
  total_quota: 15 * 1024,
  total_file_count: 248,
  total_usage_bytes: 9.87 * GiB,
}

export const samplePoolUsage: Record<string, number> = {
  'pool-asterism': 7_420,
  'pool-deep-field': 1_750,
  'pool-nightfall': 937,
}

const seedFiles: CloudFile[] = [
  folder('fld-orbital', 'Orbital 7', null, 6, '2026-10-17T09:42:00Z'),
  folder('fld-field', 'Field Recordings', null, 3, '2026-10-15T16:08:00Z', 'pool-deep-field'),
  folder('fld-archive', 'Archive', null, 3, '2026-09-28T11:20:00Z', 'pool-deep-field'),
  folder('fld-telemetry', 'Telemetry', 'fld-orbital', 3, '2026-10-17T09:40:00Z'),

  doc(
    'file-readme',
    'README.md',
    null,
    3_260,
    'text/markdown',
    '2026-10-15T14:22:00Z',
    {},
    {},
  ),
  doc(
    'file-manifest',
    'drive-manifest.json',
    null,
    18_430,
    'application/json',
    '2026-10-14T21:10:00Z',
  ),
  doc(
    'file-array-spec',
    'solar-array-spec.pdf',
    null,
    2_516_582,
    'application/pdf',
    '2026-10-12T08:30:00Z',
  ),
  doc(
    'file-swarm',
    'dyson-swarm-concept.png',
    null,
    8_493_120,
    'image/png',
    '2026-10-16T19:05:00Z',
    {
      permission_status: {
        readable: true,
        writable: true,
        manageable: true,
        visibility: 'public',
      },
    },
    { width: 3840, height: 2160, blurhash: 'L6Pj0^i_.AyE_3t7t7R**0o#DgR4' },
  ),
  doc(
    'file-launch-window',
    'launch-window.ics',
    null,
    1_180,
    'text/calendar',
    '2026-10-17T06:18:00Z',
    { indexed: false, pool_id: 'pool-nightfall' },
  ),
  doc(
    'file-scratch',
    'scratch-notes.md',
    null,
    7_940,
    'text/markdown',
    '2026-10-17T07:02:00Z',
    { indexed: false },
  ),
  doc(
    'file-dump',
    'telemetry-dump.bin',
    null,
    132_120_576,
    'application/octet-stream',
    '2026-10-13T03:44:00Z',
    { indexed: false, pool_id: 'pool-nightfall' },
  ),

  doc(
    'file-pass-042',
    'pass-042.csv',
    'fld-orbital',
    4_812_338,
    'text/csv',
    '2026-10-17T09:38:00Z',
  ),
  doc(
    'file-anomaly',
    'anomaly-report.md',
    'fld-orbital',
    12_874,
    'text/markdown',
    '2026-10-16T22:31:00Z',
  ),
  doc(
    'file-ring-assembly',
    'ring-assembly.png',
    'fld-orbital',
    6_743_552,
    'image/png',
    '2026-10-16T17:12:00Z',
    {},
    { width: 2560, height: 1440 },
  ),
  doc(
    'file-ground-track',
    'ground-track.kml',
    'fld-orbital',
    88_204,
    'application/vnd.google-earth.kml+xml',
    '2026-10-15T13:48:00Z',
  ),
  doc(
    'file-orbit-001',
    'orbit-001.json',
    'fld-telemetry',
    2_304_002,
    'application/json',
    '2026-10-17T09:40:00Z',
  ),
  doc(
    'file-orbit-002',
    'orbit-002.json',
    'fld-telemetry',
    2_296_118,
    'application/json',
    '2026-10-17T09:42:00Z',
  ),
  doc(
    'file-thermal',
    'thermal-trace.csv',
    'fld-telemetry',
    9_654_220,
    'text/csv',
    '2026-10-16T20:22:00Z',
  ),

  doc(
    'file-dawn-chorus',
    'dawn-chorus.flac',
    'fld-field',
    51_220_480,
    'audio/flac',
    '2026-10-15T05:41:00Z',
    { pool_id: 'pool-deep-field' },
    { duration_ms: 214_800, sample_rate: 96000, channels: 2 },
  ),
  doc(
    'file-wind-tunnel',
    'wind-tunnel-03.wav',
    'fld-field',
    23_371_776,
    'audio/wav',
    '2026-10-14T12:03:00Z',
    { pool_id: 'pool-deep-field' },
    { duration_ms: 97_600, sample_rate: 48000, channels: 1 },
  ),
  doc(
    'file-site-photo',
    'site-photo.jpg',
    'fld-field',
    3_312_840,
    'image/jpeg',
    '2026-10-13T17:29:00Z',
    { pool_id: 'pool-deep-field' },
    { width: 4032, height: 3024 },
  ),

  doc(
    'file-concept-v1',
    'concept-v1.png',
    'fld-archive',
    12_884_901,
    'image/png',
    '2026-09-18T10:14:00Z',
    { pool_id: 'pool-deep-field' },
    { width: 4096, height: 2304 },
  ),
  doc(
    'file-budget',
    'budget-fy26.xlsx',
    'fld-archive',
    184_320,
    'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    '2026-09-20T09:00:00Z',
    { pool_id: 'pool-nightfall' },
  ),
  doc(
    'file-old-notes',
    'old-notes.md',
    'fld-archive',
    42_880,
    'text/markdown',
    '2026-08-30T15:20:00Z',
    { pool_id: 'pool-nightfall' },
  ),

  doc(
    'file-decommissioned',
    'decommissioned-array.png',
    null,
    5_114_112,
    'image/png',
    '2026-10-10T11:11:00Z',
    { is_marked_recycle: true },
    { width: 2048, height: 1152 },
  ),
]

export const sampleFiles: CloudFile[] = seedFiles

function compareFiles(a: CloudFile, b: CloudFile, order: DemoContext['order'], desc: boolean): number {
  // Folders hold their position above files in every sample view.
  if (a.is_folder !== b.is_folder) return a.is_folder ? -1 : 1
  let delta = 0
  if (order === 'name') delta = a.name.localeCompare(b.name, 'en', { numeric: true })
  else if (order === 'size') delta = (a.object?.size ?? 0) - (b.object?.size ?? 0)
  else delta = new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime()
  if (delta === 0) delta = a.name.localeCompare(b.name)
  return desc ? -delta : delta
}

export function sampleList(
  mode: DemoMode,
  folderId: string | null,
  context: DemoContext = {},
): { items: CloudFile[]; total: number } {
  let items = sampleFiles.filter((item) => {
    if (mode === 'recycle') return item.is_marked_recycle
    if (item.is_marked_recycle) return false
    if (mode === 'unindexed') return !item.indexed && !item.parent_id
    return (item.parent_id ?? null) === folderId
  })

  if (context.query?.trim()) {
    const needle = context.query.trim().toLowerCase()
    items = items.filter((item) => item.name.toLowerCase().includes(needle))
  }
  if (context.isFolder !== undefined) {
    items = items.filter((item) => item.is_folder === context.isFolder)
  }
  if (context.poolId) {
    items = items.filter((item) => item.pool_id === context.poolId)
  }

  items = [...items].sort((a, b) =>
    compareFiles(a, b, context.order ?? 'date', context.orderDesc ?? true),
  )

  const total = items.length
  const offset = context.offset ?? 0
  const take = context.take ?? 50
  return { items: items.slice(offset, offset + take), total }
}

export function sampleFile(id: string): CloudFile | null {
  return sampleFiles.find((item) => item.id === id) ?? null
}

export function samplePoolsFor(fileId: string): CloudFile[] {
  return sampleFiles.filter((item) => item.parent_id === fileId && !item.is_folder)
}

export function sampleBreadcrumb(folderId: string): BreadcrumbItem[] {
  const chain: BreadcrumbItem[] = []
  let current = sampleFile(folderId)
  let guard = 0
  while (current && guard < 24) {
    chain.unshift({
      id: current.id,
      name: current.name,
      parent_id: current.parent_id,
      is_folder: true,
    })
    current = current.parent_id ? sampleFile(current.parent_id) : null
    guard += 1
  }
  return chain
}

export function sampleFolders(): CloudFile[] {
  return sampleFiles.filter((item) => item.is_folder)
}

export function sampleRename(fileId: string, name: string) {
  const item = sampleFile(fileId)
  if (!item) return null
  item.name = name
  item.updated_at = new Date().toISOString()
  return item
}

export function sampleCreateFolder(name: string, parentId: string | null): CloudFile {
  const created = folder(`fld-${Date.now().toString(36)}`, name, parentId, 0, new Date().toISOString())
  sampleFiles.unshift(created)
  const parent = parentId ? sampleFile(parentId) : null
  if (parent) parent.children_count = (parent.children_count ?? 0) + 1
  return created
}

export function sampleUpload(file: File, parentId: string | null): CloudFile {
  const created = doc(
    `file-upload-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`,
    file.name,
    parentId,
    file.size,
    file.type || 'application/octet-stream',
    new Date().toISOString(),
    {},
    file.type.startsWith('image/') ? { width: 1920, height: 1080 } : {},
  )
  sampleFiles.unshift(created)
  const parent = parentId ? sampleFile(parentId) : null
  if (parent) parent.children_count = (parent.children_count ?? 0) + 1
  return created
}

export function sampleMove(ids: string[], parentId: string | null) {
  for (const id of ids) {
    const item = sampleFile(id)
    if (!item) continue
    item.parent_id = parentId
    item.updated_at = new Date().toISOString()
  }
}

export function sampleRecycle(ids: string[], recycled: boolean) {
  for (const id of ids) {
    const item = sampleFile(id)
    if (!item) continue
    item.is_marked_recycle = recycled
    item.updated_at = new Date().toISOString()
  }
}

export function samplePurge(ids: string[]) {
  for (const id of ids) {
    const index = sampleFiles.findIndex((item) => item.id === id)
    if (index < 0) continue
    const [removed] = sampleFiles.splice(index, 1)
    if (!removed || removed.is_marked_recycle || !removed.parent_id) continue
    const parent = sampleFile(removed.parent_id)
    if (parent) parent.children_count = Math.max(0, (parent.children_count ?? 0) - 1)
  }
}

export function samplePermissions(fileId: string): FilePermission[] {
  const item = sampleFile(fileId)
  return [
    {
      id: 'perm-public',
      file_id: fileId,
      subject_type: item?.permission_status?.visibility === 'public' ? 'public' : 'private',
      subject_id: '',
      permission: 'read',
    },
  ]
}

export function sampleSetVisibility(fileId: string, isPublic: boolean) {
  const item = sampleFile(fileId)
  if (!item) return
  item.permission_status = {
    ...(item.permission_status ?? {
      readable: true,
      writable: true,
      manageable: true,
      visibility: 'private',
    }),
    visibility: isPublic ? 'public' : 'private',
  }
}

export const sampleTotals = {
  bytes: 9.87 * GiB,
  quotaBytes: 15 * GiB,
  objects: 248,
  files: 1_204,
  sizeLabel: '9.87 GB',
  quotaLabel: '15.0 GB',
  pools: samplePools.length,
  poolBytes: samplePools.map((pool, index) => ({
    pool,
    bytes: [7_420 * MiB, 1_750 * MiB, 937 * MiB][index] ?? 0,
  })),
}

export { GiB, MiB }
