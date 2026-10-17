<script setup lang="ts">
import { computed } from 'vue'
import { ArrowRight16, CloudOffline16, FolderOpen16, Login16, Renew16 } from '@carbon/icons-vue'

import BrandMark from './BrandMark.vue'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()

const state = computed(() => session.state)
</script>

<template>
  <slot v-if="state === 'ready'" />

  <div v-else class="console-grid relative flex h-dvh flex-col overflow-hidden bg-ink text-base-content">
    <header class="relative z-10 flex h-12 shrink-0 items-center gap-3 border-b border-line px-4">
      <BrandMark :size="22" />
      <span class="flex items-baseline gap-2">
        <span class="text-[12px] font-semibold tracking-[0.26em]">DYSON</span>
        <span class="text-[12px] font-normal tracking-[0.16em] text-dim">DRIVE</span>
      </span>
      <span class="hidden h-4 w-px bg-line sm:block" aria-hidden="true" />
      <span class="hidden text-3xs uppercase tracking-[0.16em] text-faint sm:block">
        solar network / terminal
      </span>
      <span class="ml-auto text-3xs uppercase tracking-[0.12em] text-faint">node 01</span>
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
              <span class="eyebrow">link / establishing</span>
              <span class="text-3xs tabular-nums text-faint">attempt 01</span>
            </div>
            <div class="loading-scan mt-3 h-[2px] w-full bg-line" />
            <ul class="mt-5 space-y-2.5 text-2xs">
              <li class="flex items-center gap-2 text-dim">
                <span class="dot bg-success" aria-hidden="true" />
                transport resolved
                <span class="ml-auto text-faint">http/2</span>
              </li>
              <li class="flex items-center gap-2 text-dim">
                <span class="dot bg-brass pulse-soft" aria-hidden="true" />
                verifying session cookie
                <span class="ml-auto text-faint">AuthToken</span>
              </li>
              <li class="flex items-center gap-2 text-faint">
                <span class="dot bg-line" aria-hidden="true" />
                reading volume telemetry
                <span class="ml-auto">pending</span>
              </li>
            </ul>
          </div>
          <p class="mt-3 text-3xs uppercase tracking-[0.12em] text-faint">
            master · localhost:8080 / proxy /api
          </p>
        </div>

        <div v-else-if="state === 'signed-out'" key="signed-out" class="w-full max-w-[560px]">
          <div class="flex items-center gap-2 text-2xs uppercase tracking-[0.16em] text-brass">
            <Login16 aria-hidden="true" />
            auth / required
            <span class="text-faint">· 401</span>
          </div>
          <h1 class="title mt-4 text-[34px] leading-tight text-base-content">
            The drive is sealed.
          </h1>
          <p class="voice mt-3 max-w-[460px]">
            DysonFS delegates identity to Solar Network. This terminal needs a valid session before
            it can read a single object — no anonymous mounts, no public index.
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
              <span class="text-faint">session transport</span>
              <span class="text-dim">HttpOnly cookie · same-origin</span>
            </div>
            <div class="flex items-center justify-between px-3.5 py-2.5">
              <span class="text-faint">return path</span>
              <span class="max-w-[280px] truncate text-dim">{{ session.authUrl }}</span>
            </div>
            <div class="flex items-center justify-between px-3.5 py-2.5">
              <span class="text-faint">sample mode</span>
              <span class="text-dim">local fixtures · no writes</span>
            </div>
          </div>
        </div>

        <div v-else key="offline" class="w-full max-w-[560px]">
          <div class="flex items-center gap-2 text-2xs uppercase tracking-[0.16em] text-error">
            <CloudOffline16 aria-hidden="true" />
            link / refused
            <span class="text-faint">· no carrier</span>
          </div>
          <h1 class="title mt-4 text-[34px] leading-tight text-base-content">No carrier.</h1>
          <p class="voice mt-3 max-w-[460px]">
            The terminal reached the edge of the network but the DysonFS master did not answer.
            Confirm the service is running, then re-establish the link.
          </p>

          <div class="panel mt-6 border-error/30 bg-error/5 px-4 py-3">
            <div class="eyebrow text-error/80">diagnostic</div>
            <p class="mt-1.5 font-mono text-2xs text-error">{{ session.error || 'No response from the master API.' }}</p>
          </div>

          <div class="mt-7 flex flex-wrap items-center gap-2.5">
            <button class="btn btn-primary h-9" type="button" @click="session.retry()">
              <Renew16 class="mr-1.5" />
              Retry link
            </button>
            <button class="btn btn-outline h-9" type="button" @click="session.startSample()">
              <FolderOpen16 class="mr-1.5" />
              Continue in sample mode
            </button>
          </div>
        </div>

      </Transition>
    </main>

    <footer class="relative z-10 flex h-7 shrink-0 items-center gap-3 border-t border-line px-3 text-3xs text-faint">
      <span class="uppercase tracking-[0.12em]">dysonfs</span>
      <span class="hidden h-2.5 w-px bg-line sm:block" aria-hidden="true" />
      <span class="hidden sm:block">object instrument / build 0.1</span>
      <span class="ml-auto flex items-center gap-1.5">
        <span
          class="dot"
          :class="state === 'loading' ? 'bg-brass pulse-soft' : 'bg-error'"
          aria-hidden="true"
        />
        {{ state === 'loading' ? 'linking' : 'unlinked' }}
      </span>
    </footer>
  </div>
</template>

<style scoped>
.console-grid {
  background-image:
    radial-gradient(circle at 50% 32%, color-mix(in oklab, var(--color-primary) 9%, transparent), transparent 42%),
    linear-gradient(to right, var(--color-line-soft) 1px, transparent 1px),
    linear-gradient(to bottom, var(--color-line-soft) 1px, transparent 1px);
  background-size:
    100% 100%,
    64px 64px,
    64px 64px;
}
.console-grid::before {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(to bottom, transparent 60%, var(--color-base) 100%);
  pointer-events: none;
}
</style>
