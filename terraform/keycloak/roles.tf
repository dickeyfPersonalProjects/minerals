resource "keycloak_role" "user" {
  realm_id    = keycloak_realm.minerals.id
  name        = "user"
  description = "Base role granted to every active minerals user. CONTRACT §13 v2 policies grant ownership permissions to this role."
}

resource "keycloak_default_roles" "minerals" {
  realm_id      = keycloak_realm.minerals.id
  default_roles = ["user", "offline_access", "uma_authorization"]

  depends_on = [keycloak_role.user]
}

resource "keycloak_role" "devops_viewer" {
  realm_id    = keycloak_realm.minerals.id
  name        = "devops-viewer"
  description = "Read-only access to operational tooling. devops-admin inherits this via Casbin (not Keycloak composite roles)."
}

resource "keycloak_role" "devops_admin" {
  realm_id    = keycloak_realm.minerals.id
  name        = "devops-admin"
  description = "Full operational access. Inherits devops-viewer via Casbin (not Keycloak composite roles)."
}
