// V2 BFF auth Playwright smoke (mi-1d5i / mi-sap2).
//
// Replaces auth.spec.ts (mi-dwx PKCE) and silent-renewal.spec.ts
// (mi-wmyc). The whole bug class those specs guarded against (in-SPA
// PKCE plumbing, hidden-iframe silent renewal, CSP holes for the
// cross-origin token POST) is structurally impossible under the BFF
// design — the SPA no longer speaks OAuth. The replacement covers:
//
//   * Login → Keycloak → cookie session round-trip via the backend.
//   * Hard refresh after login keeps the user logged in (the cookie
//     is HttpOnly + persistent; refresh just re-attaches the session).
//   * <img src=...> for a private-owned photo renders WITHOUT the
//     mi-lrqt Blob URL workaround (the cookie travels on image
//     requests automatically; that workaround was deleted in mi-3vc4).
//   * Cross-origin POST carrying the cookie but no CSRF header → 403
//     csrf_missing (stored-synchronizer CSRF defense).
//   * Logout invalidates the server-side session; a stale cookie
//     replay is rejected.
//
// The spec deliberately uses ONLY the real browser, real CSP, real
// router, real fetch, and real Keycloak — anything mocked here would
// recreate the original gap. Skipped automatically when the dev
// stack is not configured for BFF (no /auth/login route) so a polecat
// running the suite against the pre-BFF stack still gets a clear
// signal instead of a hard failure.

import { expect, test, request as playwrightRequest } from '@playwright/test';

const KEYCLOAK_BASE_URL = process.env.E2E_KEYCLOAK_BASE_URL ?? 'http://localhost:8081';
const KEYCLOAK_REALM = process.env.E2E_KEYCLOAK_REALM ?? 'minerals';
// The CI-only password-grant client (terraform/keycloak/clients.tf).
// Used by the private-image sub-scenario to seed a specimen + photo
// out-of-band — the BFF login flow is what the rest of the spec
// exercises end-to-end, but seeding via 30 SPA clicks per scenario
// would blow past the per-test budget.
const KEYCLOAK_TEST_CLIENT = process.env.E2E_KEYCLOAK_TEST_CLIENT ?? 'minerals-test';
const TEST_USERNAME = process.env.E2E_USERNAME ?? 'user1@localhost';
const TEST_PASSWORD = process.env.E2E_PASSWORD ?? 'MineralsDev123!';

// Smallest valid PNG the imageproc.Generate pipeline can decode
// end-to-end (1x1, single transparent pixel). Inlining the bytes
// avoids depending on a fixture file that might drift from the
// decoder's allowlist (shared with visibility.spec.ts).
const TINY_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=',
  'base64',
);

// Probe the backend once at suite start: if /auth/login is not
// registered (BFF env vars missing), skip every test in this file
// with a clear message. Running this spec against the pre-BFF stack
// would just look like an opaque "Login button never appears" failure.
test.beforeAll(async ({ baseURL }) => {
  const probe = await playwrightRequest.newContext({
    baseURL: baseURL ?? 'http://localhost:8080',
  });
  try {
    const res = await probe.get('/auth/login', { maxRedirects: 0 });
    // BFF wired → 302 to Keycloak; unwired → 404.
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

// Drive the SPA through the BFF cookie round-trip: anonymous boot,
// click Login (plain anchor to /auth/login), authenticate at Keycloak,
// land on '/', profile menu visible, /api/v1/me-equivalent returns
// the cookie-derived user.
test('login round-trip via real Keycloak → cookie session', async ({ page, baseURL }) => {
  const pageErrors: string[] = [];
  page.on('pageerror', (err) => {
    pageErrors.push(err.message);
  });
  page.on('console', (msg) => {
    if (msg.type() === 'error' || msg.type() === 'warning') {
      console.log(`[browser ${msg.type()}] ${msg.text()}`);
    }
  });

  await page.goto('/');
  const loginButton = page.getByTestId('login-button');
  await expect(loginButton, 'Login button must render for anonymous users').toBeVisible();
  // Under BFF the Login button is a plain <a href="/auth/login?...">.
  // No JS-driven OAuth dance — assert the href shape.
  await expect(loginButton).toHaveAttribute('href', /^\/auth\/login(\?.*)?$/);

  await loginButton.click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });

  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();

  // Backend /auth/callback handles the code exchange, sets the
  // HttpOnly cookie, and 302s back to '/'. The SPA boots, probes
  // /api/v1/profile, and renders the profile menu.
  const profileMenu = page.getByTestId('profile-menu-button');
  await expect(profileMenu, 'profile menu must appear after BFF callback').toBeVisible({
    timeout: 30_000,
  });
  await expect(loginButton, 'Login button must be gone after sign-in').toHaveCount(0);

  // The session cookie is HttpOnly so document.cookie cannot read it,
  // but the browser cookie jar has it. Asserting via the page context
  // proves the cookie was actually set on the right origin.
  const origin = new URL(baseURL ?? 'http://localhost:8080').origin;
  const cookies = await page.context().cookies(origin);
  const session = cookies.find((c) => c.name === 'minerals_session');
  expect(session, 'session cookie must be set on the SPA origin').toBeDefined();
  expect(session?.httpOnly, 'session cookie must be HttpOnly').toBe(true);

  expect(pageErrors, `unexpected page errors during sign-in: ${pageErrors.join(' | ')}`).toEqual(
    [],
  );
});

// Hard-refresh-stays-logged-in: the bug class mi-ct2 / mi-rb6k /
// mi-wmyc tried to solve under PKCE (in-memory token dropped on
// reload). Under BFF the cookie persists by construction — this is
// the regression net that the migration kept the win.
test('hard refresh after login keeps the user logged in', async ({ page }) => {
  await page.goto('/');
  await page.getByTestId('login-button').click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });
  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();
  await expect(page.getByTestId('profile-menu-button')).toBeVisible({ timeout: 30_000 });

  // Hard reload — discards every in-memory store. Under PKCE this
  // bounced the user back to the Login button; under BFF the cookie
  // is still in the browser jar and /api/v1/profile resolves it on
  // the next tick.
  await page.reload({ waitUntil: 'networkidle' });
  await expect(
    page.getByTestId('profile-menu-button'),
    'profile menu must still be visible after a hard refresh',
  ).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('login-button')).toHaveCount(0);
});

// Image rendering for owner of private specimen — mi-lrqt's
// regression net. Under PKCE the SPA had to wrap <img> requests with
// a custom fetch carrying the Authorization header, and the mi-lrqt
// Blob URL workaround was that wrapper. Under BFF cookies travel on
// <img> requests automatically, so the workaround is gone (mi-3vc4
// deleted frontend/src/lib/photos/blob-url.ts) and a plain <img
// src="/api/v1/photos/.../variants/...">  must render.
test('private specimen photo renders for owner via cookie auth (no Blob workaround)', async ({
  page,
  baseURL,
}) => {
  // Seed a private specimen + photo via the password-grant token —
  // the BFF login round-trip is already covered by the test above;
  // re-doing it here would blow past the per-test budget.
  const token = await mintAccessToken();
  const api = await playwrightRequest.newContext({
    baseURL: baseURL ?? 'http://localhost:8080',
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
  let specimenId: string;
  try {
    await api.post('/api/v1/profile', { data: { display_name: 'BFF Photo Owner' } });
    const create = await api.post('/api/v1/specimens', {
      data: {
        type: 'mineral',
        name: `Private Photo BFF ${Date.now()}`,
        visibility: 'private',
      },
    });
    if (!create.ok()) {
      throw new Error(`seed specimen: ${create.status()} — ${await create.text()}`);
    }
    specimenId = ((await create.json()) as { id: string }).id;

    const upload = await api.post(`/api/v1/specimens/${specimenId}/photos`, {
      multipart: {
        file: { name: 'private.png', mimeType: 'image/png', buffer: TINY_PNG },
        kind: 'visible',
      },
    });
    if (!upload.ok()) {
      throw new Error(`seed photo: ${upload.status()} — ${await upload.text()}`);
    }
  } finally {
    await api.dispose();
  }

  // Log in as the same user via the BFF flow so the browser holds a
  // session cookie. The bearer token used for seeding never reaches
  // the browser.
  await page.goto('/');
  await page.getByTestId('login-button').click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });
  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();
  await expect(page.getByTestId('profile-menu-button')).toBeVisible({ timeout: 30_000 });

  await page.goto(`/#/specimens/${specimenId}`);
  const hero = page.getByTestId('hero-photo');
  await expect(hero, 'private-owned hero photo must render').toBeVisible({ timeout: 15_000 });

  // The hero photo is a real <img>, served by /api/v1/photos/{id}/...,
  // authenticated via the session cookie. naturalWidth > 0 means the
  // bytes actually decoded — a Blob workaround would still render
  // visible boxes even on a failed network request.
  const naturalWidth = await hero.evaluate((el) => {
    const img = el.tagName === 'IMG' ? (el as HTMLImageElement) : el.querySelector('img');
    return img?.naturalWidth ?? 0;
  });
  expect(
    naturalWidth,
    '<img> must decode the photo bytes — no Blob URL workaround',
  ).toBeGreaterThan(0);
});

// CSRF on write paths: a same-origin POST WITHOUT the X-CSRF-Token
// header must be rejected. We hit /api/v1/specimens via the page's
// own fetch (so the session cookie is attached automatically) and
// assert 403 csrf_missing.
test('cross-tab POST carrying the cookie but no CSRF header → 403 csrf_missing', async ({
  page,
}) => {
  // Get a session first.
  await page.goto('/');
  await page.getByTestId('login-button').click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });
  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();
  await expect(page.getByTestId('profile-menu-button')).toBeVisible({ timeout: 30_000 });

  // Issue the no-CSRF POST from inside the page so the session cookie
  // is attached by the browser. The header is deliberately omitted —
  // CSRFMiddleware should reject before the handler runs.
  const result = await page.evaluate(async () => {
    const res = await fetch('/api/v1/specimens', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'mineral',
        name: 'csrf-attack-attempt',
        visibility: 'private',
      }),
    });
    return { status: res.status, body: await res.json().catch(() => null) };
  });
  expect(result.status, 'POST without CSRF token must 403').toBe(403);
  expect(
    (result.body as { error?: { code?: string } } | null)?.error?.code,
    'error envelope code must be csrf_missing',
  ).toBe('csrf_missing');
});

// Logout invalidates the session row server-side; a stale cookie
// cannot be replayed.
test('logout clears the session and a stale cookie cannot be replayed', async ({
  page,
  context,
  baseURL,
}) => {
  await page.goto('/');
  await page.getByTestId('login-button').click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });
  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();
  await expect(page.getByTestId('profile-menu-button')).toBeVisible({ timeout: 30_000 });

  // Capture the session cookie value before logout so we can attempt
  // the replay below.
  const origin = new URL(baseURL ?? 'http://localhost:8080').origin;
  const before = await context.cookies(origin);
  const sessionBefore = before.find((c) => c.name === 'minerals_session');
  expect(sessionBefore?.value).toBeTruthy();

  // Click the profile menu, then sign out. ProfileMenu's signOut() is
  // a fetch POST to /auth/logout carrying X-CSRF-Token and then a
  // window.location.assign('/').
  await page.getByTestId('profile-menu-button').click();
  const signOut = page.getByTestId('profile-menu-signout');
  await expect(signOut).toBeVisible();
  await signOut.click();

  // SPA reboots on '/'; the Login button must come back and the
  // profile menu must be gone.
  await expect(page.getByTestId('login-button')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('profile-menu-button')).toHaveCount(0);

  // /api/v1/profile is anonymous now — the backend cleared the
  // cookie on logout, so the next probe gets a 401.
  const profileProbe = await page.evaluate(async () => {
    const res = await fetch('/api/v1/profile', { credentials: 'include' });
    return res.status;
  });
  expect(profileProbe, '/api/v1/profile must 401 after logout').toBe(401);

  // Stale-cookie replay: re-inject the pre-logout cookie value and
  // assert the backend still treats the request as anonymous (the
  // session row was revoked).
  await context.addCookies([
    {
      name: 'minerals_session',
      value: sessionBefore?.value ?? '',
      url: origin,
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  const replay = await page.evaluate(async () => {
    const res = await fetch('/api/v1/profile', { credentials: 'include' });
    return res.status;
  });
  expect(replay, 'stale-cookie replay must not resurrect the session').toBe(401);
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
