import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { DriveError, onUnauthorized } from '@/lib/api/client'
import { driveApi } from '@/lib/api/drive'
import type { AccountIdentity } from '@/lib/api/types'
import { signInUrl } from '@/lib/auth'

type SessionState = 'loading' | 'ready' | 'signed-out' | 'offline'

export const useSessionStore = defineStore('session', () => {
  const state = ref<SessionState>('loading')
  const identity = ref<AccountIdentity | null>(null)
  const error = ref('')

  const signedIn = computed(() => state.value === 'ready' && identity.value !== null)
  // The identity console's shared sign-in page, with this drive as the place
  // to come back to. Both origins share the session cookie's domain.
  const authUrl = computed(signInUrl)

  onUnauthorized(() => {
    identity.value = null
    state.value = 'signed-out'
    error.value = 'The session expired. Sign in again to reach the drive.'
  })

  /** Resolves the signed-in account once at boot. */
  async function load() {
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

  return {
    state,
    identity,
    error,
    signedIn,
    authUrl,
    load,
    retry,
  }
})
