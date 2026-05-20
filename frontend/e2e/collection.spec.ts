// "Browse all specimens" + "Browse my collection" end-to-end (mi-xue7).
//
// Drives the full stack to prove the two scoped list views differ by
// owner-scope and visibility:
//
//   * /collection (scope=mine) shows EVERY specimen the owner created —
//     public AND private — when viewed as the authenticated owner.
//   * /specimens (browse all), viewed anonymously after logout, shows
//     the owner's PUBLIC specimen but never the private one (the
//     private row is excluded at the SQL layer — CONTRACT.md §13).
//
// Seeding is done via the CI password-grant client (out-of-band bearer
// token) so the per-test budget isn't spent clicking through the
// create form; the owner-facing assertions then run through the real
// BFF cookie session, and the anonymous assertions through a
// cookie-cleared context. This mirrors auth-bff.spec.ts /
// visibility.spec.ts — only the real browser, router, CSP, fetch, and
// Keycloak are exercised.

import { expect, test, request as playwrightRequest } from '@playwright/test';

const KEYCLOAK_BASE_URL = process.env.E2E_KEYCLOAK_BASE_URL ?? 'http://localhost:8081';
const KEYCLOAK_REALM = process.env.E2E_KEYCLOAK_REALM ?? 'minerals';
const KEYCLOAK_TEST_CLIENT = process.env.E2E_KEYCLOAK_TEST_CLIENT ?? 'minerals-test';
const TEST_USERNAME = process.env.E2E_USERNAME ?? 'user1@localhost';
const TEST_PASSWORD = process.env.E2E_PASSWORD ?? 'MineralsDev123!';

// Skip the whole file when the dev stack isn't wired for BFF auth —
// the same guard auth-bff.spec.ts uses, so a polecat running against
// the pre-BFF stack gets a clear signal instead of an opaque failure.
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

async function mintAccessToken(): Promise<string> {
  const kc = await playwrightRequest.newContext({ baseURL: KEYCLOAK_BASE_URL });
  try {
    const res = await kc.post(`/realms/${KEYCLOAK_REALM}/protocol/openid-connect/token`, {
      form: {
        grant_type: 'password',
        client_id: KEYCLOAK_TEST_CLIENT,
        username: TEST_USERNAME,
        password: TEST_PASSWORD,
        scope: 'openid',
      },
    });
    if (!res.ok()) {
      throw new Error(
        `keycloak password grant failed: ${res.status()} ${res.statusText()} — ${await res.text()}`,
      );
    }
    const body = (await res.json()) as { access_token?: string };
    if (!body.access_token) {
      throw new Error(`keycloak returned no access_token: ${JSON.stringify(body)}`);
    }
    return body.access_token;
  } finally {
    await kc.dispose();
  }
}

test('scoped list views — owner sees public+private on /collection; anonymous browse hides the private one', async ({
  page,
  baseURL,
}) => {
  // Step 1: seed one public + one private specimen as the test user.
  const token = await mintAccessToken();
  const api = await playwrightRequest.newContext({
    baseURL: baseURL ?? 'http://localhost:8080',
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });

  const stamp = Date.now();
  const publicName = `Collection E2E Public ${stamp}`;
  const privateName = `Collection E2E Private ${stamp}`;

  try {
    // First-login profile gate — idempotent across runs, failures
    // (already set up) are harmless here.
    await api.post('/api/v1/profile', { data: { display_name: 'Collection E2E' } });

    for (const [name, visibility] of [
      [publicName, 'public'],
      [privateName, 'private'],
    ] as const) {
      const res = await api.post('/api/v1/specimens', {
        data: { type: 'mineral', name, visibility },
      });
      if (!res.ok()) {
        throw new Error(`seed ${visibility} specimen: ${res.status()} — ${await res.text()}`);
      }
    }
  } finally {
    await api.dispose();
  }

  // Step 2: log in via the BFF flow so the browser holds a session
  // cookie (the seeding bearer token never reaches the browser).
  await page.goto('/');
  await page.getByTestId('login-button').click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });
  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();
  await expect(page.getByTestId('profile-menu-button')).toBeVisible({ timeout: 30_000 });

  // Step 3: the "My collection" nav link is present for the
  // authenticated user; following it lands on the owner-scoped view.
  const myCollection = page.getByTestId('nav-my-collection');
  await expect(myCollection, '"My collection" nav must show for authenticated users').toBeVisible();
  await myCollection.click();

  await expect(page.getByTestId('list-title')).toHaveText('My collection');
  await expect(page.getByTestId('specimen-grid')).toBeVisible({ timeout: 15_000 });
  // Both the public AND the private specimen belong to the owner, so
  // both must appear in the owner-scoped collection.
  await expect(
    page.getByText(publicName, { exact: true }),
    'owner collection must include the public specimen',
  ).toBeVisible();
  await expect(
    page.getByText(privateName, { exact: true }),
    'owner collection must include the private specimen (all visibilities)',
  ).toBeVisible();

  // Step 4: drop the session and view "Browse all" anonymously. The
  // private row must be filtered out at the SQL layer; the public one
  // remains discoverable.
  await page.context().clearCookies();
  await page.goto('/#/specimens');
  // Confirm we booted anonymous (the login button is back).
  await expect(page.getByTestId('login-button')).toBeVisible({ timeout: 15_000 });

  await expect(page.getByTestId('list-title')).toHaveText('Browse all specimens');
  await expect(
    page.getByText(publicName, { exact: true }),
    'anonymous browse-all must still surface the public specimen',
  ).toBeVisible({ timeout: 15_000 });
  await expect(
    page.getByText(privateName, { exact: true }),
    'anonymous browse-all must NEVER surface a private specimen',
  ).toHaveCount(0);
});
