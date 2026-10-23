import { describe, expect, it } from 'vitest'

import { signInUrl } from '@/lib/auth'

/**
 * The drive does not own a login form: it sends the browser to the identity
 * console's shared page and expects the session cookie (scoped to the parent
 * domain) to come back with it. The care needed here is that the hand-off
 * target is the drive's own address, because Stargate refuses to hand a
 * finished sign-in to an origin the cookie cannot reach.
 */
describe('signInUrl', () => {
  it('names the shared sign-in page and this page as the hand-off target', () => {
    const url = new URL(signInUrl())
    expect(url.origin).toBe('https://id.solian.app')
    expect(url.pathname).toBe('/sign-in')
    expect(url.searchParams.get('redirectUri')).toBe(window.location.href)
  })
})