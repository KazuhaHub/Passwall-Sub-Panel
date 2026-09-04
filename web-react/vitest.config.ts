import { defineConfig } from 'vitest/config'
import { fileURLToPath } from 'node:url'

// Minimal unit-test config. Node environment (no DOM needed for the pure i18n
// logic we cover); the `@` alias mirrors vite.config.ts / tsconfig so tests can
// import via '@/...' if needed.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.{ts,tsx}'],
    server: {
      deps: {
        // material-color-utilities ships extensionless relative imports
        // ('./dynamiccolor/dynamic_scheme') and declares no subpath exports.
        // Vite rewrites those when it bundles; Node's ESM resolver, which
        // vitest uses for externalised deps, does not — so importing the app
        // theme from a test fails to resolve. Inlining hands the package to
        // Vite's resolver, which is what the app itself runs through.
        inline: ['@material/material-color-utilities'],
      },
    },
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
