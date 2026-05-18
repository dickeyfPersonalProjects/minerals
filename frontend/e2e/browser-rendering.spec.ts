// Browser-rendering audit for protected resources (mi-8ppx,
// hq-ohz5 Snippet 2). Sibling to visibility.spec.ts: that spec
// covers the anonymous-viewer-sees-public-photo path; this spec
// covers the OWNER-viewing-private-resource paths that mi-lrqt
// fixed (photo blob-URL pipeline) and the same exposure on
// journal file downloads.
//
// The principle the audit enforces: every protected resource the
// SPA renders through a browser-issued fetch (<img>, <video>,
// <a download>) MUST have a Playwright assertion that the bytes
// actually arrived for the authenticated owner. A toBeVisible()
// on the wrapping element is not enough — a 404 still renders a
// broken-image placeholder that satisfies toBeVisible().
//
// Setup uses the password-grant client to mint a token and
// provision resources via direct API calls (matches the pattern
// from visibility.spec.ts — the bead's 90 s budget would not
// survive a full SPA click-through of every fixture). The
// browser side then completes the real PKCE login to put the SPA
// into the owner's authenticated state; once mounted, the SPA's
// own bearer drives the browser-issued fetches we're auditing.

import { test, expect, request as playwrightRequest } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';
import { expectDownloadable, expectImgLoaded } from './helpers/loaded';

const KEYCLOAK_BASE_URL = process.env.E2E_KEYCLOAK_BASE_URL ?? 'http://localhost:8081';
const KEYCLOAK_REALM = process.env.E2E_KEYCLOAK_REALM ?? 'minerals';
const KEYCLOAK_TEST_CLIENT = process.env.E2E_KEYCLOAK_TEST_CLIENT ?? 'minerals-test';
const TEST_USERNAME = process.env.E2E_USERNAME ?? 'user1@localhost';
const TEST_PASSWORD = process.env.E2E_PASSWORD ?? 'MineralsDev123!';

// 1x1 transparent PNG — same fixture visibility.spec.ts uses. The
// server's imageproc decoder accepts it end-to-end and the resulting
// processed bytes have non-zero dimensions, so naturalWidth > 0 on
// the rendered <img> proves the full pipeline succeeded.
const TINY_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=',
  'base64',
);

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

async function expectOk(res: Awaited<ReturnType<APIRequestContext['post']>>, label: string) {
  if (!res.ok()) {
    throw new Error(`${label} failed: ${res.status()} ${res.statusText()} — ${await res.text()}`);
  }
  return res;
}

// Drive the real SPA login round-trip so the in-memory auth store
// holds a valid access token. Mirrors auth.spec.ts's PKCE walk; kept
// inline (rather than extracted) because the auth spec is the
// canonical demonstration — duplicating ~20 lines here costs less
// than the indirection of a shared helper that the auth spec would
// also have to consume.
async function loginViaSpa(page: import('@playwright/test').Page, baseURL: string) {
  await page.goto('/');
  const loginButton = page.getByTestId('login-button');
  await expect(loginButton, 'login button must render on cold load').toBeVisible();
  await loginButton.click();
  await page.waitForURL(/\/realms\/minerals\/protocol\/openid-connect\/auth/, {
    timeout: 30_000,
  });
  await page.locator('#username').fill(TEST_USERNAME);
  await page.locator('#password').fill(TEST_PASSWORD);
  await page.locator('#kc-login').click();
  await expect(
    page.getByTestId('profile-menu-button'),
    'profile menu must appear after token exchange',
  ).toBeVisible({ timeout: 30_000 });
  // Make sure we are on the SPA origin, not a stale Keycloak page.
  await page.waitForURL((u) => u.origin === baseURL, { timeout: 10_000 });
}

test('owner viewing private specimen — hero photo bytes actually load in the browser (mi-lrqt regression)', async ({
  page,
  baseURL,
}) => {
  const appBase = baseURL ?? 'http://localhost:8080';
  const token = await mintAccessToken();
  const api = await playwrightRequest.newContext({
    baseURL: appBase,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });

  let specimenId: string;
  try {
    // First-login profile gate (mi-2hf) — idempotent.
    await expectOk(
      await api.post('/api/v1/profile', { data: { display_name: 'Browser Rendering E2E' } }),
      'POST /api/v1/profile (setup)',
    );

    // Private specimen with a private photo. Both resources rely on
    // §13 RBAC to gate the bytes: anyone but the owner gets 404 on
    // GET /photos/{id}/display, so the only way the <img> can render
    // is if the SPA's bearer reaches the request — which is what
    // mi-lrqt's AuthedImage / blob-URL pipeline ensures.
    const specimenName = `Browser Render E2E ${Date.now()}`;
    const createRes = await expectOk(
      await api.post('/api/v1/specimens', {
        data: {
          type: 'mineral',
          name: specimenName,
          description: 'Auto-generated for mi-8ppx browser-rendering audit.',
          visibility: 'private',
        },
      }),
      'POST /api/v1/specimens',
    );
    specimenId = ((await createRes.json()) as { id: string }).id;

    const uploadRes = await api.post(`/api/v1/specimens/${specimenId}/photos`, {
      multipart: {
        file: { name: 'private.png', mimeType: 'image/png', buffer: TINY_PNG },
        kind: 'visible',
      },
    });
    await expectOk(uploadRes, 'POST private photo');

    // Drive the real PKCE login so the SPA's in-memory auth store
    // holds a token for the same user that owns the specimen above.
    await loginViaSpa(page, appBase);

    const pageErrors: string[] = [];
    page.on('pageerror', (err) => pageErrors.push(err.message));

    await page.goto(`/#/specimens/${specimenId}`);
    await expect(page.getByTestId('specimen-detail')).toBeVisible();
    await expect(page.getByTestId('specimen-name')).toHaveText(specimenName);

    // The audit assertion. AuthedImage renders <img data-src=…>
    // once loadAuthedBlobUrl resolves. naturalWidth > 0 proves the
    // browser actually decoded a bitmap — a 404 (the pre-mi-lrqt
    // failure mode) or a pending fetch would leave it at 0 and
    // fail this assertion before the 15 s poll timeout.
    await expectImgLoaded(page.getByTestId('hero-photo'));

    // Belt and suspenders: the <img>'s data-src attribute must
    // mirror the backend path so a future refactor that replaces
    // AuthedImage with a raw <img src="/api/v1/..."> (which would
    // silently break for non-public photos again) cannot quietly
    // sneak through. The path is observable; assert it.
    const dataSrc = await page
      .getByTestId('hero-photo')
      .locator('img')
      .first()
      .getAttribute('data-src');
    expect(dataSrc, 'hero photo <img> must expose its backend path via data-src').toMatch(
      /^\/api\/v1\/photos\/[a-f0-9-]+\/display$/,
    );

    expect(
      pageErrors,
      `unexpected page errors during owner render: ${pageErrors.join(' | ')}`,
    ).toEqual([]);
  } finally {
    await api.dispose();
  }
});

test('anonymous viewer cannot fetch private photo bytes — protection still holds', async () => {
  // The flip side of the audit: for every protected resource type
  // we assert the OWNER can load, we should also assert ANONYMOUS
  // cannot. Otherwise a "load-completed" assertion that passes for
  // an inadvertently-public resource would silently pass even if
  // the protection were stripped. The check is a direct GET (no
  // bearer) — bypasses the SPA so we audit the backend handler
  // itself.

  const token = await mintAccessToken();
  const api = await playwrightRequest.newContext({
    baseURL: 'http://localhost:8080',
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
  let photoId: string;
  try {
    await expectOk(
      await api.post('/api/v1/profile', { data: { display_name: 'Browser Rendering E2E' } }),
      'POST /api/v1/profile (setup)',
    );
    const createRes = await expectOk(
      await api.post('/api/v1/specimens', {
        data: {
          type: 'mineral',
          name: `Browser Render Negative ${Date.now()}`,
          description: 'Negative-coverage fixture for mi-8ppx.',
          visibility: 'private',
        },
      }),
      'POST /api/v1/specimens',
    );
    const specimen = (await createRes.json()) as { id: string };
    const photoRes = await api.post(`/api/v1/specimens/${specimen.id}/photos`, {
      multipart: {
        file: { name: 'priv.png', mimeType: 'image/png', buffer: TINY_PNG },
        kind: 'visible',
      },
    });
    await expectOk(photoRes, 'POST private photo (negative coverage)');
    photoId = ((await photoRes.json()) as { id: string }).id;
  } finally {
    await api.dispose();
  }

  const anon = await playwrightRequest.newContext({ baseURL: 'http://localhost:8080' });
  try {
    const res = await anon.get(`/api/v1/photos/${photoId}/display`);
    // CONTRACT §13 v2 don't-leak-existence: anon viewer sees 404,
    // never 403. The status itself is part of the contract.
    expect(
      res.status(),
      `anonymous viewer must NOT be able to fetch private photo bytes (got ${res.status()})`,
    ).toBe(404);
  } finally {
    await anon.dispose();
  }
});

// Journal file attachment downloads. The audit grep finds
// `<a href="/api/v1/files/${id}" download>` in JournalAttachments.svelte
// — same browser-issued GET shape as the pre-mi-lrqt photo case, with
// the same exposure: the SPA's bearer never reaches the request, so
// any non-public journal attachment downloads as a 404. mi-jv0a is
// the fix (mirror mi-lrqt's blob-URL pattern for downloads); until it
// lands, the load-completed assertion would always fail. fixme()
// keeps the scenario in the spec so the audit's intent is auditable
// and the test wakes up automatically when mi-jv0a lands.
test.fixme('owner downloading private journal attachment — bytes load via authed pipeline (blocked on mi-jv0a)', async () => {
  const token = await mintAccessToken();
  const api = await playwrightRequest.newContext({
    baseURL: 'http://localhost:8080',
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
  try {
    await expectOk(
      await api.post('/api/v1/profile', {
        data: { display_name: 'Browser Rendering E2E' },
      }),
      'POST /api/v1/profile (setup)',
    );
    const createRes = await expectOk(
      await api.post('/api/v1/specimens', {
        data: {
          type: 'mineral',
          name: `Journal Download E2E ${Date.now()}`,
          description: 'Fixture for mi-8ppx journal-attachment scenario.',
          visibility: 'private',
        },
      }),
      'POST /api/v1/specimens',
    );
    const specimen = (await createRes.json()) as { id: string };

    const entryRes = await expectOk(
      await api.post(`/api/v1/specimens/${specimen.id}/journal`, {
        data: { body_md: 'audit fixture entry' },
      }),
      'POST journal entry',
    );
    const entry = (await entryRes.json()) as { id: string };

    const attachRes = await api.post(`/api/v1/journal/${entry.id}/files`, {
      multipart: {
        file: { name: 'audit.txt', mimeType: 'text/plain', buffer: Buffer.from('hi') },
      },
    });
    await expectOk(attachRes, 'POST journal attachment');
    const att = (await attachRes.json()) as { file_id: string };

    // expectDownloadable goes through the bearer-attached API
    // context, which mi-jv0a will mirror in the SPA. Once that
    // ships, drop fixme() and this becomes the standing audit.
    await expectDownloadable(api, `/api/v1/files/${att.file_id}`, 'text/');
  } finally {
    await api.dispose();
  }
});
