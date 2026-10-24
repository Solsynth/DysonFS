<script setup lang="ts">
/**
 * The picker's own session gate.
 *
 * The drive's gate is a full page with its own chrome; inside a 560×480 frame
 * that chrome would take the whole surface. More importantly a frame cannot
 * complete a sign-in redirect on its own — the identity host refuses to be
 * framed, and the cookie would land third-party — so the sign-in control
 * leaves the frame and the host is told what happened. A cross-site host
 * never reaches this gate: it hands the frame a token or a single-use
 * hand-off code in `picker:open`, which the frame exchanges for a session
 * before the cookie lookup settles.
 */
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CloudOffline16, Login16, Renew16 } from '@carbon/icons-vue'

import { useSessionStore } from '@/stores/session'

const props = defineProps<{ embedded: boolean }>()
const emit = defineEmits<{ 'signed-out': []; ready: [] }>()

const session = useSessionStore()
const { t } = useI18n()

const state = computed(() => session.state)

function signIn() {
  // A new tab is the only place the shared sign-in page will render, and its
  // redirect target is this frame's own URL, so the frame reloads signed in.
  window.open(session.authUrl, '_blank', 'noopener')
  emit('signed-out')
}

watch(
  () => state.value,
  (next) => {
    if (next === 'signed-out') emit('signed-out')
    if (next === 'ready') emit('ready')
  },
  { immediate: true },
)
</script>

<template>
  <slot v-if="state === 'ready'" />

  <div v-else class="flex h-dvh flex-col items-center justify-center gap-3 px-6 text-center">
    <template v-if="state === 'loading'">
      <div class="loading-scan h-[2px] w-32 bg-line-soft" />
      <p class="text-2xs text-faint">{{ t('session.connecting') }}</p>
    </template>

    <template v-else-if="state === 'signed-out'">
      <Login16 class="h-5 w-5 text-faint" aria-hidden="true" />
      <h1 class="title text-[16px]">{{ t('session.signInRequired') }}</h1>
      <p class="voice max-w-[320px] text-[12px]">{{ t('session.signInBody') }}</p>
      <button type="button" class="btn btn-primary btn-sm" @click="signIn">
        {{ t('session.signIn') }}
      </button>
      <p v-if="props.embedded" class="text-2xs text-faint">Opens in a new tab.</p>
    </template>

    <template v-else>
      <CloudOffline16 class="h-5 w-5 text-error" aria-hidden="true" />
      <h1 class="title text-[16px]">{{ t('error.storageUnreachable') }}</h1>
      <p v-if="session.error" class="max-w-[320px] font-mono text-2xs text-error">
        {{ session.error }}
      </p>
      <button type="button" class="btn btn-ghost btn-sm" @click="session.retry()">
        <Renew16 />
        {{ t('session.tryAgain') }}
      </button>
    </template>
  </div>
</template>
