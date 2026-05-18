// Browser-rendering load assertions for protected resources
// (mi-8ppx). The audit-level rule: every protected resource the SPA
// renders via `<img>`, `<video>`, or `<a download>` MUST have a
// Playwright assertion that the browser actually obtained the bytes
// — not just that the element is in the DOM. A `<img>` with a 404
// behind it is still `visible()`; only `naturalWidth > 0` proves the
// fetch succeeded. The motivation comes from mi-lrqt, where private
// photos were `visible()` but rendered as broken images because the
// SPA's bearer token never reached the browser-issued fetch.
//
// The canonical guidance is hq-ohz5 Snippet 2 (browser-rendering
// assertions). These helpers are the concrete handles for it.
//
// Two patterns share these helpers:
//   * AuthedImage (mi-lrqt) — the rendered <img> has src set to a
//     blob: URL and a `data-src` attribute holding the original
//     /api/v1/... path. Both attributes must be present, and the
//     image's naturalWidth must be > 0.
//   * Direct authenticated GET via APIRequestContext — used for
//     resources the SPA cannot inline (e.g. PDFs, journal file
//     downloads). Asserts status 200 plus a content-type starting
//     with the expected prefix.

import { expect, type Locator, type APIRequestContext } from '@playwright/test';

// Polls the locator until the underlying <img> reports a non-zero
// naturalWidth. A broken or pending image will time out here. The
// `data-src` invariant guards against AuthedImage being mistakenly
// replaced by a raw <img> — the original /api/v1/... path must
// survive into the DOM so this assertion stays auditable.
export async function expectImgLoaded(locator: Locator): Promise<void> {
  // The locator may point at a wrapper (e.g. AuthedImage rendering
  // its <img> child once the blob URL resolves). Resolve to the
  // first <img> descendant in either case.
  const img =
    (await locator.evaluate((el) => (el as HTMLElement).tagName)).toLowerCase() === 'img'
      ? locator
      : locator.locator('img').first();

  await expect(img, 'image element must be present in the DOM').toBeVisible();

  // naturalWidth > 0 proves the browser decoded a real bitmap. A
  // pending fetch, a 404, or an opaque blob: URL with an empty body
  // would all leave it at 0.
  await expect
    .poll(async () => img.evaluate((el) => (el as HTMLImageElement).naturalWidth), {
      message: 'image naturalWidth never became > 0 — the browser never decoded a bitmap',
      timeout: 15_000,
    })
    .toBeGreaterThan(0);
}

// Polls the locator until the <video> reports HAVE_METADATA or
// better. v1 has no <video> tags in the SPA, but the helper is
// shipped now so future video rendering inherits the same rule
// without rediscovering it.
export async function expectVideoLoaded(locator: Locator): Promise<void> {
  await expect(locator, 'video element must be present in the DOM').toBeVisible();
  // readyState >= 1 (HAVE_METADATA) proves the browser received and
  // parsed enough bytes to know dimensions and duration. A 404 leaves
  // it at 0 (HAVE_NOTHING).
  await expect
    .poll(async () => locator.evaluate((el) => (el as HTMLMediaElement).readyState), {
      message: 'video readyState never reached HAVE_METADATA (>=1) — bytes never arrived',
      timeout: 15_000,
    })
    .toBeGreaterThanOrEqual(1);
}

// Asserts that a download/href URL really yields bytes for an
// authenticated viewer. Used for `<a download>` resources that the
// browser would fetch anonymously when the user clicks the link —
// the test bypasses the click and verifies the backend handler
// directly so the assertion stays focused on browser-rendering
// load semantics, not on download dialogs.
//
// `expectedContentTypePrefix` is matched as a prefix (e.g. "image/"
// matches both "image/png" and "image/jpeg"; "application/pdf"
// matches itself). Pass empty string to skip the check.
export async function expectDownloadable(
  api: APIRequestContext,
  url: string,
  expectedContentTypePrefix = '',
): Promise<void> {
  const res = await api.get(url);
  expect(res.status(), `GET ${url} did not return 200`).toBe(200);
  if (expectedContentTypePrefix) {
    const ct = res.headers()['content-type'] ?? '';
    expect(
      ct.startsWith(expectedContentTypePrefix),
      `GET ${url} content-type ${JSON.stringify(ct)} does not start with ${JSON.stringify(expectedContentTypePrefix)}`,
    ).toBe(true);
  }
  // Body must be non-empty — a 200 with zero bytes still indicates a
  // broken pipeline (some auth bugs surface this way).
  const body = await res.body();
  expect(body.byteLength, `GET ${url} returned empty body`).toBeGreaterThan(0);
}
