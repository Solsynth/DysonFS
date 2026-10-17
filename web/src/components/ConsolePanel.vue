<script setup lang="ts">
defineProps<{
  title: string
  note?: string
}>()
</script>

<template>
  <section class="border border-line bg-base-200/30">
    <header class="flex flex-wrap items-baseline justify-between gap-2 px-4 pb-1 pt-3">
      <h2 class="text-2xs uppercase tracking-[0.16em] text-dim">{{ title }}</h2>
      <span v-if="note" class="text-2xs text-faint">{{ note }}</span>
      <slot name="header" />
    </header>
    <div class="px-4 py-4">
      <slot />
    </div>
    <footer v-if="$slots.footer" class="px-4 pb-3 pt-1">
      <slot name="footer" />
    </footer>
  </section>
</template>
