<script setup lang="ts">
import { computed } from 'vue'

import type { CloudFile } from '@/lib/api/types'
import { fileKind } from '@/lib/fileKinds'

const props = withDefaults(
  defineProps<{
    file: CloudFile
    size?: 'sm' | 'md' | 'lg'
  }>(),
  { size: 'sm' },
)

const kind = computed(() => fileKind(props.file))
const dimension = computed(() =>
  props.size === 'lg' ? 'h-8 w-8' : props.size === 'md' ? 'h-5 w-5' : 'h-4 w-4',
)
</script>

<template>
  <component :is="kind.icon" class="shrink-0" :class="[kind.tone, dimension]" aria-hidden="true" />
</template>
