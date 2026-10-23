import { fileURLToPath, URL } from 'node:url'

import type { Plugin } from 'vite'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import vueJsx from '@vitejs/plugin-vue-jsx'
import vueDevTools from 'vite-plugin-vue-devtools'
import { defineConfig, loadEnv } from 'vite'


/**
 * Serves `/picker` from `picker.html` in development, mirroring the master's
 * route (internal/webui). Without it a host pointed at `/picker` works in
 * production and 404s locally, which is exactly the bug an embed hides.
 */
function pickerRoute(): Plugin {
  return {
    name: 'snfs-picker-route',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const url = req.url ?? ''
        if (url === '/picker' || url.startsWith('/picker?') || url === '/picker/') {
          req.url = `/picker.html${url.includes('?') ? url.slice(url.indexOf('?')) : ''}`
        }
        next()
      })
    },
  }
}
// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const apiTarget = env.DYSON_API_TARGET || 'http://localhost:8080'

  // Two documents share this build: the drive's own shell, and the picker page
  // a host embeds in a frame. The embeddable script that opens that frame is
  // built separately (vite.picker-sdk.config.ts) because it carries no Vue.
  return {
    plugins: [vue(), vueJsx(), vueDevTools(), tailwindcss(), pickerRoute()],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
    // Two documents ship: the drive at `/`, and the picker a host embeds at
    // `/picker`. `picker-demo.html` is a development page — Vite serves any
    // document in the root — and is deliberately not an input, so it never
    // deploys next to the drive.
    build: {
      rollupOptions: {
        input: {
          main: fileURLToPath(new URL('./index.html', import.meta.url)),
          picker: fileURLToPath(new URL('./picker.html', import.meta.url)),
        },
      },
    },
    server: {
      // The drive talks to DysonFS through the same origin in production so
      // the HttpOnly AuthToken cookie is first-party. Dev keeps that shape.
      proxy: {
        '/api': apiTarget,
        '/health': apiTarget,
      },
    },
  }
})
