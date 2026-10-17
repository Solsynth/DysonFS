import { buildUrl, DriveError, raiseUnauthorized, request, requestPage } from './client'
import type {
  AccountIdentity,
  BreadcrumbItem,
  CloudFile,
  FilePermission,
  FilePool,
  FileOrder,
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

  permissions: (fileId: string) =>
    request<FilePermission[]>(`/files/${fileId}/permissions`),

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

export interface UploadOptions {
  parentId?: string | null
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
    form.set('index', 'true')
    if (options.parentId) form.set('parent_id', options.parentId)

    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/files/upload/direct')
    xhr.withCredentials = true
    xhr.setRequestHeader('Accept', 'application/json')

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
          reject(new DriveError(xhr.status, 'The upload finished but the file record was unreadable.'))
        }
        return
      }
      let message = xhr.statusText || 'The upload failed.'
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
      reject(new DriveError(0, 'The upload connection dropped before it finished.'))
    })

    xhr.addEventListener('abort', () => {
      options.signal?.removeEventListener('abort', abort)
      reject(new DriveError(0, 'Upload cancelled.'))
    })

    xhr.send(form)
  })
}

export type { FileOrder }
