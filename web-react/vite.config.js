import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'node:path';
export default defineConfig({
    // Runtime panel_path is injected through <base href> in index.html. Relative
    // asset URLs let the same bundle work at /, /panel/, or a deeper prefix.
    base: './',
    plugins: [react()],
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
                manualChunks: function (id) {
                    if (!id.includes('node_modules'))
                        return;
                    if (/[\\/](react|react-dom|react-router-dom)[\\/]/.test(id))
                        return 'vendor-react';
                    if (/[\\/](@mui[\\/]material|@mui[\\/]icons-material|@emotion[\\/](react|styled))[\\/]/.test(id))
                        return 'vendor-mui';
                    if (/[\\/]echarts[\\/]/.test(id))
                        return 'vendor-echarts';
                    if (/[\\/](i18next|react-i18next)[\\/]/.test(id))
                        return 'vendor-i18n';
                },
            },
        },
    },
});
