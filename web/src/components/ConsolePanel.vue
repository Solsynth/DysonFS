<script setup lang="ts">
defineProps<{
  title: string
  note?: string
}>()
</script>

<template>
  <section class="rounded-xl bg-base-200/60">
    <header class="flex flex-wrap items-baseline justify-between gap-2 px-4 pb-1 pt-3.5">
      <h2 class="text-[11px] font-medium text-faint">{{ title }}</h2>
      <span v-if="note" class="text-[11px] text-faint">{{ note }}</span>
      <slot name="header" />
    </header>
    <div class="px-4 py-3.5">
      <slot />
    </div>
    <footer v-if="$slots.footer" class="px-4 pb-3.5 pt-1">
      <slot name="footer" />
    </footer>
  </section>
</template>
