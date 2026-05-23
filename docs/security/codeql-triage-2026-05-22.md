# CodeQL Alert Triage — 2026-05-22

Companion to `static-audit-2026-05-22.md` (mi-l1eg). Records triage of the
open GitHub code-scanning (CodeQL) alerts as part of the V3 static audit.

## go/cookie-secure-not-set — FALSE POSITIVE (suppressed)

- **Alert:** `go/cookie-secure-not-set` [medium] — "Cookie does not set
  Secure attribute to true."
- **Location:** `internal/auth/bff/cookie.go` — `ClearSessionCookie`
  (the partner `SetSessionCookie` raises the same query).
- **Verdict:** false positive.

**Why.** Both `SetSessionCookie` and `ClearSessionCookie` set
`Secure: cfg.Secure` — an env-driven value that is `true` in
prod/staging and `false` only in local dev (HTTP localhost, where a
hardcoded `Secure: true` would break login). The callsite, not the
helper, owns the security-relevant choice. Verified: neither function
uses a literal `false`; both take the same `CookieConfig.Secure`. There
was already a `//nolint:gosec` on both for the equivalent gosec G124
rule with the same justification — CodeQL is a separate analyzer that
does not honour the gosec nolint, so it re-raised the alert.

**Action taken.** Added an inline CodeQL suppression
`// codeql[go/cookie-secure-not-set]` (alongside the existing
`//nolint:gosec`) on both `SetSessionCookie` and `ClearSessionCookie`.
Inline suppression is preferred over an API dismissal: it lives with the
code and survives re-scans. No behaviour change.

## CodeQL baseline health

The earlier "configurations not found" NEUTRAL on PRs was main lacking a
CodeQL baseline when those PRs opened, not a misconfiguration. The
baseline on main is now healthy (analyses completed for all three
languages on 2026-05-22). CodeQL should produce real pass/fail on PRs
going forward — verify the next PR's code-scanning check reports a
concrete result rather than NEUTRAL.

This was the only open code-scanning alert at audit time.
