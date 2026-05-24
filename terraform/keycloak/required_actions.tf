# Registration consent (mi-97kr).
#
# Keycloak ships a built-in "Terms and Conditions" required action
# (alias TERMS_AND_CONDITIONS) that forces the user to accept the
# terms before the account is usable. We enable it as a DEFAULT action
# so it is attached to every newly-registered user — this is the
# consent gate for ToS + Privacy Policy acceptance at signup, chosen
# over an app-level gate because it runs inside the same Keycloak flow
# that already owns registration + email verification (no second,
# app-side enforcement surface to keep in sync). See
# docs/design/registration-consent.md for the decision record.
#
# NOTE (operator): the consent TEXT shown by this action comes from
# the login theme's `terms.ftl`. The default `keycloak` theme renders
# placeholder text. To present the real ToS + Privacy Policy, ship a
# theme override of terms.ftl that links to /terms and /privacy (or
# inlines the approved copy). Tracked separately from this code change.
resource "keycloak_required_action" "terms_and_conditions" {
  count = var.registration_consent_enabled ? 1 : 0

  realm_id = keycloak_realm.minerals.id
  alias    = "TERMS_AND_CONDITIONS"
  name     = "Terms and Conditions"
  enabled  = true
  # default_action attaches the action to newly-created users, so a
  # self-registered user must accept before reaching the app.
  default_action = true
  priority       = 10
}
