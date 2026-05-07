import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
// pgao serves the SPA from the same origin as the API (Go handler chains
// /api/v1/* and / on the same listener), so no proxy is needed in prod.
// During `npm run dev` we proxy /api + /metrics + /ready to a locally
// running pgao on :8080.
export default defineConfig({
    plugins: [react()],
    server: {
        port: 5173,
        proxy: {
            '/api': 'http://127.0.0.1:8080',
            '/metrics': 'http://127.0.0.1:8080',
            '/ready': 'http://127.0.0.1:8080',
            '/health': 'http://127.0.0.1:8080',
        },
    },
    build: {
        outDir: 'dist',
        emptyOutDir: true,
        sourcemap: false,
        target: 'es2022',
    },
});
