resource "keycloak_oidc_google_identity_provider" "google" {
  count = var.google_client_id != "" ? 1 : 0

  realm         = keycloak_realm.minerals.id
  client_id     = var.google_client_id
  client_secret = var.google_client_secret

  trust_email                = true
  sync_mode                  = "IMPORT"
  hosted_domain              = ""
  request_refresh_token      = false
  default_scopes             = "openid profile email"
  accepts_prompt_none_forward_from_client = false
}

resource "keycloak_oidc_identity_provider" "github" {
  count = var.github_client_id != "" ? 1 : 0

  realm             = keycloak_realm.minerals.id
  alias             = "github"
  display_name      = "GitHub"
  provider_id       = "github"
  client_id         = var.github_client_id
  client_secret     = var.github_client_secret
  authorization_url = "https://github.com/login/oauth/authorize"
  token_url         = "https://github.com/login/oauth/access_token"

  default_scopes = "user:email"
  trust_email    = true
  sync_mode      = "IMPORT"
}
