<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Close16, Folder16, Image16, StoragePool16, TrashCan16, WarningAlt16 } from '@carbon/icons-vue'
import type { Component } from 'vue'

import { useTabStore, type DriveTab, type TabKind } from '@/stores/tabs'

const tabs = useTabStore()
const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const icons: Record<TabKind, Component> = {
  folder: Folder16,
  preview: Image16,
  recycle: TrashCan16,
  unindexed: WarningAlt16,
  storage: StoragePool16,
}

const dragging = ref<number | null>(null)

const activePath = computed(() => route.fullPath)
const hasTabs = computed(() => tabs.open.length > 0)

function select(tab: DriveTab) {
  if (tab.id === activePath.value) return
  void router.push(tab.id)
}

async function close(tab: DriveTab, event: MouseEvent) {
  event.stopPropagation()
  const next = tabs.close(tab.id)
  if (tab.id !== activePath.value) return
  await router.push(next ?? '/drive')
}

function onDragStart(index: number, event: DragEvent) {
  dragging.value = index
  event.dataTransfer?.setData('text/plain', String(index))
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
}

function onDrop(index: number, event: DragEvent) {
  event.preventDefault()
  const raw = event.dataTransfer?.getData('text/plain')
  const from = raw ? Number(raw) : dragging.value
  dragging.value = null
  if (from === null || Number.isNaN(from)) return
  tabs.reorder(from, index)
}
</script>

<template>
  <div
    v-if="hasTabs"
    class="flex h-10 shrink-0 items-stretch border-b border-line bg-ink"
    role="tablist"
    :aria-label="t('tabs.label')"
  >
    <div class="flex min-w-0 flex-1 items-stretch overflow-x-auto">
      <div
        v-for="(tab, index) in tabs.open"
        :key="tab.id"
        class="group flex min-w-[132px] max-w-[228px] shrink-0 items-stretch border-r border-line"
        :draggable="true"
        @dragstart="onDragStart(index, $event)"
        @dragover.prevent
        @drop="onDrop(index, $event)"
      >
        <!-- The accent is a top rule rather than a fill: the strip stays flat,
             so a tab's boundary is the tab itself. -->
        <button
          type="button"
          role="tab"
          class="flex min-w-0 flex-1 items-center gap-1.5 pl-3 pr-1 text-left text-xs transition-colors duration-150"
          :class="tab.id === activePath ? 'border-t-2 border-primary text-base-content' : 'border-t-2 border-transparent text-faint hover:text-dim'"
          :aria-selected="tab.id === activePath"
          :aria-current="tab.id === activePath ? 'page' : undefined"
          :title="tab.note ? `${tab.name} · ${tab.note}` : tab.name"
          @click="select(tab)"
          @auxclick.middle.prevent="close(tab, $event)"
        >
          <component :is="icons[tab.kind]" class="shrink-0" aria-hidden="true" />
          <span class="truncate">{{ tab.name }}</span>
        </button>
        <button
          type="button"
          class="grid w-6 shrink-0 place-items-center text-faint transition-colors hover:bg-base-300 hover:text-base-content"
          :class="tab.id === activePath ? 'opacity-60' : 'opacity-0 group-hover:opacity-100'"
          :aria-label="t('tabs.closeNamed', { name: tab.name })"
          :title="t('tabs.closeNamed', { name: tab.name })"
          @click="close(tab, $event)"
        >
          <Close16 class="h-3.5 w-3.5" />
        </button>
      </div>
    </div>

    <div class="flex shrink-0 items-center gap-1 border-l border-line px-2">
      <button
        v-if="tabs.previewCount > 0"
        type="button"
        class="btn btn-ghost btn-xs"
        :title="t('tabs.closePreviews')"
        @click="tabs.closePreviews()"
      >
        {{ t('tabs.closePreviews') }}
      </button>
      <button
        type="button"
        class="btn btn-ghost btn-xs"
        :title="t('tabs.closeAll')"
        @click="tabs.closeAll()"
      >
        {{ t('tabs.closeAll') }}
      </button>
    </div>
  </div>
</template>
