import { fileURLToPath } from 'node:url'
import { configDefaults, defineConfig, mergeConfig } from 'vitest/config'
import type { ConfigEnv, UserConfig } from 'vite'
import viteConfig from './vite.config.ts'

function baseConfig(env: ConfigEnv): UserConfig {
  if (typeof viteConfig === 'function') {
    return (viteConfig as unknown as (environment: ConfigEnv) => UserConfig)(env)
  }
  return viteConfig as UserConfig
}

export default defineConfig((env) =>
  mergeConfig(baseConfig(env), {
    test: {
      environment: 'jsdom',
      exclude: [...configDefaults.exclude, 'e2e/**'],
      root: fileURLToPath(new URL('./', import.meta.url)),
    },
  }),
)
