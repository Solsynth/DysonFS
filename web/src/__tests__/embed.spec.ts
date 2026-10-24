import { afterEach, describe, expect, it, vi } from 'vitest'

import { SnfsPicker, type SnfsPickerRequest } from '@/lib/picker/embed'
import { PICKER_CHANNEL, type PickerOptions } from '@/lib/picker/protocol'

const FRAME_URL = 'https://fs.example.test/picker'

/** Opens a picker and returns the `picker:open` payload its SDK sent. */
function openPayload(request: SnfsPickerRequest | PickerOptions) {
  const picker = new SnfsPicker({ frameUrl: FRAME_URL })
  const promise = picker.open(request)
  const overlay = document.querySelector('.snfs-picker-overlay')
  const frame = overlay?.querySelector('iframe')
  if (!overlay || !frame || !frame.contentWindow) {
    throw new Error('picker frame was not built')
  }
  const post = vi.spyOn(frame.contentWindow, 'postMessage')
  frame.dispatchEvent(new Event('load'))
  const call = post.mock.calls.at(-1)
  picker.close()
  void promise
  return { payload: call?.[0] as Record<string, unknown> | undefined, targetOrigin: call?.[1] }
}

/** Opens a picker and returns the dialog DOM it built. */
function openOverlay(options: PickerOptions) {
  const picker = new SnfsPicker({ frameUrl: FRAME_URL })
  void picker.open({ options })
  const overlay = document.querySelector('.snfs-picker-overlay') as HTMLElement
  if (!overlay) throw new Error('picker overlay was not built')
  picker.close()
  return { overlay }
}

afterEach(() => {
  document.querySelector('.snfs-picker-overlay')?.remove()
})

describe('picker embed request', () => {
  it('delivers a hand-off code without a token', () => {
    const { payload, targetOrigin } = openPayload({
      options: { view: 'files' },
      code: 'handoff-code-1',
    })
    expect(payload).toMatchObject({
      channel: PICKER_CHANNEL,
      type: 'picker:open',
      options: { view: 'files' },
      code: 'handoff-code-1',
    })
    expect(payload?.token).toBeUndefined()
    expect(targetOrigin).toBe('https://fs.example.test')
  })

  it('still delivers a token unchanged', () => {
    const { payload } = openPayload({ options: {}, token: 'tok-1' })
    expect(payload).toMatchObject({
      channel: PICKER_CHANNEL,
      type: 'picker:open',
      token: 'tok-1',
    })
    expect(payload?.code).toBeUndefined()
  })

  it('treats a bare options object as the request itself', () => {
    const { payload } = openPayload({ view: 'any' })
    expect(payload).toMatchObject({
      channel: PICKER_CHANNEL,
      type: 'picker:open',
      options: { view: 'any' },
    })
    expect(payload?.token).toBeUndefined()
    expect(payload?.code).toBeUndefined()
  })

  it('paints the dialog in the theme the host asked for', () => {
    const { overlay } = openOverlay({ theme: 'light' })
    expect(overlay.dataset.theme).toBe('light')
  })

  it('keeps the dialog dark when the host asks for dark', () => {
    const { overlay } = openOverlay({ theme: 'dark' })
    expect(overlay.dataset.theme).toBe('dark')
  })

  it('resolves an unspecified theme to the display preference', () => {
    // jsdom answers `matches: false` for every query, so `auto` lands on dark.
    const { overlay } = openOverlay({})
    expect(overlay.dataset.theme).toBe('dark')
  })
})
