# Public SPA client used by the minerals frontend (browser-based OIDC,
# PKCE, no client secret).
resource "keycloak_openid_client" "frontend" {
  realm_id  = keycloak_realm.minerals.id
  client_id = "minerals-frontend"
  name      = "Minerals Frontend"
  enabled   = true

  access_type = "PUBLIC"

  standard_flow_enabled = true
  direct_access_grants_enabled = false
  implicit_flow_enabled        = false

  pkce_code_challenge_method = "S256"

  valid_redirect_uris = concat(
    [
      "${local.frontend_url}/*",
    ],
    var.additional_redirect_uris,
  )

  web_origins = concat(
    [local.frontend_url],
    var.additional_web_origins,
  )

  root_url = local.frontend_url
  base_url = "/"
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

# Test-only clients (e.g. password-grant client for integration tests).
resource "keycloak_openid_client" "test_password_grant" {
  count = var.test_environment ? 1 : 0

  realm_id  = keycloak_realm.minerals.id
  client_id = "minerals-test"
  name      = "Minerals Test (password grant)"
  enabled   = true

  access_type = "PUBLIC"

  standard_flow_enabled        = false
  direct_access_grants_enabled = true
  implicit_flow_enabled        = false
}
