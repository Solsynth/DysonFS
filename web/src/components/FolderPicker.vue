<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ChevronRight16, Folder16 } from '@carbon/icons-vue'

import type { CloudFile } from '@/lib/api/types'
import { useVaultStore } from '@/stores/vault'

interface Crumb {
  id: string | null
  name: string
}

const props = withDefaults(
  defineProps<{
    initialId?: string | null
    exclude?: string[]
  }>(),
  { initialId: null, exclude: () => [] },
)

const emit = defineEmits<{
  change: [payload: { id: string | null; path: string }]
}>()

const vault = useVaultStore()

const path = ref<Crumb[]>([{ id: null, name: 'My Drive' }])
const folders = ref<CloudFile[]>([])
const loading = ref(false)
const error = ref('')

const currentId = computed(() => path.value[path.value.length - 1]?.id ?? null)
const pathLabel = computed(() => path.value.map((crumb) => crumb.name).join(' / '))
const canMoveHere = computed(() => !props.exclude.includes(currentId.value ?? ''))

function initialPath(): Crumb[] {
  if (props.initialId && vault.folderId === props.initialId && vault.crumbs.length > 1) {
    return vault.crumbs.map((crumb) => ({ id: crumb.id, name: crumb.name }))
  }
  return [{ id: null, name: 'My Drive' }]
}

onMounted(() => {
  path.value = initialPath()
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const list = await vault.listFolders(currentId.value)
    folders.value = list.filter((folder) => !props.exclude.includes(folder.id))
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : 'The folder list was unreadable.'
    folders.value = []
  } finally {
    loading.value = false
  }
}

watch(
  currentId,
  () => {
    emit('change', { id: currentId.value, path: pathLabel.value })
    void load()
  },
  { immediate: true },
)

function enter(folder: CloudFile) {
  if (props.exclude.includes(folder.id)) return
  path.value = [...path.value, { id: folder.id, name: folder.name }]
}

function jump(index: number) {
  path.value = path.value.slice(0, index + 1)
}
</script>

<template>
  <div class="border border-line bg-base-200/30">
    <div class="flex flex-wrap items-center gap-1 border-b border-line px-2.5 py-2 text-2xs">
      <template v-for="(crumb, index) in path" :key="crumb.id ?? 'root'">
        <span v-if="index > 0" class="text-line" aria-hidden="true">/</span>
        <button
          type="button"
          class="max-w-[140px] truncate px-0.5 transition-colors"
          :class="index === path.length - 1 ? 'text-base-content' : 'text-dim hover:text-base-content'"
          @click="jump(index)"
        >
          {{ crumb.name }}
        </button>
      </template>
    </div>

    <div class="h-[190px] overflow-y-auto scroll-region">
      <div v-if="loading" class="space-y-2 p-3">
        <div v-for="row in 3" :key="row" class="h-6 bg-base-300/60 pulse-soft" />
      </div>

      <p v-else-if="error" class="px-3 py-5 text-2xs text-error">{{ error }}</p>

      <div v-else-if="folders.length === 0" class="px-3 py-8 text-center">
        <Folder16 class="mx-auto h-5 w-5 text-faint" />
        <p class="mt-2 text-2xs text-faint">No folders under here.</p>
        <p class="mt-0.5 text-3xs text-faint">You can still move objects to this location.</p>
      </div>

      <template v-else>
        <button
          v-for="folder in folders"
          :key="folder.id"
          type="button"
          class="flex w-full items-center gap-2 border-b border-line-soft px-3 py-2 text-left text-2xs text-dim transition-colors last:border-0 hover:bg-base-200/60 hover:text-base-content"
          @click="enter(folder)"
        >
          <Folder16 class="h-3.5 w-3.5 shrink-0 text-brass" />
          <span class="min-w-0 flex-1 truncate">{{ folder.name }}</span>
          <span class="text-3xs tabular-nums text-faint">{{ folder.children_count ?? 0 }}</span>
          <ChevronRight16 class="h-3.5 w-3.5 shrink-0 text-faint" />
        </button>
      </template>
    </div>

    <div class="flex items-center justify-between gap-2 border-t border-line px-3 py-1.5 text-3xs">
      <span class="truncate text-faint">destination · {{ pathLabel }}</span>
      <span :class="canMoveHere ? 'text-success' : 'text-error'">
        {{ canMoveHere ? 'writable' : 'unavailable' }}
      </span>
    </div>
  </div>
</template>
