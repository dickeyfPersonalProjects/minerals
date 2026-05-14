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
