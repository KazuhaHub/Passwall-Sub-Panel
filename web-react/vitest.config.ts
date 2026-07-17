import { defineConfig } from 'vitest/config'
import { fileURLToPath } from 'node:url'

// Minimal unit-test config. Node environment (no DOM needed for the pure i18n
// logic we cover); the `@` alias mirrors vite.config.ts / tsconfig so tests can
// import via '@/...' if needed.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/*.test.{ts,tsx}', 'src/vite-env.d.ts'],
      reporter: ['text', 'json-summary', 'html'],
      thresholds: {
        statements: 7,
        branches: 6,
        functions: 7,
        lines: 8,
      },
    },
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
})
