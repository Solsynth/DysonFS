/** Persisted interface preferences: color theme. */

export type Theme = 'dark' | 'light'

export const THEME_KEY = 'solarpass.theme'

export const themeNames: Record<Theme, string> = {
  dark: 'snfs',
  light: 'snfs-light',
}

function storage(): Storage | null {
  return typeof localStorage === 'undefined' ? null : localStorage
}

/** Stored choice, else the operating system's light/dark preference. */
export function readTheme(): Theme {
  const stored = storage()?.getItem(THEME_KEY)
  if (stored === 'light' || stored === 'dark') return stored
  if (typeof window === 'undefined' || !window.matchMedia) return 'dark'
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

export function applyTheme(theme: Theme) {
  if (typeof document === 'undefined') return
  document.documentElement.dataset.theme = themeNames[theme]
}
