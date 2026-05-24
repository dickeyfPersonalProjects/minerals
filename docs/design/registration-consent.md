# Registration consent + legal pages (mi-97kr)

Decision record for how the Terms of Service and Privacy Policy are
hosted, surfaced, and consented to. Part of the V3 launch
prerequisite for GDPR + Quebec Law 25 compliance.

## What ships

1. **Legal text** — the operator-approved Privacy Policy and Terms of
   Service live as markdown under `internal/legal/content/` and are
   embedded into the binary (`//go:embed`). These files are the single
   source of truth; updating the legal text is a content edit there,
   not a code change elsewhere.

2. **Public API** — `GET /api/v1/legal/{slug}` (`slug` ∈ {`privacy`,
   `terms`}) renders the markdown to sanitized HTML via the
   CONTRACT.md §17 pipeline (goldmark → bluemonday) and returns
   `{slug, title, html}`. The endpoint is **public** (no auth
   middleware, like `/healthz` / `/readyz`) because the pages must be
   reachable before login. The HTML is rendered once at startup and
   cached.

3. **SPA pages** — `/privacy` and `/terms` hash routes render the
   fetched HTML via `{@html}` (the same trusted sink SpecimenDetail
   uses for journal `body_html`, justified by the §17 server-side
   sanitization). Linked from the global footer (visible pre-login)
   and surfaced at the registration entry point.

## Consent gate: Keycloak required action (chosen) vs app-level gate

**Chosen: Keycloak's built-in `TERMS_AND_CONDITIONS` required action**,
enabled as a *default action* via terraform
(`terraform/keycloak/required_actions.tf`, toggled by
`registration_consent_enabled`, default `true`).

Why Keycloak over an app-level gate:

- Registration, email verification, and the OAuth flow already live
  entirely inside Keycloak (the SPA only links to `/auth/register`,
  which 302s the browser to Keycloak's hosted form). Putting consent
  there keeps it in the one flow that owns account creation — there is
  no second, app-side enforcement surface to keep in sync, and no way
  for a user to obtain a session without having passed it.
- An app-level gate would have to re-implement "has this user
  accepted the current terms version" state and block every protected
  route until satisfied — duplicating the first-login profile gate
  machinery for no benefit, and leaving a window where a Keycloak
  session exists but app consent does not.

The `TERMS_AND_CONDITIONS` action is a built-in Keycloak required
action (disabled by default); the terraform resource enables it and
marks it `default_action = true` so it is attached to every
newly-registered user.

### Open follow-up (operator / theme)

The consent **text** Keycloak displays comes from the login theme's
`terms.ftl`. The default `keycloak` theme renders placeholder copy. To
show the real policy, ship a theme override of `terms.ftl` that links
to `/terms` + `/privacy` (or inlines the approved text). This is theme
work, tracked separately from this code change. Until then, the
required action still enforces an explicit accept/decline step, and
the SPA footer + register-button microcopy carry the real links.

## Out of scope (separate beads)

- French translation of the legal docs (mi-m26w) and full UI i18n
  (mi-w6xc). The pages are English-only for now but are not hardcoded
  in a way that fights future i18n (content is data, not inline JSX).
- Automated account deletion / right-to-erasure (mi-nwg5). The privacy
  policy documents the manual contact path in the interim.
