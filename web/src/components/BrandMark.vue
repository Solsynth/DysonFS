<script setup lang="ts">
withDefaults(
  defineProps<{
    size?: number
  }>(),
  { size: 22 },
)
</script>

<template>
  <span
    class="brand-mark shrink-0"
    :style="{ width: `${size}px`, height: `${size}px` }"
    aria-hidden="true"
  />
</template>
