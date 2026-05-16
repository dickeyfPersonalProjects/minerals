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
