import { defineConfig } from 'vitest/config'
import { fileURLToPath } from 'node:url'

// Minimal unit-test config. Node environment (no DOM needed for the pure i18n
// logic we cover); the `@` alias mirrors vite.config.ts / tsconfig so tests can
// import via '@/...' if needed.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
})
