<script setup lang="ts">
withDefaults(
  defineProps<{
    size?: number
  }>(),
  { size: 22 },
)
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 32 32"
    fill="none"
    aria-hidden="true"
    class="shrink-0"
  >
    <circle cx="16" cy="16" r="5.1" stroke="currentColor" stroke-width="1.2" />
    <circle cx="16" cy="16" r="1.25" fill="currentColor" />
    <ellipse
      cx="16"
      cy="16"
      rx="13.2"
      ry="4.6"
      transform="rotate(-28 16 16)"
      stroke="currentColor"
      stroke-width="1"
      opacity="0.86"
    />
    <ellipse
      cx="16"
      cy="16"
      rx="12.4"
      ry="4.1"
      transform="rotate(48 16 16)"
      stroke="var(--color-brass)"
      stroke-width="1"
      opacity="0.55"
    />
    <path
      d="M16 1.4v2.1M16 28.5v2.1M1.4 16h2.1M28.5 16h2.1"
      stroke="currentColor"
      stroke-width="1"
      opacity="0.4"
    />
  </svg>
</template>
