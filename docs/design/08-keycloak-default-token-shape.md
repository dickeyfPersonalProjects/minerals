# 08 — Default-shaped Keycloak token (mi-2xa4)

## Why this document exists

Two P0/P1 auth bugs (mi-cl1, mi-2eg6) shipped to staging because the CI
smoke tested **fixture-shaped** users (seeded by
`terraform/keycloak/dev-seed.sh`) instead of **default-shaped** users
(what a freshly-created user looks like coming out of Keycloak with
default realm config). The fixture path bakes in roles, audience
mappers, attributes and group memberships the backend silently
depends on. Staging — which had not had the same Terraform applied —
issued tokens missing those properties, and the backend rejected every
authenticated write.

This document is the contract the backend programs against. **If the
backend assumes any claim shape, that assumption MUST hold for the
default-shaped token below — not just the fixture token.**

## What "default-shaped" means in CI

The `keycloak-smoke` job in `.github/workflows/pr.yml` provisions a
default-shaped user via the Keycloak Admin API with this body:

```json
{
  "username": "smoke-default@localhost",
  "email":    "smoke-default@localhost",
  "enabled":  true,
  "emailVerified": true,
  "firstName": "Default",
  "lastName":  "Shape",
  "credentials": [{"type": "password", "value": "<dev-only>", "temporary": false}]
}
```

NO explicit role assignments, NO group memberships, NO attribute
pre-fill. `firstName`/`lastName` are the only fixture-y values — and
those exist solely because Keycloak's default user profile marks a
user missing them as "not fully set up" and refuses to issue a
password-grant token (the same constraint `dev-seed.sh` documents).

## The token shape we contract against

Pinned in `internal/oidc/testdata/keycloak-default-token-claims.json`.
The smoke diffs the live token's `realm_access.roles` against this
file; the unit test in `internal/api/authz_default_shape_test.go`
asserts the backend authz code grants the implicit `user` role to a
caller carrying exactly this role set.

| Claim | Value | Source |
| --- | --- | --- |
| `realm_access.roles` | snapshot (default realm composite + children) | Realm's `default-roles-<realm>` composite |
| `aud` | contains `minerals-frontend` | `minerals-frontend-audience` mapper on the `minerals-test` client |
| `iss` | `http://localhost:8081/realms/minerals` (CI) | Realm config |
| `azp` | `minerals-test` (CI password grant) | Token issuer |

The snapshot deliberately includes the realm-name-bearing
`default-roles-<realm>` composite. If a future Keycloak version
flattens composites differently or renames the default role, the
diff fires and forces an explicit review — exactly the alert the
mi-cl1 / mi-2eg6 class of bug needed and did not have.

## What the backend MUST NOT assume

Anything the snapshot does not include. In particular:

- The `user` realm role is present in a default-shaped token today
  because mi-rcox added it to the realm's default-roles composite.
  This is the FIRST layer of defense. The SECOND layer — and the one
  the backend programs against — is that `authzUser` (mi-2eg6)
  injects `user` implicitly for any authenticated caller regardless
  of JWT roles. Code that reads `realm_access.roles` directly to
  gate writes will re-introduce mi-2eg6 if a future TF change ever
  drops `user` from the composite. The snapshot-fed unit test pins
  the fallback behavior (asserts `authzUser` still produces `user`
  when the snapshot's `user` is removed from the input).
- Group memberships, custom attributes, and devops-* roles are
  fixture-only. Backend code that depends on them must either be
  gated behind a feature that only fixture users would exercise, or
  document an explicit `terraform/keycloak` requirement for the
  deployed realm.
- The `minerals-frontend` audience is present via a client-scoped
  mapper. A different client (or a Keycloak instance without the
  mapper) WILL issue tokens the backend rejects on `aud`. mi-cl1's
  staging incident was this exact mode.

## Updating the snapshot

If a Keycloak version bump or an intentional realm-config change
shifts the default role set, update the snapshot in the same PR as
the change, with the diff visible in review:

1. Run the smoke locally (or in CI) and copy the actual
   `realm_access.roles` it logs.
2. Replace the `roles` array in
   `internal/oidc/testdata/keycloak-default-token-claims.json`.
3. Verify the unit test (`go test ./internal/api/ -run
   TestAuthzUser_DefaultKeycloakShape`) still passes — i.e. the
   backend still grants the implicit `user` role for the new
   default shape.

A drift that is NOT visible in the same PR as the realm/version
change is the failure mode this whole machinery exists to prevent.
Do not silence the diff.
