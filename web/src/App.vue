<script setup lang="ts">
import { onMounted } from 'vue'

import AppShell from './components/AppShell.vue'
import SessionGate from './components/SessionGate.vue'
import { useSessionStore } from './stores/session'

const session = useSessionStore()

onMounted(() => {
  void session.load()
})
</script>

<template>
  <SessionGate>
    <AppShell />
  </SessionGate>
</template>
