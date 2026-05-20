import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  // Build-time deploy markers (mi-c0sv). CI injects GIT_SHA (7-char) and
  // BUILD_DATE (ISO 8601) as build args → env vars during `npm run build`;
  // they're not knowable at runtime in a static SPA. Local dev / CI without
  // the vars falls back to 'dev' and build-time `now` so `npm run dev`
  // never breaks. Mirrored in vitest.config.ts so component tests see the
  // same globals.
  define: {
    __GIT_SHA__: JSON.stringify(process.env.GIT_SHA ?? 'dev'),
    __BUILD_DATE__: JSON.stringify(process.env.BUILD_DATE ?? new Date().toISOString()),
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/docs': { target: 'http://localhost:8080', changeOrigin: true },
      '/healthz': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
});
