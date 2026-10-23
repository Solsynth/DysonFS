<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed } from 'vue'
import {
  ArrowRight16,
  CloudOffline16,
  Login16,
  Moon16,
  Renew16,
  Sun16,
} from '@carbon/icons-vue'

import BrandMark from './BrandMark.vue'
import IconButton from './IconButton.vue'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'

const session = useSessionStore()
const ui = useUiStore()
const { t } = useI18n()

const state = computed(() => session.state)
</script>

<template>
  <slot v-if="state === 'ready'" />

  <div v-else class="relative flex h-dvh flex-col overflow-hidden bg-ink text-base-content">
    <header class="relative z-10 flex h-[52px] shrink-0 items-center gap-3 border-b border-line px-4">
      <BrandMark :size="21" />
      <span class="text-[14px] font-semibold tracking-[-0.01em]">{{ t('app.product') }}</span>
      <span class="hidden text-[11px] text-faint sm:inline">{{ t('app.fullName') }}</span>
      <IconButton
        class="ml-auto"
        :label="ui.isDark ? t('app.switchToLight') : t('app.switchToDark')"
        @click="ui.toggleTheme()"
      >
        <Sun16 v-if="ui.isDark" />
        <Moon16 v-else />
      </IconButton>
    </header>

    <main class="relative z-10 flex flex-1 items-center justify-center overflow-y-auto px-5 py-10">
      <Transition
        mode="out-in"
        enter-active-class="transition duration-200 ease-out"
        enter-from-class="opacity-0 translate-y-1"
        leave-active-class="transition duration-100 ease-in"
        leave-to-class="opacity-0"
      >
        <div v-if="state === 'loading'" key="loading" class="w-full max-w-[380px]">
          <BrandMark :size="44" class="text-base-content" />
          <h1 class="title mt-5 text-[22px]">{{ t('app.product') }}</h1>
          <p class="voice mt-1.5">{{ t('app.fullName') }}</p>
          <div class="loading-scan mt-8 h-[2px] w-full bg-line-soft" />
          <p class="mt-4 text-[11px] text-faint">{{ t('session.connecting') }}</p>
        </div>

        <div v-else-if="state === 'signed-out'" key="signed-out" class="w-full max-w-[440px]">
          <span class="flex items-center gap-2 text-[11px] font-medium text-faint">
            <Login16 aria-hidden="true" />
            {{ t('session.signInRequired') }}
          </span>
          <h1 class="title mt-3 text-[30px] leading-[1.15]">{{ t('session.signInTitle') }}</h1>
          <p class="voice mt-3 max-w-[400px]">
            {{ t('session.signInBody') }}
          </p>

          <div class="mt-7 flex flex-wrap items-center gap-2">
            <a :href="session.authUrl" class="btn btn-primary h-9">
              {{ t('session.signIn') }}
              <ArrowRight16 class="ml-0.5" />
            </a>
          </div>
        </div>

        <div v-else key="offline" class="w-full max-w-[440px]">
          <span class="flex items-center gap-2 text-[11px] font-medium text-error">
            <CloudOffline16 aria-hidden="true" />
            Storage unreachable
          </span>
          <h1 class="title mt-3 text-[30px] leading-[1.15]">{{ t('error.storageUnreachable') }}</h1>
          <p class="voice mt-3 max-w-[400px]">{{ t('error.storageBody') }}</p>

          <div v-if="session.error" class="mt-5 rounded-lg bg-error/5 px-3.5 py-3">
            <div class="text-[11px] font-medium text-error/80">{{ t('session.details') }}</div>
            <p class="mt-1.5 font-mono text-[11px] text-error">{{ session.error }}</p>
          </div>

          <div class="mt-7 flex flex-wrap items-center gap-2">
            <button class="btn btn-primary h-9" type="button" @click="session.retry()">
              <Renew16 />
              {{ t('session.tryAgain') }}
            </button>
          </div>
        </div>
      </Transition>
    </main>

    <footer
      class="relative z-10 flex h-8 shrink-0 items-center gap-2 border-t border-line px-4 text-[11px] text-faint"
    >
      <span>{{ t('app.product') }}</span>
      <span class="text-line" aria-hidden="true">/</span>
      <span>{{ t('app.network') }}</span>
      <span class="ml-auto flex items-center gap-1.5">
        <span
          class="dot"
          :class="state === 'loading' ? 'bg-primary pulse-soft' : 'bg-error'"
          aria-hidden="true"
        />
        {{ state === 'loading' ? t('session.connectingIndicator') : t('session.signedOut') }}
      </span>
    </footer>
  </div>
</template>
