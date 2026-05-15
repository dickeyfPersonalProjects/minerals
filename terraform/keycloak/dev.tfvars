# Working tfvars for the local docker-compose Keycloak (mi-hye).
# Committed to the repo — no copy step, no edits required.
#
#   docker compose --profile keycloak up -d keycloak
#   cd terraform/keycloak && terraform init && terraform apply -var-file=dev.tfvars
#
# Mirrors the staging/prod realm shape minus SMTP, registration, and
# external IdPs (Google/GitHub). See terraform.tfvars.example for the
# full prod shape.

keycloak_url_override   = "http://localhost:8081"
keycloak_admin_user     = "admin"
keycloak_admin_password = "admin"

env_domain         = "localhost"
realm_name         = "minerals"
realm_display_name = "Minerals (dev)"

registration_allowed = false

# Wire the SPA dev origin (Vite default) into the public frontend
# client's redirect / web-origin lists. The default `https://www.localhost/*`
# entries derived from env_domain are harmless but unused.
additional_redirect_uris = ["http://localhost:5173/*"]
additional_web_origins   = ["http://localhost:5173"]

# Create the `minerals-test` public direct-access-grant client so local
# dev and the CI auth smoke test (mi-ivk) can obtain realm tokens for a
# test user via the password grant. Dev-only — never set in prod.
test_environment = true
