import { createI18n } from 'vue-i18n'
import type { I18nOptions } from 'vue-i18n'

import { readLocale } from '@/lib/prefs'

// Each area contributes a module per language. Splitting the catalogs keeps
// translations reviewable next to the view they serve while the message
// object stays flat: modules must export unique top-level namespaces.
function bundle(modules: Record<string, unknown>): Record<string, unknown> {
  return Object.assign({}, ...Object.values(modules))
}

const english = import.meta.glob('../locales/en/*.ts', { eager: true, import: 'default' })
const chinese = import.meta.glob('../locales/zh-CN/*.ts', { eager: true, import: 'default' })

export const i18n = createI18n<false>({
  legacy: false,
  locale: readLocale(),
  fallbackLocale: 'en',
  messages: {
    en: bundle(english),
    'zh-CN': bundle(chinese),
  } as I18nOptions['messages'],
})

/** Locale used by formatters (relative time, compact numbers). */
export function currentLocale(): string {
  return i18n.global.locale.value
}

/** Translation outside a component setup; use `useI18n` inside components. */
export function translate(key: string, named?: Record<string, unknown>): string {
  return i18n.global.t(key, named ?? {})
}
