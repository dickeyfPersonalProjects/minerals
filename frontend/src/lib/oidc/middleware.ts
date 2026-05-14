// openapi-fetch middleware that attaches `Authorization: Bearer`
// from the in-memory auth store to every outgoing request.
//
// Side-effecting install — main.ts calls it once at startup so all
// API traffic carries the token without per-call wiring. Idempotent:
// repeated calls install only once.

import { client } from '../api';
import { getAccessToken } from './auth';

let installed = false;

export function installAuthHeaderMiddleware(): void {
  if (installed) return;
  installed = true;
  client.use({
    onRequest({ request }) {
      const token = getAccessToken();
      if (token && !request.headers.has('Authorization')) {
        request.headers.set('Authorization', `Bearer ${token}`);
      }
      return request;
    },
  });
}
