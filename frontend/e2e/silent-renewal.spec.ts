// Silent renewal acceptance — refresh stays logged in (mi-ct2).
//
// Sibling to auth.spec.ts (the PKCE login round-trip from mi-dwx).
// This spec covers the bug the bead was filed against: after a
// successful login, hard-refreshing the tab dropped the in-memory
// token and bounced the user back to the anonymous Login button.
//
// What's asserted end-to-end:
//   - the SPA mounts a hidden iframe to {issuer}/auth?prompt=none on
//     page load, so the freshly reloaded tab does NOT show the Login
//     button after Keycloak's SSO cookie re-mints a token;
//   - CSP `frame-src` actually allows that iframe (mi-ct2 widening);
//     a wrongly-locked-down policy would surface as a browser-level
//     "Refused to frame" pageerror and a stuck Login button.
//
// We deliberately do NOT mock anything: real Keycloak, real CSP,
// real iframe, real SSO cookie. The whole point is to catch every
// failure mode of the silent-renewal path.

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

  // 2. Hard-refresh the page. Without silent renewal, the in-memory
  //    token would be gone and the navbar would flip back to the
  //    Login button. With silent renewal, the SPA mounts a hidden
  //    `prompt=none` iframe on boot, Keycloak honours its own SSO
  //    cookie, and the token is re-minted before the user notices.
  await page.reload({ waitUntil: 'load' });

  // 3. Assert the user is STILL logged in WITHOUT clicking anything.
  //    Allow generous time for the silent flow (iframe round-trip
  //    plus token POST) but no user interaction is permitted.
  await expect(profileMenu, 'profile menu must remain visible after refresh').toBeVisible({
    timeout: 30_000,
  });
  await expect(loginButton, 'Login button must NOT reappear after refresh').toHaveCount(0);

  // 4. No CSP violations or thrown exceptions during the silent flow.
  //    A locked-down frame-src would surface here as a "Refused to
  //    frame ... because it violates the following Content Security
  //    Policy directive: frame-src" error.
  const cspBlocked = pageErrors.find((m) => /frame-src|Refused to frame/i.test(m));
  expect(cspBlocked, `CSP blocked the silent-renewal iframe: ${cspBlocked}`).toBeUndefined();
  expect(pageErrors, `unexpected page errors during refresh: ${pageErrors.join(' | ')}`).toEqual(
    [],
  );
});
