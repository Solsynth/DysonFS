import type { CloudFile } from './api/types'
import { currentLocale, translate } from './i18n'

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
  return new Intl.NumberFormat(currentLocale()).format(count)
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

/**
 * A short kind token for dense tables. An extension is a format identifier and
 * stays as-is in every language; a folder or an unnamed blob is a word, so it
 * follows the interface language.
 */
export function kindLabel(file: CloudFile): string {
  if (file.is_folder) return translate('label.folder')
  const extension = extensionOf(file.name)
  if (extension) return extension.toUpperCase()
  const subtype = file.object?.mime_type.split('/')[1]
  return subtype ? subtype.toUpperCase() : translate('label.file')
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

// Formatters are built per locale and cached, because a `Intl` instance bakes
// its locale in at construction and the console lets the operator switch
// language without a reload.
const formatters = new Map<string, Intl.DateTimeFormat | Intl.RelativeTimeFormat>()

function cache<T extends Intl.DateTimeFormat | Intl.RelativeTimeFormat>(
  key: string,
  build: () => T,
): T {
  const existing = formatters.get(key)
  if (existing) return existing as T
  const made = build()
  formatters.set(key, made)
  return made
}

// English dates keep the console's day-month-year house style (`17 Oct 2026`)
// rather than the US ordering `en` would give; Chinese takes its own region.
function formatLocale(): string {
  return currentLocale() === 'en' ? 'en-GB' : currentLocale()
}

function relativeFormatter(): Intl.RelativeTimeFormat {
  return cache(`relative:${formatLocale()}`, () =>
    new Intl.RelativeTimeFormat(formatLocale(), { numeric: 'auto' }),
  )
}

function dateFormatter(key: string, options: Intl.DateTimeFormatOptions) {
  return cache(`date:${key}:${formatLocale()}`, () =>
    new Intl.DateTimeFormat(formatLocale(), options),
  ) as Intl.DateTimeFormat
}

const STAMP: Intl.DateTimeFormatOptions = {
  day: '2-digit',
  month: 'short',
  year: 'numeric',
}
const SHORT: Intl.DateTimeFormatOptions = {
  day: '2-digit',
  month: 'short',
  year: '2-digit',
}
const DATE_TIME: Intl.DateTimeFormatOptions = {
  day: '2-digit',
  month: 'short',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
}
const CLOCK: Intl.DateTimeFormatOptions = {
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
}

function toDate(value: string | Date | null | undefined): Date | null {
  if (!value) return null
  const date = value instanceof Date ? value : new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

export function formatDate(value: string | Date | null | undefined): string {
  const date = toDate(value)
  return date ? dateFormatter('stamp', STAMP).format(date) : '—'
}

export function formatDateTime(value: string | Date | null | undefined): string {
  const date = toDate(value)
  return date ? dateFormatter('time', DATE_TIME).format(date).replace(',', '') : '—'
}

export function formatTime(value: string | Date | null | undefined): string {
  const date = toDate(value)
  return date ? dateFormatter('clock', CLOCK).format(date) : '—'
}

export function formatShortDate(value: string | Date | null | undefined): string {
  const date = toDate(value)
  return date ? dateFormatter('short', SHORT).format(date) : '—'
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
  if (absolute < hour) return relativeFormatter().format(Math.round(delta / minute), 'minute')
  if (absolute < day) return relativeFormatter().format(Math.round(delta / hour), 'hour')

  const days = Math.round(delta / day)
  if (Math.abs(days) < 8) return relativeFormatter().format(days, 'day')
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
    return count > 0
      ? translate('status.objectsCount', { n: formatCount(count) })
      : translate('label.folder')
  }
  const mime = mimeOf(file)
  return mime
}

export function plural(count: number, singular: string, pluralForm?: string): string {
  return count === 1 ? singular : (pluralForm ?? `${singular}s`)
}

export function displayName(file: CloudFile): string {
  return file.name.trim() || translate('label.untitled')
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
