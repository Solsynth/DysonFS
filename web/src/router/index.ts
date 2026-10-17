import { createRouter, createWebHistory } from 'vue-router'

declare module 'vue-router' {
  interface RouteMeta {
    mode?: 'drive' | 'unindexed' | 'recycle'
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
      path: '/storage',
      name: 'storage',
      component: () => import('@/views/StorageView.vue'),
      meta: { title: 'Storage' },
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
