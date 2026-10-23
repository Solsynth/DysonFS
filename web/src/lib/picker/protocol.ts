/**
 * The wire protocol between an embedding page and the SnFS picker iframe.
 *
 * The picker is a page of its own so an app can borrow the drive's session
 * cookie without moving that cookie into a script tag. Everything that crosses
 * the frame boundary is a plain JSON object tagged with `channel`, so both
 * sides can drop messages that belong to some other integration running on the
 * same page.
 */

export const PICKER_CHANNEL = 'snfs-picker'

/** What the app asks to pick. Mirrors the Google Picker's view idea. */
export type PickerView = 'files' | 'folders' | 'any'

export interface PickerOptions {
  /** Files, folders, or both. Defaults to `files`. */
  view?: PickerView
  /** Permit more than one selection. Defaults to `false`. */
  multiple?: boolean
  /** MIME types the app accepts; `image/*` matches a whole family. */
  mimeTypes?: string[]
  /** Broad families (`image`, `document`, …) accepted by the app. */
  kinds?: string[]
  /** File extensions (without the dot) accepted by the app. */
  extensions?: string[]
  /** Folder to open first; `null` is the drive root. */
  startFolderId?: string | null
  /** Offer an upload tab, so the app can accept a file that does not exist yet. */
  upload?: boolean
  /** Heading drawn above the listing, e.g. "Choose an attachment". */
  title?: string
  /** Short guidance under the heading. */
  description?: string
  /** `dark` or `light`; `auto` follows the operating system. */
  theme?: 'auto' | 'dark' | 'light'
  /** `en` or `zh-CN`. */
  locale?: 'en' | 'zh-CN'
  /** Shown when the current folder holds nothing the view accepts. */
  emptyHint?: string
}

/**
 * What the host receives for each chosen object. This is a copy, not the API
 * row alone: a selected file may come from a folder the picker no longer
 * lists, and the app should not have to hold the listing to describe it.
 */
export interface PickerFile {
  id: string
  name: string
  is_folder: boolean
  mime_type: string
  size: number
  extension: string
  kind: string
  parent_id: string | null
  created_at: string
  updated_at: string
  /** Same-origin URL of the object's bytes, with the session cookie attached. */
  url: string
  /** Same-origin URL of a rendered thumbnail; `null` when none exists. */
  thumbnail_url: string | null
  /** The API row, for an app that needs a field this shape does not name. */
  file: Record<string, unknown>
}

/** Host → iframe. */
export type HostMessage =
  | {
      channel: typeof PICKER_CHANNEL
      type: 'picker:open'
      options: PickerOptions
      /**
       * Access token for the drive API, when the host has one (SunkenLand's
       * OIDC session, for instance). A cross-site frame cannot receive the
       * drive's `SameSite=Lax` cookie, so this is how such a host
       * authenticates it. Omit it when the frame is same-site and the cookie
       * already works.
       */
      token?: string
    }
  | { channel: typeof PICKER_CHANNEL; type: 'picker:close' }
/** Iframe → host. */
export type FrameMessage =
  | { channel: typeof PICKER_CHANNEL; type: 'picker:ready' }
  | { channel: typeof PICKER_CHANNEL; type: 'picker:signed-out' }
  | { channel: typeof PICKER_CHANNEL; type: 'picker:action'; action: 'picked'; files: PickerFile[] }
  | { channel: typeof PICKER_CHANNEL; type: 'picker:action'; action: 'cancelled' }
  | { channel: typeof PICKER_CHANNEL; type: 'picker:error'; message: string }

/** True when a message on this window is addressed to the picker channel. */
export function isPickerMessage(
  data: unknown,
): data is { channel: string; type: string; [key: string]: unknown } {
  if (typeof data !== 'object' || data === null) return false
  if (!('channel' in data) || data.channel !== PICKER_CHANNEL) return false
  return 'type' in data && typeof data.type === 'string'
}

/** Reads a host message, rejecting anything that is not one this build knows. */
export function parseHostMessage(data: unknown): HostMessage | null {
  if (!isPickerMessage(data)) return null
  if (data.type === 'picker:open') {
    const options = 'options' in data ? data.options : undefined
    const token = 'token' in data ? data.token : undefined
    return {
      channel: PICKER_CHANNEL,
      type: 'picker:open',
      options: typeof options === 'object' && options !== null ? (options as PickerOptions) : {},
      token: typeof token === 'string' && token.trim() ? token.trim() : undefined,
    }
  }
  if (data.type === 'picker:close') {
    return { channel: PICKER_CHANNEL, type: 'picker:close' }
  }
  return null
}

function stringList(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined
  const list = value.filter((entry): entry is string => typeof entry === 'string')
  return list.length ? list : undefined
}

function flag(value: unknown): boolean {
  return value === true || value === 1 || value === 'true' || value === '1'
}

/**
 * Normalizes options from an untrusted source (a query string, a `postMessage`
 * payload, a script attribute) into the shape the picker uses. Unknown values
 * fall back rather than throwing: an embed is worth showing even when the app
 * asked for a view this build does not know.
 */
export function normalizeOptions(raw: Record<string, unknown> = {}): PickerOptions {
  const view = raw.view
  const theme = raw.theme
  const locale = raw.locale
  const startFolderId = raw.startFolderId ?? raw.folder
  const title = typeof raw.title === 'string' ? raw.title.trim() : ''
  const description = typeof raw.description === 'string' ? raw.description.trim() : ''
  const emptyHint = typeof raw.emptyHint === 'string' ? raw.emptyHint.trim() : ''

  return {
    view: view === 'folders' || view === 'any' ? view : 'files',
    multiple: flag(raw.multiple),
    mimeTypes: stringList(raw.mimeTypes),
    kinds: stringList(raw.kinds),
    extensions: stringList(raw.extensions),
    startFolderId:
      typeof startFolderId === 'string' && startFolderId.trim() ? startFolderId.trim() : null,
    upload: flag(raw.upload),
    title: title || undefined,
    description: description || undefined,
    theme: theme === 'dark' || theme === 'light' ? theme : 'auto',
    locale: locale === 'zh-CN' || locale === 'en' ? locale : undefined,
    emptyHint: emptyHint || undefined,
  }
}

/** The options a `/picker?…` URL asks for. Lists arrive comma-separated. */
export function optionsFromQuery(search: string): PickerOptions {
  const params = new URLSearchParams(search)
  const list = (key: string): string[] | undefined => {
    const value = params.get(key)
    if (!value) return undefined
    const parts = value
      .split(',')
      .map((part) => part.trim())
      .filter(Boolean)
    return parts.length ? parts : undefined
  }

  return normalizeOptions({
    view: params.get('view') ?? undefined,
    multiple: params.get('multiple') ?? undefined,
    upload: params.get('upload') ?? undefined,
    mimeTypes: list('mime') ?? list('mimeTypes'),
    kinds: list('kinds'),
    extensions: list('extensions') ?? list('ext'),
    startFolderId: params.get('folder') ?? params.get('startFolderId') ?? undefined,
    title: params.get('title') ?? undefined,
    description: params.get('description') ?? undefined,
    theme: params.get('theme') ?? undefined,
    locale: params.get('locale') ?? undefined,
    emptyHint: params.get('emptyHint') ?? undefined,
  })
}

/** Serializes options into the query string of a picker URL. */
export function optionsToQuery(options: PickerOptions): string {
  const params = new URLSearchParams()
  if (options.view && options.view !== 'files') params.set('view', options.view)
  if (options.multiple) params.set('multiple', '1')
  if (options.upload) params.set('upload', '1')
  if (options.mimeTypes?.length) params.set('mime', options.mimeTypes.join(','))
  if (options.kinds?.length) params.set('kinds', options.kinds.join(','))
  if (options.extensions?.length) params.set('extensions', options.extensions.join(','))
  if (options.startFolderId) params.set('folder', options.startFolderId)
  if (options.title) params.set('title', options.title)
  if (options.description) params.set('description', options.description)
  if (options.theme && options.theme !== 'auto') params.set('theme', options.theme)
  if (options.locale) params.set('locale', options.locale)
  if (options.emptyHint) params.set('emptyHint', options.emptyHint)
  return params.toString()
}
