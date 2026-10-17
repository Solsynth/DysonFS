<script setup lang="ts">
import { Checkmark16, Close16, Error16, Information16 } from '@carbon/icons-vue'

import { useNoticeStore } from '@/stores/notice'

const notice = useNoticeStore()
</script>

<template>
  <div
    class="pointer-events-none fixed right-3 top-14 z-[70] flex w-[340px] max-w-[calc(100vw-24px)] flex-col gap-1.5"
    aria-live="polite"
  >
    <TransitionGroup
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="translate-y-1 opacity-0"
      leave-active-class="transition duration-150 ease-in"
      leave-to-class="opacity-0"
    >
      <div
        v-for="item in notice.items"
        :key="item.id"
        class="pointer-events-auto flex items-start gap-2 border border-line bg-base-200/95 px-3 py-2.5 shadow-[0_10px_32px_rgba(0,0,0,0.4)] backdrop-blur-sm"
        :class="{
          'border-l-2 border-l-success': item.tone === 'ok',
          'border-l-2 border-l-error': item.tone === 'error',
          'border-l-2 border-l-primary': item.tone === 'info',
        }"
        role="status"
      >
        <Checkmark16 v-if="item.tone === 'ok'" class="mt-0.5 h-3.5 w-3.5 shrink-0 text-success" />
        <Error16 v-else-if="item.tone === 'error'" class="mt-0.5 h-3.5 w-3.5 shrink-0 text-error" />
        <Information16 v-else class="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" />
        <p class="min-w-0 flex-1 text-2xs leading-relaxed text-dim">{{ item.message }}</p>
        <button
          type="button"
          class="shrink-0 text-faint transition-colors hover:text-base-content"
          aria-label="Dismiss notice"
          @click="notice.dismiss(item.id)"
        >
          <Close16 class="h-3.5 w-3.5" />
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>
