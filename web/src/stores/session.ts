import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { DriveError, onUnauthorized } from '@/lib/api/client'
import { driveApi } from '@/lib/api/drive'
import type { AccountIdentity } from '@/lib/api/types'
import { enableSampleData, isSampleData, sampleIdentity } from '@/lib/demo'

type SessionState = 'loading' | 'ready' | 'signed-out' | 'offline'

export const useSessionStore = defineStore('session', () => {
  const state = ref<SessionState>('loading')
  const identity = ref<AccountIdentity | null>(null)
  const error = ref('')
  const sample = ref(isSampleData())

  const signedIn = computed(() => state.value === 'ready' && identity.value !== null)
  const authUrl = computed(() => {
    const base = import.meta.env.VITE_AUTH_URL || 'https://solsynth.dev/auth'
    try {
      const url = new URL(base, window.location.origin)
      url.searchParams.set('next', window.location.href)
      return url.toString()
    } catch {
      return base
    }
  })

  onUnauthorized(() => {
    if (sample.value) return
    identity.value = null
    state.value = 'signed-out'
    error.value = 'The session expired. Sign in again to reach the drive.'
  })

  /** Resolves the signed-in account once at boot. */
  async function load() {
    sample.value = isSampleData()
    if (sample.value) {
      identity.value = sampleIdentity
      state.value = 'ready'
      error.value = ''
      return
    }

    state.value = 'loading'
    error.value = ''
    try {
      identity.value = await driveApi.account()
      state.value = 'ready'
    } catch (caught) {
      if (caught instanceof DriveError && caught.isUnauthenticated) {
        identity.value = null
        state.value = 'signed-out'
        return
      }
      // Older deployments may not expose /api/account/me. If billing answers,
      // the session is valid and the drive remains usable without a name.
      if (caught instanceof DriveError && (caught.status === 404 || caught.status === 405)) {
        try {
          await driveApi.usage()
          identity.value = {
            id: 'session',
            name: 'operator',
            nick: 'Operator',
            is_superuser: false,
          }
          state.value = 'ready'
          return
        } catch (fallback) {
          if (fallback instanceof DriveError && fallback.isUnauthenticated) {
            identity.value = null
            state.value = 'signed-out'
            return
          }
          error.value = fallback instanceof Error ? fallback.message : 'The drive did not answer.'
          state.value = 'offline'
          return
        }
      }
      error.value = caught instanceof Error ? caught.message : 'The drive did not answer.'
      state.value = 'offline'
    }
  }

  async function retry() {
    await load()
  }

  function startSample() {
    enableSampleData(true)
    sample.value = true
    identity.value = sampleIdentity
    state.value = 'ready'
    error.value = ''
  }

  function stopSample() {
    enableSampleData(false)
    sample.value = false
    identity.value = null
    state.value = 'signed-out'
  }

  return {
    state,
    identity,
    error,
    sample,
    signedIn,
    authUrl,
    load,
    retry,
    startSample,
    stopSample,
  }
})
