// 'New specimens' default visibility, end-to-end (mi-q2d8). Drives the
// full SPA through the BFF cookie login, sets the account-level default
// whole-specimen visibility to 'public' on the Settings page, then
// creates a specimen WITHOUT touching the visibility control and
// asserts the created specimen is public — proving the create form
// pre-filled the default and that the default flowed through to POST.
//
// The acceptance contract (from the bead):
//   * Settings exposes a 'New specimens → Default visibility' control,
//   * the create form pre-fills visibility from the user's default,
//   * a specimen created without touching the control lands public.
//
// Login is exercised through the real BFF round-trip (same pattern as
// auth-bff.spec.ts); everything else is plain SPA clicks within the
// per-test budget — no out-of-band API seeding needed because the
// whole point is the UI default flowing into the create POST.

import { expect, test, request as playwrightRequest } from '@playwright/test';

const TEST_USERNAME = process.env.E2E_USERNAME ?? 'user1@localhost';
const TEST_PASSWORD = process.env.E2E_PASSWORD ?? 'MineralsDev123!';

// Probe for BFF auth once; skip with a clear message if the dev stack
// is not configured for it (mirrors auth-bff.spec.ts).
test.beforeAll(async ({ baseURL }) => {
  const probe = await playwrightRequest.newContext({
    baseURL: baseURL ?? 'http://localhost:8080',
  });
  try {
    const res = await probe.get('/auth/login', { maxRedirects: 0 });
    if (res.status() !== 302) {
      test.skip(
        true,
        `V2 BFF auth not configured on the dev stack (GET /auth/login returned ${res.status()}). ` +
          `Bring the stack up with: bash terraform/keycloak/dev-seed.sh && ` +
          `docker compose --env-file .env.bff --profile keycloak up -d --force-recreate app`,
      );
    }
  } finally {
    await probe.dispose();
  }
});

async function login(page: import('@playwright/test').Page): Promise<void> {
  await page.goto('/');
  await page.getByTestId('login-button').click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });
  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();
  await expect(page.getByTestId('profile-menu-button')).toBeVisible({ timeout: 30_000 });
}

test('Settings default flows into the create form and the created specimen is public', async ({
  page,
}) => {
  await login(page);

  // 1. Set the 'New specimens' default visibility to 'public'.
  await page.goto('/#/settings');
  const defaultVis = page.getByTestId('settings-default-specimen-visibility');
  await expect(defaultVis).toBeVisible({ timeout: 15_000 });
  await defaultVis.selectOption('public');
  const save = page.getByTestId('settings-field-defaults-save');
  await expect(save).toBeEnabled();
  await save.click();
  // The dropdown re-baselines against the saved server state, so Save
  // disables again once the PATCH resolves.
  await expect(save).toBeDisabled({ timeout: 15_000 });

  // 2. Open the create form; the visibility control must pre-fill to
  //    the saved default without any interaction.
  await page.goto('/#/specimens/new');
  const visibility = page.locator('#specimen-visibility');
  await expect(visibility).toBeVisible({ timeout: 15_000 });
  await expect(visibility).toHaveValue('public');

  // 3. Fill the minimum required fields and submit WITHOUT touching
  //    visibility. A unique catalog number keeps reruns from colliding.
  const catalog = `MI-Q2D8-${Date.now()}`;
  await page.getByLabel(/^name/i).fill('Default-visibility galena');
  await page.getByLabel(/catalog number/i).fill(catalog);
  await page.getByTestId('submit-button').click();

  // 4. Land on the new specimen's detail page; it must be public.
  await page.waitForURL(/#\/specimens\/[0-9a-f-]+/i, { timeout: 30_000 });
  await expect(page.getByTestId('specimen-detail')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('visibility-chip')).toHaveText(/public/i);
});
