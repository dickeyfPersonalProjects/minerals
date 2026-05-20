import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { svelteTesting } from '@testing-library/svelte/vite';

export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  // Mirror vite.config.ts's deploy-marker globals (mi-c0sv) so components
  // that read __GIT_SHA__ / __BUILD_DATE__ (the footer) compile under
  // vitest. vitest uses this config, not vite.config.ts, so the `define`
  // must be repeated here. Tests get the 'dev' / build-time `now` defaults.
  define: {
    __GIT_SHA__: JSON.stringify(process.env.GIT_SHA ?? 'dev'),
    __BUILD_DATE__: JSON.stringify(process.env.BUILD_DATE ?? new Date().toISOString()),
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
    // Vitest's default include glob matches `**/*.spec.ts` everywhere
    // in the project, which would otherwise sweep up the Playwright
    // specs under `e2e/` and try to run them in jsdom. Playwright
    // specs are invoked separately via `npm run e2e` (mi-dwx).
    exclude: ['**/node_modules/**', '**/dist/**', 'e2e/**'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'json-summary'],
      include: ['src/**/*.{ts,svelte}'],
      exclude: [
        'src/**/*.test.ts',
        'src/lib/api/schema.d.ts',
        'src/test-setup.ts',
        'src/main.ts',
        'src/routes.ts',
      ],
      // Soft floor re-baselined 2026-05-15 from `npm run test:cover` under
      // vitest 4 (v8 provider): lines 90.56, statements 88.12, functions 89.33,
      // branches 71.22. Branch metric shifted vs. vitest 3 baseline (77.4 →
      // 71.22) due to v8 provider's stricter branch instrumentation.
      // Floor = measured rounded down to nearest 5; refactors shouldn't
      // trip CI, but new untested branches will. Bump only when a
      // deliberate coverage push lifts the baseline (Q-2 R2).
      thresholds: {
        lines: 85,
        statements: 85,
        functions: 85,
        branches: 70,
        autoUpdate: false,
      },
    },
  },
});
