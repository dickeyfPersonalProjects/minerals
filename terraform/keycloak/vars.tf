variable "env_domain" {
  description = "Environment domain (e.g. dev.minerals.example). Used to derive keycloak_url and frontend_url when not overridden."
  type        = string
}

variable "keycloak_url_override" {
  description = "Override Keycloak URL (e.g. http://localhost:8081 for local dev). Defaults to https://auth.{env_domain} if empty."
  type        = string
  default     = ""
}

variable "keycloak_admin_user" {
  description = "Master-realm admin username (used when no client credentials are provided)."
  type        = string
  default     = ""
}

variable "keycloak_admin_password" {
  description = "Master-realm admin password (used when no client credentials are provided)."
  type        = string
  default     = ""
  sensitive   = true
}

variable "keycloak_client_id" {
  description = "Client ID for terraform-provider-keycloak service-account auth. When set, used instead of username/password."
  type        = string
  default     = ""
}

variable "keycloak_client_secret" {
  description = "Client secret for terraform-provider-keycloak service-account auth."
  type        = string
  default     = ""
  sensitive   = true
}

variable "realm_name" {
  description = "Keycloak realm name."
  type        = string
  default     = "minerals"
}

variable "realm_display_name" {
  description = "Display name shown on the Keycloak login page"
  type        = string
  default     = "Minerals"
}

variable "registration_allowed" {
  description = "Whether self-registration is permitted on the realm login page."
  type        = bool
  default     = false
}

# SMTP -----------------------------------------------------------------------

variable "smtp_host" {
  description = "SMTP host for realm email (verification, password reset). Empty disables SMTP."
  type        = string
  default     = ""
}

variable "smtp_port" {
  description = "SMTP port."
  type        = string
  default     = "587"
}

variable "smtp_from" {
  description = "SMTP From address."
  type        = string
  default     = ""
}

variable "smtp_from_display_name" {
  description = "SMTP From display name."
  type        = string
  default     = "Minerals"
}

variable "smtp_user" {
  description = "SMTP auth username. Empty disables SMTP auth."
  type        = string
  default     = ""
}

variable "smtp_password" {
  description = "SMTP auth password."
  type        = string
  default     = ""
  sensitive   = true
}

variable "smtp_starttls" {
  description = "Enable STARTTLS."
  type        = bool
  default     = true
}

variable "smtp_ssl" {
  description = "Enable SSL (implicit TLS)."
  type        = bool
  default     = false
}

# OIDC IdPs ------------------------------------------------------------------

variable "google_client_id" {
  description = "Google OIDC client ID. Empty disables the Google IdP."
  type        = string
  default     = ""
}

variable "google_client_secret" {
  description = "Google OIDC client secret."
  type        = string
  default     = ""
  sensitive   = true
}

variable "github_client_id" {
  description = "GitHub OAuth client ID. Empty disables the GitHub IdP."
  type        = string
  default     = ""
}

variable "github_client_secret" {
  description = "GitHub OAuth client secret."
  type        = string
  default     = ""
  sensitive   = true
}

# Test environment ----------------------------------------------------------

variable "test_environment" {
  description = "Whether this is a test environment. Enables test-only clients."
  type        = bool
  default     = false
}

# Extra client URIs ---------------------------------------------------------

variable "additional_redirect_uris" {
  description = "Extra redirect URIs to add to the minerals-frontend client (e.g. preview deploy URLs)."
  type        = list(string)
  default     = []
}

variable "additional_web_origins" {
  description = "Extra web origins to add to the minerals-frontend client."
  type        = list(string)
  default     = []
}
