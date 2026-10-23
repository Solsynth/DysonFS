import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'

// The picker's client script is built on its own so a host app can load it
// from a plain <script> tag. It has no Vue dependency and no styles of its own
// beyond the modal chrome it injects, which keeps it small enough to inline in
// a page that only occasionally opens a picker.
export default defineConfig({
  // The SDK is a script, not a document: it must not drag the drive's icons
  // and favicons into its output directory.
  publicDir: false,
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    lib: {
      entry: fileURLToPath(new URL('./src/lib/picker/embed.ts', import.meta.url)),
      name: 'SnfsPicker',
      formats: ['es', 'iife'],
      fileName: (format) => (format === 'es' ? 'snfs-picker.mjs' : 'snfs-picker.js'),
    },
    outDir: 'dist/picker',
    emptyOutDir: true,
    sourcemap: true,
    // The wrapper must expose `openSnfsPicker` and `SnfsPicker` on `window`
    // for a classic script tag, and named exports for a bundler import.
    rollupOptions: {
      output: {
        exports: 'named',
        extend: true,
      },
    },
  },
})
