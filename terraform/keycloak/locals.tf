locals {
  keycloak_url = var.keycloak_url_override != "" ? var.keycloak_url_override : "https://auth.${var.env_domain}"
  frontend_url = "https://www.${var.env_domain}"

  smtp_enabled = var.smtp_host != ""
}
