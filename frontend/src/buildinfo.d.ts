// Compile-time constants injected by Vite's `define` (see vite.config.ts
// and vitest.config.ts). They carry the git short commit hash and the
// build timestamp so the SPA footer can show what's actually deployed —
// the human-visible detector for "tag moved but the pod didn't roll"
// (mi-c0sv). Populated from CI build args (GIT_SHA / BUILD_DATE); default
// to 'dev' / build-time `now` for local `npm run dev` and tests.
declare const __GIT_SHA__: string;
declare const __BUILD_DATE__: string;
