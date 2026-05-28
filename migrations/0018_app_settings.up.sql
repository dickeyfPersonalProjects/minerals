-- 0018_app_settings: a small key-value store for runtime-mutable
-- application settings (mi-pkn2). Its first (and currently only) key
-- is `registration_enabled` — the operator-flippable self-signup
-- toggle the admin console writes and the BFF /auth/register gate
-- reads per request.
--
-- Design decisions:
--   - Key-value, not a column-per-setting table: a runtime toggle is
--     read/written by key, and a wide single-row table would need a
--     migration for every new knob. The value is stored as TEXT and
--     interpreted by the typed repo accessor (booleans are 'true' /
--     'false').
--   - No seed row: an ABSENT key means "fall back to the configured
--     env default" (REGISTRATION_ENABLED). The toggle only writes a
--     row once an operator flips it, so a fresh install keeps honoring
--     the deploy-time default until then.
--   - updated_by is the opaque users.id of the operator who last wrote
--     the value (nullable: a value written by a non-user path, or
--     before the column was populated, stays NULL). No FK — the
--     audit breadcrumb must survive the referenced user's erasure.

BEGIN;

CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT        NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID
);

COMMIT;
