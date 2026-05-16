// Playwright config for the real-browser PKCE auth smoke (mi-dwx).
// Lives next to vite/vitest configs because the e2e tests live under
// frontend/e2e/ and consume the same node_modules — but it is invoked
// only via `npm run e2e`, never by `vitest`.
//
// Scope: one browser (Chromium), one worker. The bead's "auth round-
// trip" coverage is single-user; parallel runs would race on the same
// Keycloak session.

import { defineConfig, devices } from '@playwright/test';

const BASE_URL = process.env.E2E_BASE_URL ?? 'http://localhost:8080';

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  // One retry in CI as the bead allows; never silently more. Local
  // runs surface flakes immediately.
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI
    ? [['github'], ['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]]
    : [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  outputDir: 'test-results',
  // Per-test cap matches the bead's ≤90 s budget for the spec.
  timeout: 90_000,
  expect: {
    timeout: 15_000,
  },
  use: {
    baseURL: BASE_URL,
    // Keep traces + screenshots + video only on failure to keep the
    // green-path artifact size small. The trace alone is enough to
    // triage without re-running locally.
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
