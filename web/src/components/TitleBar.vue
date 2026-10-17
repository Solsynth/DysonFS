<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { Menu16, Moon16, Search16, Sun16, Upload16 } from '@carbon/icons-vue'

import BrandMark from './BrandMark.vue'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import { useUploadStore } from '@/stores/upload'
import { useVaultStore } from '@/stores/vault'
import { initialOf } from '@/lib/format'

const session = useSessionStore()
const ui = useUiStore()
const uploads = useUploadStore()
const vault = useVaultStore()
const route = useRoute()
const { t } = useI18n()

const input = ref<HTMLInputElement | null>(null)

const onDrive = computed(() => route.meta.mode === 'drive')
const canUpload = computed(() => onDrive.value)
const identityName = computed(() => session.identity?.nick || session.identity?.name || t('session.account'))

function openPicker() {
  if (!canUpload.value) return
  input.value?.click()
}

function onPicked(event: Event) {
  const target = event.target as HTMLInputElement
  const list = Array.from(target.files ?? [])
  target.value = ''
  if (list.length === 0) return
  uploads.enqueue(list, vault.folderId)
}

function onUploadRequest() {
  openPicker()
}

onMounted(() => {
  window.addEventListener('snfs:upload', onUploadRequest)
})
onBeforeUnmount(() => {
  window.removeEventListener('snfs:upload', onUploadRequest)
})
</script>

<template>
  <header class="relative z-30 flex h-[52px] shrink-0 items-center gap-2 border-b border-line bg-ink px-3 md:px-4">
    <button
      class="btn btn-ghost btn-xs grid size-7 place-items-center px-0 lg:hidden"
      type="button"
      :aria-label="t('nav.openNavigation')"
      :title="t('nav.openNavigation')"
      @click="ui.toggleSidebar()"
    >
      <Menu16 aria-hidden="true" />
    </button>

    <!-- The lockup carries the same identity as the Solar Network console,
         so the two surfaces read as one product. -->
    <RouterLink
      to="/drive"
      class="flex shrink-0 items-center gap-2"
      :aria-label="t('app.home')"
      :title="`${t('app.product')} — ${t('app.fullName')}`"
    >
      <BrandMark :size="21" />
      <span class="text-[13px] font-semibold tracking-[0.14em] text-base-content">{{ t('app.product') }}</span>
      <span class="hidden text-2xs tracking-[0.04em] text-faint sm:inline">{{ t('app.network') }}</span>
    </RouterLink>

    <div class="ml-2 hidden flex-1 md:flex">
      <button
        class="group flex h-8 w-full max-w-[360px] items-center gap-2 border border-line bg-base-200/60 px-2.5 text-left transition-colors duration-150 hover:border-line hover:text-base-content"
        type="button"
        :aria-label="t('app.searchFilesOrCommand')"
        @click="ui.paletteOpen = true"
      >
        <Search16 class="shrink-0 text-faint" aria-hidden="true" />
        <span class="truncate text-2xs text-faint group-hover:text-dim">
          {{ t('app.searchFilesOrCommand') }}
        </span>
        <span class="ml-auto hidden shrink-0 items-center gap-1 sm:flex">
          <span class="kbd">⌘</span>
          <span class="kbd">K</span>
        </span>
      </button>
    </div>

    <div class="ml-auto flex shrink-0 items-center gap-1">
      <span
        v-if="session.sample"
        class="mr-1 hidden items-center gap-1.5 text-2xs text-faint lg:flex"
        :title="t('app.sampleHint')"
      >
        <span class="dot bg-warning" aria-hidden="true" />
        {{ t('app.sampleBadge') }}
      </span>

      <button
        class="btn btn-ghost btn-xs grid size-7 place-items-center px-0 md:hidden"
        type="button"
        :aria-label="t('app.searchFiles')"
        :title="t('app.searchFiles')"
        @click="ui.paletteOpen = true"
      >
        <Search16 aria-hidden="true" />
      </button>

      <button
        class="btn btn-ghost btn-xs grid size-7 place-items-center px-0"
        type="button"
        :aria-label="ui.isDark ? t('app.switchToLight') : t('app.switchToDark')"
        :title="ui.isDark ? t('app.switchToLight') : t('app.switchToDark')"
        @click="ui.toggleTheme()"
      >
        <Sun16 v-if="ui.isDark" aria-hidden="true" />
        <Moon16 v-else aria-hidden="true" />
      </button>

      <button
        class="btn btn-primary btn-sm ml-1"
        type="button"
        :disabled="!canUpload"
        :title="canUpload ? t('app.uploadFiles') : t('app.uploadDisabled')"
        @click="openPicker"
      >
        <Upload16 aria-hidden="true" />
        <span class="hidden sm:inline">{{ t('app.upload') }}</span>
        <span v-if="uploads.activeCount > 0" class="text-2xs tabular-nums opacity-80">
          {{ uploads.activeCount }}
        </span>
      </button>

      <span
        class="ml-1 grid size-6 shrink-0 place-items-center border border-line bg-base-300 text-2xs text-dim"
        :title="`${identityName} · ${t('app.network')}`"
        :aria-label="t('app.signedInAs', { name: identityName })"
      >
        {{ initialOf(identityName) }}
      </span>

      <input
        ref="input"
        type="file"
        multiple
        class="sr-only"
        tabindex="-1"
        aria-hidden="true"
        @change="onPicked"
      />
    </div>
  </header>
</template>
