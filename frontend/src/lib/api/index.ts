// Typed API client for the Minerals backend (mi-cy4).
//
// `schema.d.ts` is regenerated from the OpenAPI spec by
// `make gen-api-client`; do not edit it by hand. This module
// wraps openapi-fetch with our schema so app code calls a typed
// surface (e.g. `client.GET('/healthz')`) instead of bare fetch.
//
// Same-origin in dev (Vite proxy) and prod (SPA embedded in the
// Go binary), so no CORS wiring is needed (per design §4 / §10).
import createClient from 'openapi-fetch';
import type { paths } from './schema';

export const client = createClient<paths>({ baseUrl: '/' });
export type { paths } from './schema';
