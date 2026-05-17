// Real-browser PKCE auth smoke (mi-dwx). Boots the SPA in a headless
// Chromium against a live docker-compose stack (app + Postgres +
// MinIO + Keycloak, seeded by terraform + dev-seed.sh) and walks the
// full login → token-exchange → logout path.
//
// This spec is the regression net for three bug classes the curl-
// based smoke (mi-ivk) cannot reach:
//
//   mi-rvn — Login button optimism: the navbar must render the Login
//            button on first paint when runtime-config exposes an
//            `oidc` block. Asserted by the first `expect(loginButton)`.
//
//   mi-0ag — path→hash routing: Keycloak posts back to a path URL
//            (`/auth/callback?...`). path-to-hash.ts rewrites it
//            BEFORE svelte-spa-router boots; if that rewrite breaks,
//            the AuthCallback route never mounts and the token
//            exchange never runs — `profileMenu` would never appear.
//
//   mi-cl1 — CSP connect-src for the OIDC issuer origin: the SPA's
//            token POST to Keycloak crosses an origin boundary
//            (http://localhost:8080 → http://localhost:8081). A CSP
//            with `connect-src 'self'` would silently block it — the
//            page error listener below surfaces such a violation, and
//            the missing token would leave the Login button visible
//            instead of the profile menu.
//
// The spec deliberately uses ONLY the real browser, real CSP, real
// router, real fetch, and real Keycloak. Anything mocked here would
// recreate the original gap.

import { test, expect } from '@playwright/test';

const TEST_USERNAME = process.env.E2E_USERNAME ?? 'user1@localhost';
const TEST_PASSWORD = process.env.E2E_PASSWORD ?? 'MineralsDev123!';

// Keycloak admin endpoint + credentials used by the fresh-user
// sub-scenario below (mi-cg3). Defaults match dev-seed.sh — the
// realm/admin pair the CI keycloak-smoke job spins up. Override
// via env when running against a non-dev stack.
const KC_BASE_URL = process.env.E2E_KC_URL ?? 'http://localhost:8081';
const KC_ADMIN_USER = process.env.E2E_KC_ADMIN_USER ?? 'admin';
const KC_ADMIN_PASS = process.env.E2E_KC_ADMIN_PASS ?? 'admin';
const KC_REALM = process.env.E2E_KC_REALM ?? 'minerals';

// kcAdminToken mints an admin-cli token from the master realm — the
// same path dev-seed.sh uses to provision the seeded users. Throws
// loudly so a misconfigured stack fails the spec rather than silently
// skipping the assertion.
async function kcAdminToken(): Promise<string> {
  const res = await fetch(`${KC_BASE_URL}/realms/master/protocol/openid-connect/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'password',
      client_id: 'admin-cli',
      username: KC_ADMIN_USER,
      password: KC_ADMIN_PASS,
    }),
  });
  if (!res.ok) {
    throw new Error(`Keycloak admin token request failed: ${res.status} ${await res.text()}`);
  }
  const body = (await res.json()) as { access_token?: string };
  if (!body.access_token) {
    throw new Error('Keycloak admin token response missing access_token');
  }
  return body.access_token;
}

// createFreshKcUser provisions a one-shot Keycloak user under the
// minerals realm with a unique email so the SPA + backend have
// never seen them before. Mirrors the field set dev-seed.sh's
// create_user() uses: firstName/lastName are mandatory for the
// realm's "fully set up" check, and emailVerified avoids a
// verify-email required-action that would block the login form.
async function createFreshKcUser(token: string, email: string, password: string): Promise<string> {
  const res = await fetch(`${KC_BASE_URL}/admin/realms/${KC_REALM}/users`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      username: email,
      email,
      firstName: 'Fresh',
      lastName: 'Polecat',
      enabled: true,
      emailVerified: true,
      credentials: [{ type: 'password', value: password, temporary: false }],
    }),
  });
  if (res.status !== 201) {
    throw new Error(`Keycloak create user failed: ${res.status} ${await res.text()}`);
  }
  // Keycloak returns the new user's id in the Location header
  // (`.../admin/realms/<realm>/users/<id>`), not the response body.
  const location = res.headers.get('location');
  if (!location) {
    throw new Error('Keycloak create user response missing Location header');
  }
  const id = location.split('/').pop();
  if (!id) {
    throw new Error(`Keycloak create user Location header malformed: ${location}`);
  }
  return id;
}

async function deleteKcUser(token: string, userId: string): Promise<void> {
  const res = await fetch(`${KC_BASE_URL}/admin/realms/${KC_REALM}/users/${userId}`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${token}` },
  });
  // 204 deleted; 404 already gone (e.g. retry path) — both fine.
  if (res.status !== 204 && res.status !== 404) {
    throw new Error(`Keycloak delete user failed: ${res.status} ${await res.text()}`);
  }
}

test('login round-trip + logout via real Keycloak', async ({ page, baseURL }) => {
  // Surface browser-side errors in the Playwright trace so a future
  // CSP violation, fetch failure, or thrown exception shows up in
  // the failure artifact without needing a local repro.
  const pageErrors: string[] = [];
  page.on('pageerror', (err) => {
    pageErrors.push(err.message);
  });
  page.on('console', (msg) => {
    if (msg.type() === 'error' || msg.type() === 'warning') {
      // Keep console errors in the trace; do NOT fail the spec on
      // them (third-party libs sometimes warn benignly).
      console.log(`[browser ${msg.type()}] ${msg.text()}`);
    }
  });

  // 1. SPA renders. The Login button only appears when the anonymous
  //    GET to /api/v1/runtime-config returns an `oidc` block — the
  //    mi-rvn guarantee.
  await page.goto('/');
  const loginButton = page.getByTestId('login-button');
  await expect(loginButton, 'Login button must render for anonymous users').toBeVisible();

  // 2. Click Login → SPA assigns location to Keycloak's `/auth`
  //    endpoint with PKCE params. Wait for the redirect to land on
  //    the Keycloak origin before interacting with the form.
  await loginButton.click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });

  // 3. Fill the Keycloak login form (standard theme: #username,
  //    #password, #kc-login). Submitting bounces back to
  //    http://localhost:8080/auth/callback?code=...&state=... .
  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();

  // 4. The full chain that has to succeed here:
  //      Keycloak 302 → /auth/callback?code=...
  //      path-to-hash.ts rewrites to /#/auth/callback?...
  //      AuthCallback.svelte POSTs code+verifier to the token endpoint
  //      Token stored, router replaces to '/' (or stored returnTo)
  //      Layout.svelte renders ProfileMenu instead of LoginButton
  //
  //    If any link in that chain breaks (mi-0ag, mi-cl1, etc.) the
  //    profile menu never appears.
  const profileMenu = page.getByTestId('profile-menu-button');
  await expect(profileMenu, 'profile menu must appear after token exchange').toBeVisible({
    timeout: 30_000,
  });
  await expect(loginButton, 'Login button must be gone after sign-in').toHaveCount(0);
  expect(pageErrors, `unexpected page errors during sign-in: ${pageErrors.join(' | ')}`).toEqual(
    [],
  );

  // 5. Sign out: open the profile dropdown, click Sign out. beginLogout
  //    clears local auth state and assigns to Keycloak's end-session
  //    endpoint. Because the SPA does NOT retain an id_token, Keycloak
  //    >= 18 surfaces a logout confirmation form (#kc-logout) — accept
  //    that path AND the unattended-redirect path (some Keycloak
  //    configurations skip the confirmation when the client + redirect
  //    pair is recognised).
  await profileMenu.click();
  const signOut = page.getByTestId('profile-menu-signout');
  await expect(signOut).toBeVisible();
  await signOut.click();

  // Wait until the page either lands on the Keycloak logout
  // confirmation OR has already returned to the SPA.
  const confirmLogout = page.locator('#kc-logout');
  await Promise.race([
    confirmLogout.waitFor({ state: 'visible', timeout: 10_000 }).catch(() => undefined),
    page
      .waitForURL((url) => url.origin === (baseURL ?? 'http://localhost:8080'), {
        timeout: 10_000,
      })
      .catch(() => undefined),
  ]);
  if (await confirmLogout.isVisible().catch(() => false)) {
    await confirmLogout.click();
  }

  // 6. Back on the SPA, anonymous again: Login button returns,
  //    profile menu is gone.
  await page.waitForURL((url) => url.origin === (baseURL ?? 'http://localhost:8080'), {
    timeout: 15_000,
  });
  await expect(
    page.getByTestId('login-button'),
    'Login button must return after sign-out',
  ).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('profile-menu-button')).toHaveCount(0);
});

// Fresh-user → profile-setup redirect (mi-cg3, follow-up to mi-4p4).
//
// Provision a brand-new Keycloak user that neither the SPA nor the
// app's user table has ever seen. After the SPA returns from the
// Keycloak callback, the first protected fetch (the Specimens list
// the home route fires on mount) MUST 403 with
// `details.redirect: /profile/setup`, and the API wrapper middleware
// MUST swap the route to `/profile/setup` BEFORE any error UI
// renders — no toast, no "Couldn't load specimens" banner, ever. The
// pre-mi-4p4 bug was a flash of the 403 error UI on Specimens before
// the redirect landed; this spec is the real-browser regression net
// for that bug class, complementing the Vitest unit test in
// frontend/src/lib/api/wrapper.test.ts.
test('fresh user lands directly on /profile/setup with no error UI', async ({ page }) => {
  const pageErrors: string[] = [];
  page.on('pageerror', (err) => {
    pageErrors.push(err.message);
  });
  page.on('console', (msg) => {
    if (msg.type() === 'error' || msg.type() === 'warning') {
      console.log(`[browser ${msg.type()}] ${msg.text()}`);
    }
  });

  // Mint the fresh user lazily per-test so parallel runs (even
  // though playwright.config.ts pins workers=1 today) and reruns
  // never collide on the same email. The Date.now() + random suffix
  // is enough entropy for this; the user is deleted in finally.
  const adminToken = await kcAdminToken();
  const localPart = `pending-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const email = `${localPart}@localhost`;
  const userId = await createFreshKcUser(adminToken, email, TEST_PASSWORD);

  try {
    // 1. SPA renders anonymous. Same mi-rvn precondition as the
    //    round-trip test.
    await page.goto('/');
    const loginButton = page.getByTestId('login-button');
    await expect(loginButton).toBeVisible();

    // 2. Click Login → Keycloak form → submit credentials for the
    //    fresh user. After this click the browser is on the
    //    Keycloak origin filling the standard theme.
    await loginButton.click();
    await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
      timeout: 30_000,
    });
    await page.locator('#username').fill(email);
    await page.locator('#password').fill(TEST_PASSWORD);
    await page.locator('#kc-login').click();

    // 3. Race: the SPA's first protected fetch returns 403 +
    //    `details.redirect: /profile/setup`. The wrapper middleware
    //    MUST navigate to /profile/setup BEFORE either the global
    //    error toast OR the Specimens in-page error banner mounts.
    //    If either error UI element wins the race, that is the
    //    mi-4p4 regression — fail with a clear cause rather than
    //    waiting for the navigation timeout.
    const redirectLanded = page
      .waitForURL(/#\/profile\/setup$/, { timeout: 30_000 })
      .then(() => 'redirect' as const);
    const toastAppeared = page
      .getByTestId('toast')
      .first()
      .waitFor({ state: 'visible', timeout: 30_000 })
      .then(() => 'toast' as const)
      .catch(() => 'toast-never' as const);
    const specimensError = page
      .getByTestId('error')
      .first()
      .waitFor({ state: 'visible', timeout: 30_000 })
      .then(() => 'specimens-error' as const)
      .catch(() => 'specimens-error-never' as const);

    const winner = await Promise.race([redirectLanded, toastAppeared, specimensError]);
    expect(
      winner,
      'redirect to /profile/setup must win over any error UI (mi-4p4 regression)',
    ).toBe('redirect');

    // 4. Once we are on /profile/setup, the form is visible. This is
    //    the positive-side proof that the route actually mounted —
    //    URL alone could lie if the route guard 404'd silently.
    await expect(page.getByTestId('profile-setup')).toBeVisible();

    // 5. Belt-and-braces: even though `winner === 'redirect'` proves
    //    no error UI was visible at the moment we landed, double-
    //    check that neither testid is in the DOM now. Catches a
    //    pathological case where an error mounts AFTER the route
    //    change (e.g. a deferred fetch firing post-navigation).
    await expect(page.getByTestId('toast')).toHaveCount(0);
    await expect(page.getByTestId('error')).toHaveCount(0);

    expect(
      pageErrors,
      `unexpected page errors during fresh-user redirect: ${pageErrors.join(' | ')}`,
    ).toEqual([]);
  } finally {
    // Always clean up the Keycloak user so reruns against the same
    // stack stay isolated. The app's users-table row keyed on this
    // user's sub becomes an orphan; it is harmless for the test (no
    // FK constraints prevent reuse) and the dev compose volume gets
    // wiped between CI jobs anyway.
    await deleteKcUser(adminToken, userId);
  }
});
