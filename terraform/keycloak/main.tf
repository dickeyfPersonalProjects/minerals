terraform {
  required_version = ">= 1.5.0"

  required_providers {
    keycloak = {
      source  = "mrparkers/keycloak"
      version = "~> 4.4"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

# Dual-auth: prefer OIDC service-account credentials when supplied
# (terraform-friendly, no password in state), otherwise fall back to
# the master-realm admin username/password (bootstrap / local dev).
locals {
  use_oidc_auth = var.keycloak_client_id != ""
}

provider "keycloak" {
  url           = local.keycloak_url
  realm         = "master"
  client_id     = local.use_oidc_auth ? var.keycloak_client_id : "admin-cli"
  client_secret = local.use_oidc_auth ? var.keycloak_client_secret : null
  username      = local.use_oidc_auth ? null : var.keycloak_admin_user
  password      = local.use_oidc_auth ? null : var.keycloak_admin_password
}
