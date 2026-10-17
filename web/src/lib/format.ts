import type { CloudFile } from './api/types'

const byteUnits = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function formatBytes(bytes: number | null | undefined, digits = 1): string {
  if (bytes === null || bytes === undefined || Number.isNaN(bytes)) return '—'
  if (bytes === 0) return '0 B'
  if (bytes < 0) return '—'
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < byteUnits.length - 1) {
    value /= 1024
    unit += 1
  }
  const place = value >= 100 ? Math.min(digits, 1) : digits
  const rendered = value.toFixed(place).replace(/\.0+$/, '')
  return `${rendered} ${byteUnits[unit]}`
}

export function formatQuotaMb(mb: number | null | undefined): string {
  if (mb === null || mb === undefined || Number.isNaN(mb)) return '—'
  return formatBytes(mb * 1024 * 1024)
}

export function formatCount(count: number | null | undefined): string {
  if (count === null || count === undefined) return '—'
  return new Intl.NumberFormat('en-GB').format(count)
}

export function percent(value: number, total: number): number {
  if (!total || total <= 0) return 0
  return Math.min(100, Math.max(0, (value / total) * 100))
}

export function formatPercent(value: number, total: number, digits = 0): string {
  return `${percent(value, total).toFixed(digits)}%`
}

export function extensionOf(name: string): string {
  const clean = name.trim()
  const dot = clean.lastIndexOf('.')
  if (dot <= 0 || dot === clean.length - 1) return ''
  return clean.slice(dot + 1).toLowerCase()
}

export function baseName(name: string): string {
  const dot = name.lastIndexOf('.')
  if (dot <= 0) return name
  return name.slice(0, dot)
}

export function titleCase(value: string): string {
  return value.replace(/\b[a-z]/g, (letter) => letter.toUpperCase())
}

export function kindLabel(file: CloudFile): string {
  if (file.is_folder) return 'FOLDER'
  const extension = extensionOf(file.name)
  if (extension) return extension.toUpperCase()
  const mime = file.object?.mime_type || ''
  const subtype = mime.split('/')[1]
  return subtype ? subtype.toUpperCase() : 'FILE'
}

export function mimeOf(file: CloudFile): string {
  if (file.is_folder) return 'application/x-directory'
  return file.object?.mime_type || 'application/octet-stream'
}

export function sizeOf(file: CloudFile): number {
  return file.object?.size ?? 0
}

export function totalSize(files: CloudFile[]): number {
  return files.reduce((sum, file) => sum + sizeOf(file), 0)
}

export function fileDimensions(file: CloudFile): string | null {
  const meta = (file.object?.meta ?? file.file_meta ?? null) as Record<string, unknown> | null
  if (!meta) return null
  const width = Number(meta.width ?? 0)
  const height = Number(meta.height ?? 0)
  if (!width || !height) return null
  return `${Math.round(width)} × ${Math.round(height)}`
}

export function fileDuration(file: CloudFile): string | null {
  const meta = (file.object?.meta ?? file.file_meta ?? null) as Record<string, unknown> | null
  if (!meta) return null
  const milliseconds = Number(meta.duration_ms ?? 0)
  const seconds = milliseconds > 0 ? milliseconds / 1000 : Number(meta.duration ?? 0)
  if (!seconds || !Number.isFinite(seconds)) return null
  const total = Math.round(seconds)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${m}:${String(s).padStart(2, '0')}`
}

export function shortHash(hash: string | null | undefined): string {
  if (!hash) return '—'
  return `${hash.slice(0, 10)}…`
}

export function shortId(id: string | null | undefined): string {
  if (!id) return '—'
  return id.slice(0, 8)
}

const relative = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })
const dateStamp = new Intl.DateTimeFormat('en-GB', {
  day: '2-digit',
  month: 'short',
  year: 'numeric',
})
const dateShort = new Intl.DateTimeFormat('en-GB', {
  day: '2-digit',
  month: 'short',
  year: '2-digit',
})
const dateTime = new Intl.DateTimeFormat('en-GB', {
  day: '2-digit',
  month: 'short',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})
const timeOnly = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

function toDate(value: string | Date | null | undefined): Date | null {
  if (!value) return null
  const date = value instanceof Date ? value : new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

export function formatDate(value: string | Date | null | undefined): string {
  const date = toDate(value)
  return date ? dateStamp.format(date) : '—'
}

export function formatDateTime(value: string | Date | null | undefined): string {
  const date = toDate(value)
  return date ? dateTime.format(date).replace(',', '') : '—'
}

export function formatTime(value: string | Date | null | undefined): string {
  const date = toDate(value)
  return date ? timeOnly.format(date) : '—'
}

export function formatShortDate(value: string | Date | null | undefined): string {
  const date = toDate(value)
  return date ? dateShort.format(date) : '—'
}

export function relativeTime(value: string | Date | null | undefined): string {
  const date = toDate(value)
  if (!date) return '—'
  const delta = date.getTime() - Date.now()
  const absolute = Math.abs(delta)
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour

  if (absolute < minute * 1.5) return 'just now'
  if (absolute < hour) return relative.format(Math.round(delta / minute), 'minute')
  if (absolute < day) return relative.format(Math.round(delta / hour), 'hour')

  const days = Math.round(delta / day)
  if (Math.abs(days) < 8) return relative.format(days, 'day')
  return formatShortDate(date)
}

export function modifiedLabel(value: string | Date | null | undefined): string {
  const date = toDate(value)
  if (!date) return '—'
  const delta = Date.now() - date.getTime()
  if (delta < 7 * 24 * 60 * 60 * 1000) return relativeTime(date)
  return formatShortDate(date)
}

export function fileTypeSummary(file: CloudFile): string {
  if (file.is_folder) {
    const count = file.children_count ?? 0
    return count > 0 ? `${formatCount(count)} objects` : 'Folder'
  }
  const mime = mimeOf(file)
  return mime
}

export function plural(count: number, singular: string, pluralForm?: string): string {
  return count === 1 ? singular : (pluralForm ?? `${singular}s`)
}

export function displayName(file: CloudFile): string {
  return file.name.trim() || 'Untitled'
}

export function initialOf(name: string | null | undefined): string {
  const trimmed = (name ?? '').trim()
  if (!trimmed) return '·'
  return trimmed.slice(0, 1).toUpperCase()
}

export function parseMeta(file: CloudFile): Record<string, unknown> {
  const raw = file.object?.meta ?? file.file_meta
  if (!raw) return {}
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw) as Record<string, unknown>
    } catch {
      return {}
    }
  }
  return raw
}
