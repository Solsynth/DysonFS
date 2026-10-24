import {
  authHeaders,
  buildUrl,
  DriveError,
  raiseUnauthorized,
  request,
  requestPage,
} from './client'
import { translate } from '@/lib/i18n'
import type {
  AccountIdentity,
  BreadcrumbItem,
  CloudFile,
  FilePermission,
  FilePool,
  FileOrder,
  HandoffTokenPair,
  ListQuery,
  PoolUsage,
  QuotaSummary,
  UsageSummary,
} from './types'

function listQuery(options: ListQuery): Record<string, string | number | boolean> {
  const query: Record<string, string | number | boolean> = {
    take: options.take ?? 50,
  }
  if (options.offset) query.offset = options.offset
  if (options.query?.trim()) query.query = options.query.trim()
  if (options.order) query.order = options.order
  if (options.orderDesc !== undefined) query.orderDesc = options.orderDesc
  if (options.pool_id) query.pool_id = options.pool_id
  if (options.is_folder !== undefined) query.is_folder = options.is_folder
  if (options.recycled !== undefined) query.recycled = options.recycled
  if (options.indexed !== undefined) query.indexed = options.indexed
  return query
}

export const driveApi = {
  account: () => request<AccountIdentity>('/account/me'),

  quota: () => request<QuotaSummary>('/billing/quota'),
  /**
   * Trades a single-use hand-off code for a token pair. The drive's server
   * holds the client secret; the caller only ever sees the resulting token.
   */
  redeemHandoffCode: (code: string) =>
    request<HandoffTokenPair>('/picker/handoff/redeem', { method: 'POST', body: { code } }),

  usage: () => request<UsageSummary>('/billing/usage'),
  pools: () => requestPage<FilePool>('/pools'),
  poolUsage: (poolId: string) => request<PoolUsage>(`/billing/usage/${poolId}`),

  root: (options: ListQuery = {}, signal?: AbortSignal) =>
    requestPage<CloudFile>('/files/root/children', listQuery(options), signal),

  children: (folderId: string, options: ListQuery = {}, signal?: AbortSignal) =>
    requestPage<CloudFile>(`/files/${folderId}/children`, listQuery(options), signal),

  owned: (options: ListQuery = {}, signal?: AbortSignal) =>
    requestPage<CloudFile>('/files/me', listQuery(options), signal),

  unindexed: (options: ListQuery = {}, signal?: AbortSignal) =>
    requestPage<CloudFile>('/files/unindexed', listQuery(options), signal),

  file: (fileId: string, signal?: AbortSignal) =>
    request<CloudFile>(`/files/${fileId}/info`, { signal }),

  breadcrumb: (fileId: string, signal?: AbortSignal) =>
    request<BreadcrumbItem[]>(`/files/${fileId}/breadcrumb`, { signal }),

  createFolder: (name: string, parentId?: string | null) =>
    request<CloudFile>('/files/folders', {
      method: 'POST',
      body: { name, parent_id: parentId || undefined },
    }),

  rename: (fileId: string, name: string) =>
    request<CloudFile>(`/files/${fileId}`, { method: 'PATCH', body: { name } }),

  move: (fileIds: string[], parentId: string | null, indexed = true) =>
    request<{ count: number }>('/files/move/batch', {
      method: 'POST',
      body: { file_ids: fileIds, parent_id: parentId, indexed },
    }),

  recycle: (fileIds: string[]) =>
    request<{ count: number }>('/files/recycle/batch', {
      method: 'POST',
      body: { file_ids: fileIds },
    }),

  restore: (fileIds: string[]) =>
    request<{ count: number }>('/files/restore/batch', {
      method: 'POST',
      body: { file_ids: fileIds },
    }),

  purge: (fileIds: string[]) =>
    request<{ count: number }>('/files/delete/batch', {
      method: 'POST',
      body: { file_ids: fileIds },
    }),

  permissions: (fileId: string) => request<FilePermission[]>(`/files/${fileId}/permissions`),

  setPermissions: (fileId: string, items: FilePermission[]) =>
    request<{ ok: boolean }>(`/files/${fileId}/permissions`, {
      method: 'PUT',
      body: { items },
    }),
}

export function openUrl(fileId: string, download = false): string {
  return buildUrl(`/files/${fileId}/open`, download ? { download: 1 } : undefined)
}

export function thumbnailUrl(fileId: string): string {
  return buildUrl(`/files/${fileId}`, { thumbnail: 1 })
}

export function previewUrl(fileId: string): string {
  return buildUrl(`/files/${fileId}`, {
    width: 1200,
    height: 900,
    fit: 'inside',
    format: 'webp',
  })
}

/**
 * Reads the head of a text-like file for the in-tab source preview.
 *
 * The open endpoint answers with a redirect to the storage backend, so this
 * only succeeds when that backend is same-origin or sends CORS headers. A
 * failure is reported to the caller rather than papered over: the preview
 * falls back to the file's metadata and an Open action.
 */
export async function fetchTextPreview(fileId: string, limit = 256 * 1024): Promise<string> {
  const response = await fetch(buildUrl(`/files/${fileId}/open`), { credentials: 'include' })
  if (!response.ok) {
    throw new DriveError(
      response.status,
      response.status === 403
        ? translate('apiError.cannotReadBytes')
        : translate('apiError.readFailed'),
    )
  }
  const body = await response.text()
  if (body.length <= limit) return body
  return `${body.slice(0, limit)}\n\n… truncated at ${Math.round(limit / 1024)} KB`
}

export interface UploadOptions {
  parentId?: string | null
  /**
   * Whether the new file joins the drive's folder tree. A picker's uploads are
   * unindexed: the app keeps the id, and the person's drive does not acquire a
   * copy of every file they hand to another app. Unindexed rows still carry
   * their own bytes and appear in `/files/unindexed`.
   */
  indexed?: boolean
  onProgress?: (percent: number) => void
  signal?: AbortSignal
}

/**
 * Uploads through the proxied endpoint. It accepts every storage backend,
 * unlike the presigned fast path, and returns the created file row directly.
 */
export function uploadDirect(file: File, options: UploadOptions = {}): Promise<CloudFile> {
  return new Promise((resolve, reject) => {
    const form = new FormData()
    form.set('file', file)
    form.set('index', String(options.indexed !== false))
    if (options.parentId) form.set('parent_id', options.parentId)
    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/files/upload/direct')
    xhr.withCredentials = true
    xhr.setRequestHeader('Accept', 'application/json')
    for (const [name, value] of Object.entries(authHeaders())) {
      xhr.setRequestHeader(name, value)
    }

    const abort = () => xhr.abort()
    options.signal?.addEventListener('abort', abort, { once: true })

    xhr.upload.addEventListener('progress', (event) => {
      if (!event.lengthComputable || event.total === 0) return
      options.onProgress?.(Math.round((event.loaded / event.total) * 100))
    })

    xhr.addEventListener('load', () => {
      options.signal?.removeEventListener('abort', abort)
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          const parsed = JSON.parse(xhr.responseText) as CloudFile
          options.onProgress?.(100)
          resolve(parsed)
        } catch {
          reject(new DriveError(xhr.status, translate('apiError.unreadableRecord')))
        }
        return
      }
      let message = xhr.statusText || translate('apiError.uploadFailed')
      try {
        const payload = JSON.parse(xhr.responseText) as { error?: string; message?: string }
        message = payload.message || payload.error || message
      } catch {
        // Keep the HTTP status text.
      }
      if (xhr.status === 401) raiseUnauthorized()
      reject(new DriveError(xhr.status, message))
    })

    xhr.addEventListener('error', () => {
      options.signal?.removeEventListener('abort', abort)
      reject(new DriveError(0, translate('apiError.connectionDropped')))
    })

    xhr.addEventListener('abort', () => {
      options.signal?.removeEventListener('abort', abort)
      reject(new DriveError(0, translate('apiError.uploadCancelled')))
    })

    xhr.send(form)
  })
}

export type { FileOrder }
