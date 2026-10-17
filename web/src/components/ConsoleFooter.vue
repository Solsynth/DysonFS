<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { Moon16, Sun16 } from '@carbon/icons-vue'

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

      <label class="flex items-center gap-1.5">
        <span class="sr-only">{{ t('app.language') }}</span>
        <select
          class="select select-xs footer-locale"
          :value="ui.locale"
          :aria-label="t('app.language')"
          @change="onChangeLocale"
        >
          <option value="en">{{ t('app.languageEnglish') }}</option>
          <option value="zh-CN">{{ t('app.languageChinese') }}</option>
        </select>
      </label>
    </div>
  </footer>
</template>

<style scoped>
/* The language picker is inline, not a full-width form field. */
select.select.footer-locale {
  inline-size: auto;
  width: auto;
  height: 1.75rem;
  min-height: 1.75rem;
  padding-block: 0;
  font-size: 0.6875rem;
}
</style>
