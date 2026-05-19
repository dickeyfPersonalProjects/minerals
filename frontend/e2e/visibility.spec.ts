// Per-field visibility end-to-end (mi-fo8 #9, bead mi-eex). Drives
// the full stack: an authenticated user sets a profile field default,
// creates a public specimen with mixed per-field and per-image
// visibility overrides, then a brand-new anonymous browser session
// loads the specimen's public detail page and asserts the redaction
// reaches the rendered DOM — not just the API response.
//
// The acceptance contract for this spec (from the bead):
//   * the specimen renders for anonymous viewers,
//   * its name and description render,
//   * no element representing the price field appears in the DOM,
//   * the 'Acquired from' field is absent (it falls through the chain
//     to the user-level default, which is set to a value the anonymous
//     viewer cannot see),
//   * at least one image with explicit per-image visibility=public
//     renders, while an image that inherits the specimen's images-
//     default does not (because that default is overridden to
//     private at the specimen level — see the open-question note
//     in CONTRACT.md §13b on per-image loosening).
//
// The bead deliberately demands DOM-level checks: an API redaction
// that ships next to a UI bug like `Price: undefined` is still a leak.
// Network spying is therefore avoided; everything below the
// 'navigate as anonymous' line reads the rendered page.
//
// Setup runs via direct API calls (with a Keycloak password-grant
// token) because the bead's ≤90 s budget would not survive a full
// click-through of profile → create → upload → patch each per-field
// override. The PKCE round-trip (mi-dwx, auth.spec.ts) covers the
// SPA-driven login path; this spec covers the post-login privacy
// surface area.

import { test, expect, request as playwrightRequest } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';

const KEYCLOAK_BASE_URL = process.env.E2E_KEYCLOAK_BASE_URL ?? 'http://localhost:8081';
const KEYCLOAK_REALM = process.env.E2E_KEYCLOAK_REALM ?? 'minerals';
// The CI-only password-grant client (terraform/keycloak/clients.tf).
// Backend JWT verification accepts tokens minted by it just as it
// accepts tokens from `minerals-frontend` (same realm, same JWKS).
const KEYCLOAK_TEST_CLIENT = process.env.E2E_KEYCLOAK_TEST_CLIENT ?? 'minerals-test';
const TEST_USERNAME = process.env.E2E_USERNAME ?? 'user1@localhost';
const TEST_PASSWORD = process.env.E2E_PASSWORD ?? 'MineralsDev123!';

// Smallest valid PNG that the server's imageproc.Generate pipeline
// can decode end-to-end (1x1, single transparent pixel). Generating
// real bytes here avoids depending on a fixture file that might
// drift from the imageproc decoder's allowlist.
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

test('per-field visibility — anonymous viewer sees public fields, redacted fields are absent from DOM', async ({
  page,
  baseURL,
}) => {
  // Step 1: authenticate against the realm via the password-grant
  // client and stand up a request context bound to that bearer.
  const token = await mintAccessToken();
  const api = await playwrightRequest.newContext({
    baseURL: baseURL ?? 'http://localhost:8080',
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });

  try {
    // Step 2: complete the first-login profile gate (mi-2hf). Idempotent
    // — subsequent runs against the same realm just re-affirm the row.
    await expectOk(
      await api.post('/api/v1/profile', {
        data: { display_name: 'Visibility E2E' },
      }),
      'POST /api/v1/profile (setup)',
    );

    // Step 3: write the per-field defaults. `acquired_from = private`
    // is the lever that proves the field is redacted from the
    // anonymous viewer when no specimen-level override exists — the
    // chain falls through to this user default. `price = unlisted` is
    // a non-default value the specimen-level override will shadow with
    // `private`; we still patch it so the resolved chain layer is
    // user-default, not system-default, and any future regression that
    // accidentally skipped the user layer would surface here.
    // The mi-z3d0 additions: acquired_at and catalog_number also
    // default to private at the user level so anonymous browsers
    // never see them on a specimen that doesn't override them
    // per-row. There is no per-specimen override column for these
    // two fields, so the user-default layer is the ONLY thing
    // gating their visibility.
    await expectOk(
      await api.patch('/api/v1/profile', {
        data: {
          field_defaults: {
            price: 'unlisted',
            acquired_from: 'private',
            acquired_at: 'private',
            catalog_number: 'private',
          },
        },
      }),
      'PATCH /api/v1/profile (field_defaults)',
    );

    // Round-trip check (mi-z3d0): GET /api/v1/profile must reflect
    // the keys just written, including the new ones.
    const profileRes = await expectOk(
      await api.get('/api/v1/profile'),
      'GET /api/v1/profile (after PATCH)',
    );
    const profile = (await profileRes.json()) as {
      field_defaults: Record<string, string> | null;
    };
    if (
      profile.field_defaults?.acquired_at !== 'private' ||
      profile.field_defaults?.catalog_number !== 'private'
    ) {
      throw new Error(
        `GET /api/v1/profile did not round-trip new field_defaults keys: ${JSON.stringify(
          profile.field_defaults,
        )}`,
      );
    }

    // Step 4: create a public specimen carrying values for every
    // protected scalar field. The acquired_from / catalog_number
    // strings are deliberately distinctive so a regression that
    // leaks the value into the DOM can be spotted in trace artefacts.
    const distinctiveAcquiredFrom = `SecretSourceE2E-${Date.now()}`;
    const distinctiveCatalog = `CAT-LEAK-${Date.now()}`;
    const specimenName = `Visibility E2E ${Date.now()}`;
    const createRes = await expectOk(
      await api.post('/api/v1/specimens', {
        data: {
          type: 'mineral',
          name: specimenName,
          description: 'Auto-generated specimen for the per-field visibility Playwright spec.',
          visibility: 'public',
          price_cents: 12345,
          acquired_from: distinctiveAcquiredFrom,
          acquired_at: '2024-01-02',
          catalog_number: distinctiveCatalog,
        },
      }),
      'POST /api/v1/specimens',
    );
    const specimen = (await createRes.json()) as { id: string };
    const specimenId = specimen.id;

    // Step 5: upload two photos. The first will be explicitly public;
    // the second stays at the per-image default (null override) so the
    // resolved chain steps through the specimen-level images default.
    const uploadPhoto = async (filename: string): Promise<{ id: string }> => {
      const res = await api.post(`/api/v1/specimens/${specimenId}/photos`, {
        multipart: {
          file: { name: filename, mimeType: 'image/png', buffer: TINY_PNG },
          kind: 'visible',
        },
      });
      await expectOk(res, `POST photos (${filename})`);
      return (await res.json()) as { id: string };
    };
    const publicPhoto = await uploadPhoto('public.png');
    const inheritingPhoto = await uploadPhoto('inherits.png');

    // Step 6: mark publicPhoto's visibility explicitly public. The
    // other photo's visibility column stays null.
    await expectOk(
      await api.patch(`/api/v1/photos/${publicPhoto.id}`, {
        data: { visibility: 'public' },
      }),
      'PATCH /api/v1/photos (publicPhoto → public)',
    );

    // Step 7: override two specimen-level per-field visibilities:
    //   * visibility_price = private  → anonymous viewer never sees price.
    //   * visibility_images = private → an inheriting photo (null
    //     override) resolves to private; only photos with their own
    //     explicit non-private visibility survive redaction for anon.
    // visibility_acquired_from stays null so the chain falls through
    // to the user-default = private. That makes anon's acquired_from
    // absent through the user-default layer, not the specimen-field
    // layer — exercising a second layer of the chain.
    await expectOk(
      await api.patch(`/api/v1/specimens/${specimenId}`, {
        data: {
          visibility_price: 'private',
          visibility_images: 'private',
        },
      }),
      'PATCH /api/v1/specimens (visibility overrides)',
    );

    // Step 8: switch to a brand-new browser identity. The Playwright
    // `page` fixture starts with empty storage, but clearing cookies
    // and storage is defence-in-depth against test order changes.
    await page.context().clearCookies();

    // Surface page errors in the trace so a SPA exception during the
    // anonymous render shows up in the failure artefact without
    // needing a local repro.
    const pageErrors: string[] = [];
    page.on('pageerror', (err) => {
      pageErrors.push(err.message);
    });

    // Hit the specimen's hash route directly as an anonymous viewer.
    // path-to-hash.ts rewrites the path to a hash for svelte-spa-router;
    // the hash form here sidesteps that rewrite and matches the URL the
    // SPA itself produces when navigating internally.
    await page.goto(`/#/specimens/${specimenId}`);

    const article = page.getByTestId('specimen-detail');
    await expect(article, 'public specimen must render for anonymous viewers').toBeVisible();

    // The visible-fields half of the contract: name and description
    // both render. Using exact text on the name guards against
    // header-bar / breadcrumb collisions returning a stale match.
    await expect(page.getByTestId('specimen-name')).toHaveText(specimenName);
    await expect(page.getByTestId('description-body')).toContainText(
      'Auto-generated specimen for the per-field visibility',
    );

    // Specimen-level visibility chip is present (the specimen is public).
    await expect(page.getByTestId('visibility-chip')).toHaveText(/public/i);

    // The redacted-fields half of the contract. The bead's diagnostic
    // rationale: even a correct API redaction can leak via a stale
    // SPA rendering "Price: undefined" or "Acquired from:" with no
    // value — those DOM strings would fail this assertion. We scope
    // to the specimen article so navbar text doesn't pollute matches.
    await expect(
      article.getByText(/\bPrice\b/i),
      'price field must not appear anywhere in the rendered specimen',
    ).toHaveCount(0);
    await expect(
      article.getByText(/Acquired from/i),
      'acquired_from label must not appear (user-default = private)',
    ).toHaveCount(0);
    // Belt and suspenders: the distinctive value itself must never
    // appear, even if a future UI rendered the value without a label.
    await expect(
      article.getByText(distinctiveAcquiredFrom, { exact: false }),
      'acquired_from value must not leak in any DOM text',
    ).toHaveCount(0);

    // mi-z3d0 — new redactable scalars. acquired_at and catalog_number
    // have no per-specimen override column; the user-default = private
    // is the only chain layer that gates them, so anon must NEVER see
    // either the label or the value.
    await expect(
      article.getByText(/Acquired\b/i),
      'acquired_at label must not appear (user-default = private)',
    ).toHaveCount(0);
    await expect(
      article.getByText(distinctiveCatalog, { exact: false }),
      'catalog_number value must not leak in any DOM text (user-default = private)',
    ).toHaveCount(0);

    // Image-visibility chain: exactly one photo should be reachable to
    // anonymous viewers. The hero slot pulls the first photo in the
    // visible set, so when only one photo survives redaction the hero
    // renders it and the thumbnail strip is empty.
    //
    // (If the parent EPIC's open question — can a per-image override
    // loosen the specimen's images-default? — flips to "no", this
    // count becomes 0 because the explicit `public` photo no longer
    // wins against the specimen's `private` images-default. Adjust
    // here in lockstep with the visibility helper.)
    await expect(page.getByTestId('hero-photo')).toBeVisible();
    await expect(page.getByTestId('gallery-thumb')).toHaveCount(0);

    expect(
      pageErrors,
      `unexpected page errors during anonymous render: ${pageErrors.join(' | ')}`,
    ).toEqual([]);

    // Inheriting photo id is captured to make the trace self-describing
    // — if a future regression flips redaction semantics this id is
    // the one the diff is about.
    test.info().annotations.push({
      type: 'inheriting-photo-id',
      description: inheritingPhoto.id,
    });
  } finally {
    await api.dispose();
  }
});
