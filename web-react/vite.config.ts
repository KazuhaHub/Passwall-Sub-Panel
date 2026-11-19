import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

// Chunk assignment is by EXACT npm package name, taken from the last
// `node_modules/<pkg>/` segment of the module id. Regexes over the path were
// wrong twice: '/react/' also matches '@base-ui/react/', and the stale
// 'react-router-dom' never matched the real 'react-router' package.
//
// Why every package is listed rather than just the heads: the bundler puts an
// unassigned module in the chunk of the first assigned module that pulls it in.
// So a package left out can drag React core into, say, the i18n chunk, and a
// chunk that imports from that one then loads (and modulepreloads) it on every
// page. Anything a vendor chunk depends on and that is shared with another chunk
// is therefore assigned to vendor-react explicitly.
//
// vendor-base-ui and vendor-ui hold the Tailwind/shadcn stack. Nothing on the
// login path may import them (smoke:dist enforces it on index.html), because the
// un-migrated MUI pages must not pay for the new stack.
const CHUNK_BY_PACKAGE = new Map<string, string>([
  // React core and the small libraries every chunk shares.
  ...['react', 'react-dom', 'scheduler', 'react-router', 'cookie-es', '@remix-run/route-pattern',
    'use-sync-external-store', '@babel/runtime', 'clsx', 'tslib'].map(n => [n, 'vendor-react'] as const),
  ...['@mui/material', '@mui/icons-material', '@mui/system', '@mui/utils', '@mui/styled-engine',
    '@mui/private-theming', '@mui/types', '@emotion/react', '@emotion/styled', '@emotion/cache',
    '@emotion/serialize', '@emotion/sheet', '@emotion/utils', '@emotion/hash', '@emotion/unitless',
    '@emotion/use-insertion-effect-with-fallbacks', '@emotion/is-prop-valid',
    '@emotion/memoize', '@popperjs/core', 'react-transition-group', 'react-is', 'prop-types',
    'hoist-non-react-statics', 'stylis', 'csstype', 'dom-helpers', 'object-assign', '@emotion/weak-memoize'].map(n => [n, 'vendor-mui'] as const),
  ...['echarts', 'zrender'].map(n => [n, 'vendor-echarts'] as const),
  ...['i18next', 'react-i18next', 'html-parse-stringify', 'void-elements'].map(n => [n, 'vendor-i18n'] as const),
  ...['@base-ui/react', '@base-ui/utils', 'tabbable', 'reselect'].map(n => [n, 'vendor-base-ui'] as const),
  ...['lucide-react', 'cmdk', 'sonner', '@tanstack/react-table', '@tanstack/table-core', 'react-remove-scroll',
    'react-remove-scroll-bar', 'react-style-singleton', 'use-callback-ref', 'use-sidecar', 'aria-hidden', 'get-nonce',
    'detect-node-es'].map(n => [n, 'vendor-ui'] as const),
])

// Whole npm scopes that belong to one chunk, so a new transitive package inside
// them (radix ships ~15 tiny ones) cannot slip through the exact-name map.
const CHUNK_BY_SCOPE = new Map<string, string>([
  ['@radix-ui', 'vendor-ui'],
  ['@floating-ui', 'vendor-base-ui'],
])

function chunkFor(pkg: string): string | undefined {
  return CHUNK_BY_PACKAGE.get(pkg) ?? CHUNK_BY_SCOPE.get(pkg.split('/')[0])
}

function packageOf(id: string): string | undefined {
  const m = /.*node_modules[\\/](@[^\\/]+[\\/][^\\/]+|[^\\/]+)[\\/]/.exec(id)
  return m?.[1].replace('\\', '/')
}

export default defineConfig({
  // Runtime panel_path is injected through <base href> in index.html. Relative
  // asset URLs let the same bundle work at /, /panel/, or a deeper prefix.
  base: './',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  server: {
    port: 5174,
    host: true,
    proxy: {
      '/api': { target: 'http://localhost:8788', changeOrigin: true },
      '/sub': { target: 'http://localhost:8788', changeOrigin: true },
    },
    fs: {
      // Allow Vite to serve files from the parent repo (npm hoisting
      // can place some packages in the repo-root node_modules instead
      // of web-react/node_modules). Kept defensive even though the
      // specific @fontsource/noto-sans-sc case that motivated this
      // is gone.
      allow: ['..'],
    },
  },
  build: {
    // Build directly into the Go embed location so `go build` picks up
    // the latest assets without a copy step.
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // Split heavy vendor deps into stable named chunks instead of
        // bundling everything into one ~700KB main bundle. Each chunk
        // is cached independently — a small app-code change no longer
        // invalidates the React/MUI/echarts caches, and the chunks
        // download in parallel rather than blocking on the megabundle.
        // Keep the chunk count low (no granular per-package) — a long
        // chunk list also hurts because HTTP/2 multiplex still pays
        // per-request overhead.
        // rolldown's codeSplitting (Vite 8's bundler) replaces the legacy
        // manualChunks callback, whose semantics here pulled unassigned
        // dependencies into the first claiming chunk (see CHUNK_BY_PACKAGE).
        // includeDependenciesRecursively is off for the same reason: a group
        // holds exactly the packages the map names, nothing it merely imports.
        codeSplitting: {
          groups: [{
            name: (id: string) => {
              const pkg = packageOf(id)
              return (pkg && chunkFor(pkg)) || null
            },
            test: /node_modules/,
            includeDependenciesRecursively: false,
          }],
        },
      },
    },
  },
})
