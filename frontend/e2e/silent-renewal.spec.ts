// Silent renewal acceptance — refresh stays logged in (mi-wmyc).
//
// Sibling to auth.spec.ts (the PKCE login round-trip from mi-dwx).
// This spec covers the bug the bead was filed against: after a
// successful login, hard-refreshing the tab dropped the in-memory
// token and bounced the user back to the anonymous Login button.
//
// Strategy 2 (top-level navigation with `prompt=none`): on app boot,
// if the browser has held a session before (had_session marker), the
// SPA navigates the whole tab to {issuer}/auth?prompt=none and lets
// Keycloak's SSO cookie mint a new token via the existing PKCE
// callback flow. No iframe, no CSP frame-src widening.
//
// We deliberately do NOT mock anything: real Keycloak, real CSP,
// real top-level redirect, real SSO cookie. The whole point is to
// catch every failure mode of the silent-renewal path.

import { test, expect } from '@playwright/test';

const TEST_USERNAME = process.env.E2E_USERNAME ?? 'user1@localhost';
const TEST_PASSWORD = process.env.E2E_PASSWORD ?? 'MineralsDev123!';

test('hard refresh after login keeps the user logged in (silent renewal)', async ({ page }) => {
  const pageErrors: string[] = [];
  page.on('pageerror', (err) => {
    pageErrors.push(err.message);
  });
  page.on('console', (msg) => {
    if (msg.type() === 'error' || msg.type() === 'warning') {
      console.log(`[browser ${msg.type()}] ${msg.text()}`);
    }
  });

  // 1. Get the user logged in via the normal PKCE round-trip.
  await page.goto('/');
  const loginButton = page.getByTestId('login-button');
  await expect(loginButton).toBeVisible();
  await loginButton.click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });
  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();

  const profileMenu = page.getByTestId('profile-menu-button');
  await expect(profileMenu, 'profile menu must appear after first login').toBeVisible({
    timeout: 30_000,
  });

  // Sanity: the had_session marker must be set after a successful
  // interactive exchange — that's the gate that turns silent renewal
  // on for this browser. If this is missing, the rest of the test is
  // diagnosing the wrong thing.
  const marker = await page.evaluate(() =>
    window.localStorage.getItem('minerals.oidc.had_session'),
  );
  expect(marker, 'had_session marker must be set after interactive login').toBe('1');

  // 2. Hard-refresh the page. Without silent renewal, the in-memory
  //    token would be gone and the navbar would flip back to the
  //    Login button. With silent renewal, the SPA navigates the
  //    whole tab to Keycloak's `prompt=none` endpoint, Keycloak
  //    honours its own SSO cookie, and the token is re-minted via
  //    the existing PKCE callback flow before the user notices.
  await page.reload({ waitUntil: 'load' });

  // 3. Assert the user is STILL logged in WITHOUT clicking anything.
  //    Allow generous time for the silent flow (full top-level
  //    round-trip through Keycloak plus token POST) but no user
  //    interaction is permitted.
  await expect(profileMenu, 'profile menu must remain visible after refresh').toBeVisible({
    timeout: 30_000,
  });
  await expect(loginButton, 'Login button must NOT reappear after refresh').toHaveCount(0);

  // 4. Strategy 2 deliberately does NOT use iframes. CSP frame-src
  //    stays restricted to 'self' (mi-cl1). Assert no auth-issuer
  //    iframe was mounted on this page — catches a regression to the
  //    abandoned iframe-based approach.
  const issuerFrames = page.frames().filter((f) => f.url().includes('/realms/'));
  expect(
    issuerFrames,
    'no Keycloak iframe should be mounted (Strategy 2 uses top-level nav)',
  ).toHaveLength(0);

  // 5. No CSP violations or thrown exceptions during the silent
  //    flow. A "Refused to frame" error here would mean someone
  //    quietly re-introduced an iframe AND the CSP correctly blocked
  //    it; either way it's a regression worth catching.
  const cspBlocked = pageErrors.find((m) => /frame-src|Refused to frame/i.test(m));
  expect(cspBlocked, `CSP blocked an unexpected frame: ${cspBlocked}`).toBeUndefined();
  expect(pageErrors, `unexpected page errors during refresh: ${pageErrors.join(' | ')}`).toEqual(
    [],
  );
});
