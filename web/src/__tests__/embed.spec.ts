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
})
