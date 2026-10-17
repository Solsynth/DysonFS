import { createRouter, createWebHistory } from 'vue-router'

declare module 'vue-router' {
  interface RouteMeta {
    mode?: 'drive' | 'unindexed' | 'recycle' | 'preview' | 'storage'
    title?: string
  }
}

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', redirect: '/drive' },
    {
      path: '/drive',
      name: 'drive',
      component: () => import('@/views/DriveView.vue'),
      meta: { mode: 'drive', title: 'My Drive' },
    },
    {
      path: '/drive/folder/:id',
      name: 'folder',
      component: () => import('@/views/DriveView.vue'),
      meta: { mode: 'drive', title: 'Folder' },
    },
    {
      path: '/unindexed',
      name: 'unindexed',
      component: () => import('@/views/DriveView.vue'),
      meta: { mode: 'unindexed', title: 'Unindexed' },
    },
    {
      path: '/recycle',
      name: 'recycle',
      component: () => import('@/views/DriveView.vue'),
      meta: { mode: 'recycle', title: 'Recycle Bin' },
    },
    {
      // Preview is a tab of its own: the file's bytes are the tab's content,
      // so several files can be read side by side.
      path: '/file/:id',
      name: 'preview',
      component: () => import('@/views/PreviewView.vue'),
      meta: { mode: 'preview', title: 'Preview' },
    },
    {
      path: '/storage',
      name: 'storage',
      component: () => import('@/views/StorageView.vue'),
      meta: { mode: 'storage', title: 'Storage' },
    },
    { path: '/:pathMatch(.*)*', redirect: '/drive' },
  ],
  scrollBehavior: () => ({ top: 0 }),
})

router.afterEach((to) => {
  const title = typeof to.meta.title === 'string' ? to.meta.title : null
  document.title = title ? `${title} · SnFS` : 'SnFS'
})

export default router
