<script setup lang="ts">
/**
 * The picker frame: the page a host embeds with `<iframe src="…/picker">`.
 *
 * Two things happen here and nowhere else. Options arrive either from the
 * query string (the simplest embed) or from a `picker:open` message (so a host
 * can reuse one frame for many requests). The answer leaves as a
 * `picker:action` message addressed to the embedding page's origin — never
 * `*`, so a frame that someone else's page has framed cannot be read on the
 * quiet.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import PickerPanel from '@/components/picker/PickerPanel.vue'
import PickerSessionGate from '@/components/picker/PickerSessionGate.vue'
import { setAuthToken } from '@/lib/api/client'
import { i18n } from '@/lib/i18n'
import { applyLocale, applyTheme } from '@/lib/prefs'
import {
  normalizeOptions,
  optionsFromQuery,
  parseHostMessage,
  PICKER_CHANNEL,
  type FrameMessage,
  type PickerFile,
  type PickerOptions,
} from '@/lib/picker/protocol'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const options = ref<PickerOptions>(optionsFromQuery(window.location.search))
const embedded = ref(window.parent !== window)
const result = ref<PickerFile[] | null>(null)
/** Where answers go; empty until the frame knows the page that framed it. */
const hostOrigin = ref('')
const panel = ref<InstanceType<typeof PickerPanel> | null>(null)
/** Set once a host has supplied a token, so a cookie miss is not reported. */
const tokenReceived = ref(false)
let signedOutTimer: number | undefined

const standalone = computed(() => !embedded.value)
const done = computed(() => result.value !== null)

function referrerOrigin(): string {
  try {
    const origin = new URL(document.referrer).origin
    return origin === 'null' ? '' : origin
  } catch {
    return ''
  }
}

function post(message: FrameMessage) {
  if (!embedded.value || !hostOrigin.value) return
  window.parent.postMessage(message, hostOrigin.value)
}

/**
 * A frame cannot read its parent's origin, so the host proves itself: the
 * referrer names the page that framed us, and every accepted message adopts
 * its origin when the referrer is absent (a `no-referrer` policy).
 */
function adoptHost(origin: string) {
  if (hostOrigin.value) return
  if (origin && origin !== 'null') hostOrigin.value = origin
  else hostOrigin.value = referrerOrigin()
}

function onMessage(event: MessageEvent) {
  if (event.source !== window.parent) return
  const message = parseHostMessage(event.data)
  if (!message) return
  adoptHost(event.origin)

  if (message.type === 'picker:open') {
    result.value = null
    options.value = normalizeOptions(message.options as Record<string, unknown>)
    // A host on another site cannot share the drive's cookie, so it hands the
    // frame a token instead. Re-resolve the account when one arrives: the
    // gate may already have settled as signed-out.
    if (message.token) {
      tokenReceived.value = true
      if (signedOutTimer) window.clearTimeout(signedOutTimer)
      setAuthToken(message.token)
      void session.load()
    }
    return
  }
  emitCancelled()
}

function emitCancelled() {
  post({ channel: PICKER_CHANNEL, type: 'picker:action', action: 'cancelled' })
  if (standalone.value) result.value = []
}

function onPicked(files: PickerFile[]) {
  result.value = files
  post({ channel: PICKER_CHANNEL, type: 'picker:action', action: 'picked', files })
}

/**
 * The cookie lookup and a token-bearing `picker:open` race: the frame mounts,
 * finds no cookie, and only then hears from the host. Reporting signed-out
 * immediately would make a token host tear the frame down before it could
 * authenticate, so an embedded frame waits out that window.
 */
function onSignedOut() {
  if (!embedded.value || tokenReceived.value) {
    post({ channel: PICKER_CHANNEL, type: 'picker:signed-out' })
    return
  }
  if (signedOutTimer) window.clearTimeout(signedOutTimer)
  signedOutTimer = window.setTimeout(() => {
    if (tokenReceived.value) return
    post({ channel: PICKER_CHANNEL, type: 'picker:signed-out' })
  }, 800)
}

function onError(message: string) {
  post({ channel: PICKER_CHANNEL, type: 'picker:error', message })
}

function reset() {
  result.value = null
}

// The host decides the language and the theme of the frame it embeds: its own
// page already chose both, and a frame in the other theme reads as a mistake.
watch(
  () => options.value.locale,
  (locale) => {
    if (!locale) return
    i18n.global.locale.value = locale
    applyLocale(locale)
  },
  { immediate: true },
)

watch(
  () => options.value.theme,
  (theme) => {
    if (!theme || theme === 'auto') return
    applyTheme(theme)
  },
  { immediate: true },
)

// Escape inside the frame is the person cancelling. The frame cannot close the
// dialog it sits in, so the host has to hear about the key.
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && embedded.value) emitCancelled()
}

onMounted(() => {
  hostOrigin.value = referrerOrigin()
  window.addEventListener('message', onMessage)
  window.addEventListener('keydown', onKeydown)
  // The cookie-backed session is the common case (a same-site host); a token
  // from `picker:open` replaces it when the host is elsewhere.
  void session.load()
  post({ channel: PICKER_CHANNEL, type: 'picker:ready' })
})

onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage)
  window.removeEventListener('keydown', onKeydown)
  if (signedOutTimer) window.clearTimeout(signedOutTimer)
})

defineExpose({ panel })
</script>

<template>
  <PickerSessionGate :embedded="embedded" @signed-out="onSignedOut">
    <div class="flex h-dvh min-h-0 flex-col">
      <PickerPanel
        v-if="!done"
        ref="panel"
        class="min-h-0 flex-1"
        :options="options"
        @picked="onPicked"
        @cancelled="emitCancelled"
        @error="onError"
      />

      <div v-else class="flex min-h-0 flex-1 flex-col">
        <header class="border-b border-line px-4 py-3">
          <h1 class="title text-[16px]">{{ options.title || 'Selected' }}</h1>
          <p class="voice mt-0.5 text-[12px]">
            The answer went back to the page that opened this frame.
          </p>
        </header>
        <ul
          v-if="result && result.length"
          class="min-h-0 flex-1 divide-y divide-line-soft overflow-y-auto"
        >
          <li
            v-for="file in result"
            :key="file.id"
            class="flex items-center gap-2 px-4 py-2 text-[12px]"
          >
            <span class="min-w-0 flex-1 truncate">{{ file.name }}</span>
            <code class="text-2xs text-faint">{{ file.id }}</code>
          </li>
        </ul>
        <p v-else class="voice flex-1 px-4 py-10 text-center text-[12px]">Nothing was selected.</p>
        <footer v-if="standalone" class="border-t border-line px-4 py-2.5">
          <button type="button" class="btn btn-ghost btn-sm" @click="reset">Choose again</button>
        </footer>
      </div>
    </div>
  </PickerSessionGate>
</template>
