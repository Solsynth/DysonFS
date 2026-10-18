/**
 * The shared sign-in page.
 *
 * Solar Network apps do not ship their own login form: they open the console's
 * page on the identity host and hand it a callback. The session then lives in
 * an HttpOnly cookie scoped to `[auth] cookieDomain` on Stargate, so it reaches
 * fs.solian.app as soon as the identity host sets it — the app only has to send
 * the browser there and receive it back.
 *
 * `VITE_AUTH_URL` overrides the page for a deployment whose identity host is
 * not id.solian.app (and for local development, where it is the Stargate
 * process on :8080).
 */
const DEFAULT_AUTH_PAGE = 'https://id.solian.app/sign-in'

/**
 * The address of the sign-in page for the page the person is on. Stargate
 * accepts only a target the session cookie reaches: this app's origin and the
 * identity host share `cookieDomain`, so the drive's own URL is the right
 * hand-off target. A read-only `demo` flag is dropped so sample mode does not
 * survive a real sign-in.
 */
export function signInUrl(): string {
  const base = import.meta.env.VITE_AUTH_URL || DEFAULT_AUTH_PAGE
  try {
    const url = new URL(base, window.location.origin)
    const back = new URL(window.location.href)
    back.searchParams.delete('demo')
    url.searchParams.set('redirectUri', back.toString())
    return url.toString()
  } catch {
    return base
  }
}