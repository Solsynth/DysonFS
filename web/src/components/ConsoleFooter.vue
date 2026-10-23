<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ChevronDown16, Moon16, Sun16 } from '@carbon/icons-vue'

import { useUiStore } from '@/stores/ui'
import type { Locale } from '@/lib/prefs'

const { t } = useI18n()
const ui = useUiStore()

function onChangeLocale(event: Event) {
  ui.setLocale((event.target as HTMLSelectElement).value as Locale)
}
</script>

<template>
  <footer
    class="flex h-9 shrink-0 items-center gap-3 border-t border-line bg-ink px-4 text-2xs text-faint"
  >
    <span>{{ t('app.copyright', { year: new Date().getFullYear() }) }}</span>

    <div class="ml-auto flex items-center gap-3">
      <button
        type="button"
        class="btn btn-ghost btn-xs grid size-6 place-items-center px-0"
        :aria-label="t('app.switchToLight')"
        :title="t('app.switchToLight')"
        @click="ui.setTheme(ui.isDark ? 'light' : 'dark')"
      >
        <Sun16 v-if="ui.isDark" aria-hidden="true" />
        <Moon16 v-else aria-hidden="true" />
      </button>

      <label class="relative flex items-center">
        <span class="sr-only">{{ t('app.language') }}</span>
        <select
          class="footer-locale"
          :value="ui.locale"
          :aria-label="t('app.language')"
          @change="onChangeLocale"
        >
          <option value="en">{{ t('app.languageEnglish') }}</option>
          <option value="zh-CN">{{ t('app.languageChinese') }}</option>
        </select>
        <ChevronDown16
          class="pointer-events-none absolute right-0 h-3 w-3 text-faint"
          aria-hidden="true"
        />
      </label>
    </div>
  </footer>
</template>

<style scoped>
/* The language picker is a footer control, not a form field: it drops the
   field border, fill, and shadow so it reads as one more piece of footer
   text. `appearance: none` needs a chevron, drawn as a mask so it inherits
   the footer's color in both themes. */
select.footer-locale {
  appearance: none;
  inline-size: auto;
  width: auto;
  height: 1.5rem;
  padding: 0 1rem 0 0;
  border: 0;
  border-radius: 0;
  background-color: transparent;
  background-image: none;
  box-shadow: none;
  color: inherit;
  font-family: inherit;
  font-size: 0.6875rem;
  line-height: 1.5rem;
  cursor: pointer;
}

select.footer-locale:hover {
  color: var(--color-dim);
}

select.footer-locale:focus-visible {
  outline: 1px solid var(--color-primary);
  outline-offset: 2px;
}
</style>
