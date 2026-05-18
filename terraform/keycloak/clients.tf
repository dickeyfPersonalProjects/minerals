# Confidential OAuth client for the V2 BFF auth flow (mi-1d5i). The
# browser does not speak OAuth — it hits `GET /auth/login` on the Go
# backend, which performs a server-side authorization-code exchange
# against Keycloak using `client_secret`. The SPA only ever sees an
# opaque session cookie. See `docs/design/auth-bff.md`.
resource "keycloak_openid_client" "frontend" {
  realm_id  = keycloak_realm.minerals.id
  client_id = "minerals-frontend"
  name      = "Minerals Frontend"
  enabled   = true

  # Confidential: the backend holds the secret; the browser never does.
  # The secret reaches the cluster as the `minerals-oidc-secret`
  # SealedSecret in the per-env GitOps overlay — see
  # `docs/deploy/secrets.md`.
  access_type = "CONFIDENTIAL"

  standard_flow_enabled        = true
  direct_access_grants_enabled = false
  implicit_flow_enabled        = false

  # No PKCE: a confidential client authenticates to the token endpoint
  # with `client_secret`, which is what PKCE substitutes for in the
  # public-client case. Setting `pkce_code_challenge_method` here would
  # be a no-op.

  # Backend-served callback. The URL string is unchanged from the
  # pre-BFF (PKCE-in-SPA) design — same hostname, same path — but
  # `/auth/callback` is now a backend route, not a SPA route. Do NOT
  # 'fix' this back to a SPA wildcard.
  valid_redirect_uris = concat(
    [
      "${local.frontend_url}/auth/callback",
    ],
    var.additional_redirect_uris,
  )

  # web_origins stays scoped to the public frontend origin. The BFF
  # design removes the SPA's cross-origin POSTs to Keycloak, but leaving
  # the registered origin costs nothing and supports any future
  # SPA-to-Keycloak interactions (e.g. silent-renewal iframe attempts)
  # without a Terraform change.
  web_origins = concat(
    [local.frontend_url],
    var.additional_web_origins,
  )

  root_url = local.frontend_url
  base_url = "/"
}

# Audience mapper for the SPA client. Keycloak access tokens otherwise
# carry only `aud: account` — the requesting client lands in `azp`, not
# `aud`. The Go backend is a pure resource server that checks `aud`
# contains OIDC_CLIENT_ID (minerals-frontend), so without this mapper
# every real SPA token is rejected on the audience check. Adds
# `minerals-frontend` to the access-token `aud`.
resource "keycloak_openid_audience_protocol_mapper" "frontend_audience" {
  realm_id  = keycloak_realm.minerals.id
  client_id = keycloak_openid_client.frontend.id
  name      = "minerals-frontend-audience"

  included_client_audience = keycloak_openid_client.frontend.client_id

  add_to_id_token     = false
  add_to_access_token = true
}

# Confidential backend client with a service account, used by the Go
# backend for token introspection and any server-side keycloak admin
# operations.
resource "keycloak_openid_client" "backend" {
  realm_id  = keycloak_realm.minerals.id
  client_id = "minerals-backend"
  name      = "Minerals Backend"
  enabled   = true

  access_type = "CONFIDENTIAL"

  standard_flow_enabled        = false
  direct_access_grants_enabled = false
  implicit_flow_enabled        = false
  service_accounts_enabled     = true
}

# Grant the backend service account the realm-management view-users role
# so it can resolve user identities during token introspection.
data "keycloak_openid_client" "realm_management" {
  realm_id  = keycloak_realm.minerals.id
  client_id = "realm-management"
}

data "keycloak_role" "view_users" {
  realm_id  = keycloak_realm.minerals.id
  client_id = data.keycloak_openid_client.realm_management.id
  name      = "view-users"
}

resource "keycloak_openid_client_service_account_role" "backend_view_users" {
  realm_id                = keycloak_realm.minerals.id
  service_account_user_id = keycloak_openid_client.backend.service_account_user_id
  client_id               = data.keycloak_openid_client.realm_management.id
  role                    = data.keycloak_role.view_users.name
}

# Test-only password-grant client (mi-6oa).
#
# Scope: BACKEND-ONLY. This client exists to let CI mint a real
# Keycloak-issued JWT via the password grant so the curl-based half of
# the keycloak-smoke job can answer one narrow question — "given a
# syntactically valid Keycloak JWT, does the Go middleware accept it?"
# (JWKS discovery, audience check, issuer match). It is NOT a model of
# any user-facing flow.
#
# Real users hit `minerals-frontend` (PKCE) above. The user-facing path
# is covered end-to-end by the Playwright PKCE smoke (mi-dwx); this
# client must NEVER grow new assertions intended to cover that path.
#
# Created only when `test_environment = true` (CI sets
# TF_VAR_test_environment=true via the keycloak-smoke job). Never
# provisioned in staging/prod.
resource "keycloak_openid_client" "test_password_grant" {
  count = var.test_environment ? 1 : 0

  realm_id  = keycloak_realm.minerals.id
  client_id = "minerals-test"
  name      = "Minerals Test (password grant, backend-only)"
  enabled   = true

  access_type = "PUBLIC"

  standard_flow_enabled        = false
  direct_access_grants_enabled = true
  implicit_flow_enabled        = false
}

# The test client issues tokens for the same resource server as the SPA,
# so its access tokens must also carry `minerals-frontend` in `aud` —
# the backend checks `aud`, not `azp`. Without this the CI auth smoke
# test (mi-ivk) gets a token Keycloak considers valid but the app
# rejects on the audience check.
resource "keycloak_openid_audience_protocol_mapper" "test_audience" {
  count = var.test_environment ? 1 : 0

  realm_id  = keycloak_realm.minerals.id
  client_id = keycloak_openid_client.test_password_grant[0].id
  name      = "minerals-frontend-audience"

  included_client_audience = keycloak_openid_client.frontend.client_id

  add_to_id_token     = false
  add_to_access_token = true
}
