/** Persisted interface preferences: color theme and language. */

export type Theme = 'dark' | 'light'
export type Locale = 'en' | 'zh-CN'

export const THEME_KEY = 'snfs.theme'
export const LOCALE_KEY = 'snfs.locale'

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

/** Stored choice, else Chinese when the browser asks for it. */
export function readLocale(): Locale {
  const stored = storage()?.getItem(LOCALE_KEY)
  if (stored === 'en' || stored === 'zh-CN') return stored
  if (typeof navigator === 'undefined') return 'en'
  const languages = navigator.languages?.length ? navigator.languages : [navigator.language]
  return languages.some((language) => language.toLowerCase().startsWith('zh')) ? 'zh-CN' : 'en'
}

export function applyLocale(locale: Locale) {
  if (typeof document === 'undefined') return
  document.documentElement.lang = locale
}