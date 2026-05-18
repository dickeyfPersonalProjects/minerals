// Typed API client for the Minerals backend (mi-cy4).
//
// `schema.d.ts` is regenerated from the OpenAPI spec by
// `make gen-api-client`; do not edit it by hand. This module
// wraps openapi-fetch with our schema so app code calls a typed
// surface (e.g. `client.GET('/healthz')`) instead of bare fetch.
//
// Same-origin in dev (Vite proxy) and prod (SPA embedded in the
// Go binary). `credentials: 'include'` is unnecessary for the
// same-origin case (cookies travel automatically) but is set here
// so SSR / proxy-bypass / cross-origin dev scenarios still send the
// V2 BFF session cookie (mi-3vc4 / docs/design/auth-bff.md).
import createClient from 'openapi-fetch';
import type { paths } from './schema';

export const client = createClient<paths>({
  baseUrl: '/',
  credentials: 'include',
});
export type { paths } from './schema';
