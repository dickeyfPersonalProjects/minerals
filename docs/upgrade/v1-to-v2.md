# V1 → V2 upgrade runbook

This is the one-shot operator runbook for promoting a production
environment from V1 (no auth — every row owned by the seeded
"stub-overseer" sentinel) to V2 (real Keycloak OIDC auth, per-user
rows).

> **Audience:** the operator running the V2 upgrade against a live
> environment whose data was created under V1. New environments
> deployed fresh on V2 don't need this — the first real login simply
> creates their user row and the absence of legacy data makes the
> bootstrap step a no-op.

Pairs with:

- [`../deploy/README.md`](../deploy/README.md) — base/overlay GitOps flow.
- [`../deploy/keycloak.md`](../deploy/keycloak.md) — Keycloak/OIDC setup.
- [`../../CONTRACT.md#13--auth-rules`](../../CONTRACT.md) §13 — auth rules.

## Why this exists

V1 had no authentication. Every row inserted under V1 was tagged with
the stub-overseer UUID (`00000000-0000-0000-0000-000000000001`) — a
sentinel pre-seeded into the `users` table by migration 0008 so the
`author_id` / `uploaded_by` / `user_id` foreign keys always resolve.

When V2 turns real auth on and the operator logs in via Keycloak, a
new user row is created with their actual `keycloak_sub`. The
existing V1 data still belongs to the stub-overseer, so the SPA's
per-author filters hide it from the new operator. The
`bootstrap-claim-orphans` subcommand reassigns every V1-era row to
the operator's new user in a single transaction.

## Upgrade steps

### 1. Deploy the V2 image

Roll the new image normally. The init-container applies all pending
migrations (including the users table from 0008 and the V2 auth
schema from 0015). No schema change is required for the orphan-claim
itself: `author_id` and friends are already `NOT NULL` with FKs to
`users(id)`; all V1 rows already satisfy that constraint via the
stub-overseer row.

### 2. Log in via Keycloak

Go through the first-login flow:

1. Open the SPA — you'll be redirected to Keycloak.
2. Authenticate.
3. The backend creates a `users` row with `status='pending'`.
4. Complete profile setup — `status` flips to `'active'`.

You won't see any V1 data yet. That's expected: the rows still belong
to the stub-overseer.

### 3. Run `bootstrap-claim-orphans`

From the operator host:

```bash
# Dry-run first — prints per-table counts, no writes.
kubectl exec -n mineral-prod deploy/minerals -- \
  /minerals bootstrap-claim-orphans --user-email you@example.com --dry-run

# Apply.
kubectl exec -n mineral-prod deploy/minerals -- \
  /minerals bootstrap-claim-orphans --user-email you@example.com --yes
```

Expected output:

```
bootstrap-claim-orphans plan
  target user: you@example.com (id=… sub=…)
  reassigning rows from stub-overseer (00000000-0000-0000-0000-000000000001)

  specimens.author_id            142 row(s)
  collectors.author_id            38 row(s)
  journal_entries.author_id       12 row(s)
  files.uploaded_by              287 row(s)
  mineral_species.author_id       64 row(s)
  qr_sheets.user_id                3 row(s)
  TOTAL                          546 row(s)

Claimed 142 specimens
…
Total: 546 rows reassigned to you@example.com (sub=…)
```

### 4. Verify in the SPA

Refresh the SPA. Every V1 row is now attributed to your user and
visible.

## Safety guards

The command refuses to write unless ALL of these pass:

- The resolved target user exists (`--user-email` or `--user-sub`)
  and has `status='active'`. A pending user — mid-first-login,
  profile not yet completed — is rejected; complete setup first.
- The target user is **not** the stub-overseer sentinel itself.
  Stub-to-stub reassignment would be a no-op and is almost certainly
  a misconfiguration.
- The `users` table contains **exactly one** active non-stub user.
  This is the personal-app reality at V2 launch. If multiple real
  users somehow coexist (e.g. you logged in from two browser
  sessions during profile setup, both completing), the command
  refuses with exit code 2 — at that point use SQL directly with
  manual judgment about which rows go where.

If any guard trips, the command exits 2 with a structured `slog`
error explaining which guard fired. The database is untouched.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| 0    | Success — writes performed, or dry-run completed |
| 1    | Target user not found |
| 2    | Safety guard tripped (missing `--yes`, pending status, multi-user, etc.) |
| 3    | Database error during the transaction (transaction rolled back) |

## Transactional guarantees

All `UPDATE` statements run inside a single Postgres transaction at
the default isolation (READ COMMITTED). Either every affected table
moves, or none does. If the transaction fails (network glitch, FK
violation in an unforeseen edge case), the database is left in the
pre-run state and the operator can retry with the same arguments.

## Idempotency

A second run finds zero rows owned by the stub-overseer and exits 0
with a zero-row summary. Safe to re-run if you're unsure whether the
first run completed.

## What this command does NOT do

- **It is not a multi-user reconciliation tool.** No `--reassign-to`,
  no `--unclaim`, no merge-accounts flag. If a V3 ever introduces
  account merging or ownership transfer, that lives in a different
  command with consent + audit semantics (see bead notes for context).
- **It is not a live data migration mechanism.** After V2 launches,
  new rows always carry `author_id` from the auth middleware.
- **It does not relax or tighten any column constraint.** The
  schema-level NOT NULL + FK on `author_id` is already in place
  from migrations 0001 + 0011 — no follow-up tightening migration is
  needed for this codebase.

## Rollback

If the operator decides immediately after running the command that
the wrong user was selected (typo, accidental impersonation), they
can issue a manual SQL `UPDATE` reverting `author_id`/`user_id`/
`uploaded_by` back to the stub-overseer UUID. Because the command
runs once per upgrade and only writes when `--yes` is set, the
window for misuse is small. There is intentionally no `--unclaim`
flag — see "What this command does NOT do".

## Related beads

- mi-c1y — this command
- mi-7xo — V2 auth swap (where the stub-overseer pattern is documented)
- mi-tl2 — `users` table migration (seeds the stub-overseer row)
- mi-aw3a (now mi-7xo) — stub `author_id` consistency in V2
