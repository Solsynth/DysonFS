<script setup lang="ts">
import { computed } from 'vue'
import {
  ArrowRight16,
  CloudOffline16,
  FolderOpen16,
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

const state = computed(() => session.state)
</script>

<template>
  <slot v-if="state === 'ready'" />

  <div v-else class="relative flex h-dvh flex-col overflow-hidden bg-base-100 text-base-content">
    <header class="relative z-10 flex h-12 shrink-0 items-center gap-3 border-b border-line px-4">
      <BrandMark :size="22" />
      <span class="flex items-baseline gap-2">
        <span class="text-[13px] font-semibold tracking-[0.24em]">DYSON</span>
        <span class="text-2xs font-normal tracking-[0.16em] text-dim">DRIVE</span>
      </span>
      <span class="hidden h-4 w-px bg-line sm:block" aria-hidden="true" />
      <span class="hidden text-2xs uppercase tracking-[0.14em] text-faint sm:block">
        solar network
      </span>
      <IconButton
        class="ml-auto"
        :label="ui.isDark ? 'Switch to light theme' : 'Switch to dark theme'"
        @click="ui.toggleTheme()"
      >
        <Sun16 v-if="ui.isDark" />
        <Moon16 v-else />
      </IconButton>
    </header>

    <main class="relative z-10 flex flex-1 items-center justify-center overflow-y-auto px-4 py-10">
      <Transition
        mode="out-in"
        enter-active-class="transition duration-200 ease-out"
        enter-from-class="opacity-0 translate-y-1"
        leave-active-class="transition duration-100 ease-in"
        leave-to-class="opacity-0"
      >
        <div v-if="state === 'loading'" key="loading" class="w-full max-w-[520px]">
          <div class="flex items-center gap-3.5">
            <BrandMark :size="42" />
            <div>
              <div class="eyebrow">solar network</div>
              <h1 class="title text-2xl text-base-content">Dyson Drive</h1>
            </div>
          </div>

          <div class="panel mt-8 p-5">
            <div class="flex items-center justify-between">
              <span class="eyebrow">connecting</span>
              <span class="text-2xs text-faint">this takes a moment</span>
            </div>
            <div class="loading-scan mt-3 h-[2px] w-full bg-line" />
            <p class="voice mt-4">
              Checking your session with Solar Network, then loading your drive.
            </p>
          </div>
        </div>

        <div v-else-if="state === 'signed-out'" key="signed-out" class="w-full max-w-[560px]">
          <div class="flex items-center gap-2 text-2xs uppercase tracking-[0.16em] text-brass">
            <Login16 aria-hidden="true" />
            sign in required
          </div>
          <h1 class="title mt-4 text-[34px] leading-tight text-base-content">
            The drive is sealed.
          </h1>
          <p class="voice mt-3 max-w-[460px]">
            Dyson Drive reads your files through your Solar Network account. Sign in to reach the
            drive, or look around with example files first.
          </p>

          <div class="mt-7 flex flex-wrap items-center gap-2.5">
            <a :href="session.authUrl" class="btn btn-primary h-9">
              Sign in with Solar Network
              <ArrowRight16 class="ml-1" />
            </a>
            <button class="btn btn-outline h-9" type="button" @click="session.startSample()">
              <FolderOpen16 class="mr-1.5" />
              Explore sample data
            </button>
          </div>

          <div class="panel mt-8 divide-y divide-line text-2xs">
            <div class="flex items-center justify-between px-3.5 py-2.5">
              <span class="text-faint">account</span>
              <span class="text-dim">your Solar Network account</span>
            </div>
            <div class="flex items-center justify-between px-3.5 py-2.5">
              <span class="text-faint">sample mode</span>
              <span class="text-dim">example files, nothing is saved</span>
            </div>
          </div>
        </div>

        <div v-else key="offline" class="w-full max-w-[560px]">
          <div class="flex items-center gap-2 text-2xs uppercase tracking-[0.16em] text-error">
            <CloudOffline16 aria-hidden="true" />
            drive unreachable
          </div>
          <h1 class="title mt-4 text-[34px] leading-tight text-base-content">
            The drive is unreachable.
          </h1>
          <p class="voice mt-3 max-w-[460px]">
            Dyson Drive could not reach your storage. Check your connection, then try again, or look
            around with example files.
          </p>

          <div v-if="session.error" class="panel mt-6 border-error/30 bg-error/5 px-4 py-3">
            <div class="eyebrow text-error/80">details</div>
            <p class="mt-1.5 font-mono text-2xs text-error">{{ session.error }}</p>
          </div>

          <div class="mt-7 flex flex-wrap items-center gap-2.5">
            <button class="btn btn-primary h-9" type="button" @click="session.retry()">
              <Renew16 class="mr-1.5" />
              Try again
            </button>
            <button class="btn btn-outline h-9" type="button" @click="session.startSample()">
              <FolderOpen16 class="mr-1.5" />
              Continue in sample mode
            </button>
          </div>
        </div>
      </Transition>
    </main>

    <footer
      class="relative z-10 flex h-7 shrink-0 items-center gap-3 border-t border-line px-3 text-2xs text-faint"
    >
      <span class="uppercase tracking-[0.12em]">dyson drive</span>
      <span class="hidden h-2.5 w-px bg-line sm:block" aria-hidden="true" />
      <span class="hidden sm:block">solar network</span>
      <span class="ml-auto flex items-center gap-1.5">
        <span
          class="dot"
          :class="state === 'loading' ? 'bg-brass' : 'bg-error'"
          aria-hidden="true"
        />
        {{ state === 'loading' ? 'connecting' : 'signed out' }}
      </span>
    </footer>
  </div>
</template>
