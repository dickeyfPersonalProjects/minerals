resource "keycloak_realm" "minerals" {
  realm        = var.realm_name
  enabled      = true
  display_name = "Minerals"

  registration_allowed           = var.registration_allowed
  registration_email_as_username = true
  reset_password_allowed         = true
  remember_me                    = true
  verify_email                   = local.smtp_enabled
  login_with_email_allowed       = true
  duplicate_emails_allowed       = false

  # Token lifespans
  access_token_lifespan                  = "1h"
  access_token_lifespan_for_implicit_flow = "15m"
  sso_session_idle_timeout               = "30m"
  sso_session_max_lifespan               = "10h"
  sso_session_idle_timeout_remember_me   = "0s"
  sso_session_max_lifespan_remember_me   = "0s"
  offline_session_idle_timeout           = "720h"
  offline_session_max_lifespan_enabled   = true
  offline_session_max_lifespan           = "1440h"

  # Default browser/login behavior
  login_theme = "keycloak"

  password_policy = "length(12) and notUsername and notEmail and passwordHistory(3)"

  dynamic "smtp_server" {
    for_each = local.smtp_enabled ? [1] : []
    content {
      host              = var.smtp_host
      port              = var.smtp_port
      from              = var.smtp_from
      from_display_name = var.smtp_from_display_name
      reply_to          = var.smtp_from
      starttls          = var.smtp_starttls
      ssl               = var.smtp_ssl

      dynamic "auth" {
        for_each = var.smtp_user != "" ? [1] : []
        content {
          username = var.smtp_user
          password = var.smtp_password
        }
      }
    }
  }
}
