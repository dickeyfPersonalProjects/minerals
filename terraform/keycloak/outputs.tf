output "keycloak_url" {
  description = "Base URL of the Keycloak server."
  value       = local.keycloak_url
}

output "frontend_url" {
  description = "Base URL of the Minerals frontend."
  value       = local.frontend_url
}

output "realm" {
  description = "Realm name."
  value       = keycloak_realm.minerals.realm
}

output "realm_issuer" {
  description = "OIDC issuer URL for the realm."
  value       = "${local.keycloak_url}/realms/${keycloak_realm.minerals.realm}"
}

output "oidc_authorization_endpoint" {
  description = "OIDC authorization endpoint."
  value       = "${local.keycloak_url}/realms/${keycloak_realm.minerals.realm}/protocol/openid-connect/auth"
}

output "oidc_token_endpoint" {
  description = "OIDC token endpoint."
  value       = "${local.keycloak_url}/realms/${keycloak_realm.minerals.realm}/protocol/openid-connect/token"
}

output "oidc_userinfo_endpoint" {
  description = "OIDC userinfo endpoint."
  value       = "${local.keycloak_url}/realms/${keycloak_realm.minerals.realm}/protocol/openid-connect/userinfo"
}

output "oidc_jwks_uri" {
  description = "OIDC JWKS URI."
  value       = "${local.keycloak_url}/realms/${keycloak_realm.minerals.realm}/protocol/openid-connect/certs"
}

output "oidc_discovery_url" {
  description = "OIDC discovery document URL."
  value       = "${local.keycloak_url}/realms/${keycloak_realm.minerals.realm}/.well-known/openid-configuration"
}

output "frontend_client_id" {
  description = "Public client ID used by the SPA."
  value       = keycloak_openid_client.frontend.client_id
}

output "backend_client_id" {
  description = "Confidential client ID used by the Go backend."
  value       = keycloak_openid_client.backend.client_id
}

output "backend_client_secret" {
  description = "Confidential client secret for the Go backend."
  value       = keycloak_openid_client.backend.client_secret
  sensitive   = true
}

output "test_client_id" {
  description = "Test client ID (only set when test_environment = true)."
  value       = var.test_environment ? keycloak_openid_client.test_password_grant[0].client_id : ""
}

output "admin_username" {
  description = "Realm admin username."
  value       = keycloak_user.admin.username
}

output "admin_password" {
  description = "Auto-generated realm admin password."
  value       = random_password.admin.result
  sensitive   = true
}
