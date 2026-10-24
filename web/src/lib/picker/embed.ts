/**
 * The embeddable picker.
 *
 * This is the file a host app loads — `snfs-picker.js` — and it is the only
 * thing an app has to know about. It builds a frame pointed at the SnFS picker
 * page, styles the modal around it, sends the request, and resolves a Promise
 * with the chosen objects.
 *
 *   const picked = await openSnfsPicker(
 *     { frameUrl: 'https://fs.solian.app/picker' },
 *     { view: 'files', mimeTypes: ['image/*'] },
 *   )
 *
 * The frame keeps the drive's own cookies when the host is on the drive's
 * site. A host on another registrable domain has no cookie to share and no
 * token to read (the session is an HttpOnly cookie), so it obtains a
 * single-use hand-off code from the identity issuer and passes it here; the
 * frame redeems it against the drive's own origin and keeps the token in
 * memory. Either way nothing about the session lives in the host page, and
 * the host never has to proxy file bytes through its own origin.
 */

import {
  PICKER_CHANNEL,
  isPickerMessage,
  optionsToQuery,
  type PickerFile,
  type PickerOptions,
} from './protocol'

/** What a host says about the frame itself, rather than about the pick. */
export interface SnfsPickerConfig {
  /** Address of the SnFS picker page, e.g. `https://fs.solian.app/picker`. */
  frameUrl: string
  /** Heading of the host's dialog written around the frame. */
  dialogTitle?: string
  /** Width in CSS pixels; the picker is built for 560 or wider. */
  width?: number
  /** Height in CSS pixels. */
  height?: number
}

/**
 * What a host asks for. Either the options themselves (the common case) or a
 * wrapper that also carries the credential a cross-site host must supply: a
 * drive token it already holds, or a single-use hand-off code it obtained
 * from the identity issuer.
 */
export interface SnfsPickerRequest {
  options?: PickerOptions
  /**
   * Access token for the drive API. Supply it when the host page is not on the
   * drive's site: a cross-site frame cannot receive the drive's `SameSite=Lax`
   * session cookie, and this is the supported way to authenticate it. The token
   * is delivered by `postMessage`, never in the frame URL.
   */
  token?: string
  /**
   * Single-use hand-off code issued by the identity issuer to this host for
   * its own session. Pass it when you have no token to give: the frame
   * redeems it against the drive's own origin and never exposes it beyond
   * that one call. A code is short-lived and can be redeemed once.
   */
  code?: string
}

export interface SnfsPickerHandlers {
  onReady?: () => void
  onSignedOut?: () => void
  onError?: (message: string) => void
}

const OVERLAY_ID = 'snfs-picker-overlay'
const OVERLAY_STYLE_ID = 'snfs-picker-styles'

const OVERLAY_CSS = `
${OVERLAY_ID}{
  --snfs-scrim:rgba(6,8,14,.55);
  --snfs-surface:#10141f;
  --snfs-deep:#0a0d16;
  --snfs-line:rgba(255,255,255,.08);
  --snfs-dim:#9aa3b8;
  --snfs-content:#ccd2e2;
  --snfs-hover:#1c2233;
  --snfs-shadow:rgba(0,0,0,.45);
  position:fixed;inset:0;z-index:2147483000;display:flex;align-items:center;
  justify-content:center;padding:24px;background:var(--snfs-scrim);
  backdrop-filter:blur(2px);
}
/* The frame around the page is drawn here, not inside the frame's document, so
   it carries its own palette. Without this override a host in daylight gets a
   midnight dialog around a paper page: the theme reaches the iframe's URL, but
   nothing on this side ever reads it. */
${OVERLAY_ID}[data-theme='light']{
  --snfs-scrim:rgba(28,35,52,.32);
  --snfs-surface:#eaeef6;
  --snfs-deep:#f3f5fa;
  --snfs-line:#ccd3e1;
  --snfs-dim:#4d5770;
  --snfs-content:#1c2334;
  --snfs-hover:#dbe0ea;
  --snfs-shadow:rgba(28,35,52,.18);
}
${OVERLAY_ID} .snfs-picker-frame{
  display:flex;flex-direction:column;max-width:100%;max-height:100%;
  overflow:hidden;border-radius:10px;background:var(--snfs-surface);
  box-shadow:0 24px 64px var(--snfs-shadow);border:1px solid var(--snfs-line);
}
${OVERLAY_ID} .snfs-picker-bar{
  display:flex;align-items:center;gap:8px;padding:8px 12px;
  font:500 12px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace;
  color:var(--snfs-dim);background:var(--snfs-surface);
  border-bottom:1px solid var(--snfs-line);
}
${OVERLAY_ID} .snfs-picker-bar .snfs-picker-mark{
  width:14px;height:14px;flex:none;background-color:currentColor;
}
${OVERLAY_ID} .snfs-picker-bar button{
  margin-left:auto;border:0;background:transparent;color:var(--snfs-dim);
  cursor:pointer;font:inherit;padding:2px 6px;border-radius:3px;
}
${OVERLAY_ID} .snfs-picker-bar button:hover{
  background:var(--snfs-hover);color:var(--snfs-content);
}
${OVERLAY_ID} iframe{border:0;display:block;background:var(--snfs-deep);flex:1;min-height:0}
`

function injectStyles() {
  if (document.getElementById(OVERLAY_STYLE_ID)) return
  const style = document.createElement('style')
  style.id = OVERLAY_STYLE_ID
  style.textContent = OVERLAY_CSS
  document.head.append(style)
}

/**
 * The theme the dialog frame should draw itself in. `auto` follows the host's
 * display preference, which is what the picker page does for itself; the two
 * would disagree for a moment if the frame did not ask the same question.
 */
function resolveTheme(theme: PickerOptions['theme']): 'dark' | 'light' {
  if (theme === 'dark' || theme === 'light') return theme
  if (typeof window === 'undefined' || !window.matchMedia) return 'dark'
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

/**
 * Owns one frame at a time. A host that opens the picker twice reuses the
 * frame instead of stacking two of them, which is also what keeps the request
 * in flight unambiguous.
 */
export class SnfsPicker {
  private readonly config: Required<SnfsPickerConfig>
  private overlay: HTMLDivElement | null = null
  private frame: HTMLIFrameElement | null = null
  private options: PickerOptions = {}
  private token: string | undefined
  private code: string | undefined
  private resolveRequest: ((files: PickerFile[]) => void) | null = null
  private handlers: SnfsPickerHandlers = {}
  private requestId = 0
  private releaseKeys: (() => void) | null = null
  private readonly onMessage = (event: MessageEvent) => this.receive(event)

  constructor(config: SnfsPickerConfig) {
    this.config = {
      frameUrl: config.frameUrl.replace(/\/+$/, ''),
      dialogTitle: config.dialogTitle ?? 'Choose from SnFS',
      width: config.width ?? 720,
      height: config.height ?? 560,
    }
  }

  /** The origin the frame must answer from; set once the URL is known. */
  private get origin(): string {
    return new URL(this.config.frameUrl, window.location.href).origin
  }

  /**
   * Opens the picker and resolves with the chosen files. An empty array means
   * the person cancelled, so a caller can tell "declined" from "not yet".
   */
  open(
    request: SnfsPickerRequest | PickerOptions = {},
    handlers: SnfsPickerHandlers = {},
  ): Promise<PickerFile[]> {
    this.close()
    const id = ++this.requestId
    const wrapped = 'options' in request || 'token' in request || 'code' in request
    const options = wrapped ? ((request as SnfsPickerRequest).options ?? {}) : (request as PickerOptions)
    this.options = options
    this.token = wrapped ? (request as SnfsPickerRequest).token : undefined
    this.code = wrapped ? (request as SnfsPickerRequest).code : undefined
    this.handlers = handlers

    const { promise, resolve } = Promise.withResolvers<PickerFile[]>()
    this.resolveRequest = resolve

    const { frame, overlay } = this.build()
    document.body.append(overlay)
    window.addEventListener('message', this.onMessage)

    frame.addEventListener(
      'load',
      () => {
        if (id !== this.requestId) return
        // The query string carries the request so the frame paints the right
        // view immediately; `picker:open` re-states it, which is what makes a
        // reused frame adopt the new request.
        frame.contentWindow?.postMessage(
          { channel: PICKER_CHANNEL, type: 'picker:open', options, token: this.token, code: this.code },
          this.origin,
        )
      },
      { once: true },
    )

    overlay.addEventListener('mousedown', (event) => {
      if (event.target === overlay) this.cancel()
    })
    const onKeydown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      // Ask the frame first: it knows whether it is mid-request. A frame that
      // does not answer within a tick is torn down anyway.
      frame.contentWindow?.postMessage(
        { channel: PICKER_CHANNEL, type: 'picker:close' },
        this.origin,
      )
      this.cancel()
    }
    window.addEventListener('keydown', onKeydown)
    this.releaseKeys = () => window.removeEventListener('keydown', onKeydown)

    return promise
  }

  /**
   * Tears the frame down. A pending request settles here, which is why the
   * answer is claimed *before* `close()` in `receive`: teardown must not be
   * able to turn a real selection into a cancellation.
   */
  close() {
    this.releaseKeys?.()
    this.releaseKeys = null
    window.removeEventListener('message', this.onMessage)
    this.frame?.remove()
    this.overlay?.remove()
    this.frame = null
    this.overlay = null
    const resolve = this.resolveRequest
    this.resolveRequest = null
    resolve?.([])
  }

  private cancel() {
    this.frame?.contentWindow?.postMessage(
      { channel: PICKER_CHANNEL, type: 'picker:close' },
      this.origin,
    )
    this.close()
  }

  private build(): { overlay: HTMLDivElement; frame: HTMLIFrameElement } {
    injectStyles()
    const overlay = document.createElement('div')
    overlay.className = OVERLAY_ID
    overlay.setAttribute('role', 'dialog')
    overlay.setAttribute('aria-modal', 'true')
    overlay.setAttribute('aria-label', this.config.dialogTitle)
    // The frame around the page is styled here, so it has to be told the
    // theme the same way the page is: the request carries it, and `auto`
    // resolves against the host's own display preference.
    overlay.dataset.theme = resolveTheme(this.options.theme)

    const shell = document.createElement('div')
    shell.className = 'snfs-picker-frame'
    shell.style.width = `${this.config.width}px`
    shell.style.height = `${this.config.height}px`

    const bar = document.createElement('div')
    bar.className = 'snfs-picker-bar'
    // The drive's own outline mark, masked so it takes the bar's text color:
    // the host is on another origin, so the glyph is fetched from the drive.
    const mark = document.createElement('span')
    mark.className = 'snfs-picker-mark'
    mark.setAttribute('aria-hidden', 'true')
    const glyph = new URL('icon-outline.svg', `${this.origin}/`).toString()
    mark.style.mask = `url("${glyph}") center / contain no-repeat`
    mark.style.webkitMask = `url("${glyph}") center / contain no-repeat`
    const title = document.createElement('span')
    title.textContent = this.config.dialogTitle
    bar.append(mark, title)
    const close = document.createElement('button')
    close.type = 'button'
    close.textContent = 'Close'
    close.addEventListener('click', () => this.cancel())
    bar.append(close)

    const frame = document.createElement('iframe')
    frame.className = 'snfs-picker-frame-body'
    frame.title = this.config.dialogTitle
    frame.src = this.frameUrl()

    shell.append(bar, frame)
    overlay.append(shell)
    this.overlay = overlay
    this.frame = frame
    return { overlay, frame }
  }

  /** The frame URL, with the request in the query string for the first paint. */
  private frameUrl(): string {
    const url = new URL(this.config.frameUrl, window.location.href)
    const query = optionsToQuery(this.options)
    if (query) url.search = query
    return url.toString()
  }

  private receive(event: MessageEvent) {
    if (!this.frame || event.source !== this.frame.contentWindow) return
    if (event.origin !== this.origin) return
    if (!isPickerMessage(event.data)) return

    const message = event.data
    if (message.type === 'picker:ready') {
      this.handlers.onReady?.()
      return
    }
    if (message.type === 'picker:signed-out') {
      this.handlers.onSignedOut?.()
      return
    }
    if (message.type === 'picker:error') {
      this.handlers.onError?.(String(message.message ?? ''))
      return
    }
    if (message.type !== 'picker:action') return

    const resolve = this.resolveRequest
    if (!resolve) return
    this.resolveRequest = null
    const files =
      message.action === 'picked' && Array.isArray(message.files)
        ? (message.files as PickerFile[])
        : []
    this.close()
    resolve(files)
  }
}

/**
 * One-shot convenience: open, wait for an answer, tear everything down.
 *
 *   const [file] = await openSnfsPicker({ frameUrl }, { view: 'files' })
 */
export function openSnfsPicker(
  config: SnfsPickerConfig,
  request: SnfsPickerRequest | PickerOptions = {},
  handlers: SnfsPickerHandlers = {},
): Promise<PickerFile[]> {
  return new SnfsPicker(config).open(request, handlers)
}

declare global {
  interface Window {
    SnfsPicker?: typeof SnfsPicker
    openSnfsPicker?: typeof openSnfsPicker
  }
}

if (typeof window !== 'undefined') {
  window.SnfsPicker = SnfsPicker
  window.openSnfsPicker = openSnfsPicker
}
