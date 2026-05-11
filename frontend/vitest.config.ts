import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { svelteTesting } from '@testing-library/svelte/vite';

export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
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
      // Soft floor baselined 2026-05-11 from `npm run test:cover`:
      // lines 89.1, statements 89.1, functions 83.87, branches 77.4.
      // Floor = measured rounded down to nearest 5; refactors shouldn't
      // trip CI, but new untested branches will. Bump only when a
      // deliberate coverage push lifts the baseline (Q-2 R2).
      thresholds: {
        lines: 85,
        statements: 85,
        functions: 80,
        branches: 75,
        autoUpdate: false,
      },
    },
  },
});
