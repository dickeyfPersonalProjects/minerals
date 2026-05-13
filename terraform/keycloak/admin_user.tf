# Realm admin user with an auto-generated password. The password is
# emitted as a sensitive output so it can be retrieved once and stored
# in a secret manager.
resource "random_password" "admin" {
  length  = 24
  special = true
  # Avoid characters that need escaping in shells / URLs.
  override_special = "!@#%^*-_=+"
}

resource "keycloak_user" "admin" {
  realm_id   = keycloak_realm.minerals.id
  username   = "admin"
  email      = "admin@${var.env_domain}"
  enabled    = true
  email_verified = true

  first_name = "Realm"
  last_name  = "Admin"

  initial_password {
    value     = random_password.admin.result
    temporary = false
  }
}

# Grant the realm-management realm-admin role so this user can manage
# everything inside the minerals realm without master-realm access.
data "keycloak_role" "realm_admin" {
  realm_id  = keycloak_realm.minerals.id
  client_id = data.keycloak_openid_client.realm_management.id
  name      = "realm-admin"
}

resource "keycloak_user_roles" "admin" {
  realm_id = keycloak_realm.minerals.id
  user_id  = keycloak_user.admin.id

  role_ids = [
    data.keycloak_role.realm_admin.id,
    keycloak_role.user.id,
    keycloak_role.admin.id,
  ]
}
