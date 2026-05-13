resource "keycloak_role" "admin" {
  realm_id    = keycloak_realm.minerals.id
  name        = "admin"
  description = "Full administrative access to the Minerals app."
}

resource "keycloak_role" "collector" {
  realm_id    = keycloak_realm.minerals.id
  name        = "collector"
  description = "Authenticated user who can manage their own mineral collection."
}

resource "keycloak_role" "viewer" {
  realm_id    = keycloak_realm.minerals.id
  name        = "viewer"
  description = "Read-only access to public collection data."
}
