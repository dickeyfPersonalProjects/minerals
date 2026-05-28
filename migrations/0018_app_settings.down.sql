-- Roll back 0018: drop the app_settings key-value store.
--
-- Data loss: every runtime-set setting is discarded. The application
-- falls back to env-only configuration (e.g. REGISTRATION_ENABLED)
-- once the table is gone.

BEGIN;

DROP TABLE IF EXISTS app_settings;

COMMIT;
