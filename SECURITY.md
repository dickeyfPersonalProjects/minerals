# Security Policy

Thank you for taking the time to help keep this project secure. This
document describes how to report a vulnerability and what to expect
when you do.

## Project status and scope

Minerals is a personal project maintained by a single developer in
their spare time. It is not commercially supported. Response times
reflect this reality (see below).

Security reports are welcome and genuinely appreciated, but please
calibrate expectations accordingly.

## Supported versions

Only the latest `main` branch and the most recent tagged release
receive security fixes. Older versions are not supported.

| Version | Supported |
| ------- | --------- |
| `main` (HEAD) | Yes |
| Latest release | Yes |
| Older releases | No — please upgrade |

## What counts as a vulnerability

In scope:

- Authentication bypass or session management flaws
- Authorization bypass (one user accessing another user's data)
- SQL injection, path traversal, server-side request forgery
- Stored or reflected XSS that affects other users
- CSRF on state-changing endpoints
- Insecure direct object references on specimens, photos, or journal entries
- Exposure of secrets, credentials, or environment variables
- Container escape or privilege escalation in the deployment manifests
- Supply-chain issues in dependencies (please report directly to the
  upstream project first; mention here if it affects this app
  specifically)

Out of scope:

- Issues that require physical access to the host
- Denial-of-service via raw resource exhaustion that any HTTP service
  would suffer from equally (rate limiting is enforced; please report
  the bypass, not the symptom)
- Issues in the optional development setup (`docker compose up` on
  localhost with default credentials) — that environment is explicitly
  not hardened
- Social engineering, phishing, or attacks targeting the maintainer
- Self-XSS or other attacks requiring the victim to paste hostile
  content into their own browser console
- Missing security headers that don't lead to a concrete exploit (e.g.,
  "Server header reveals version" without a corresponding vulnerable
  version)
- Vulnerabilities in third-party services this app integrates with
  (report those to the third party)

## How to report

**Please do not file public GitHub issues for security reports.**

Preferred channel: GitHub's private vulnerability reporting.
Navigate to the repository's Security tab and click "Report a
vulnerability." This creates a private discussion only visible to the
maintainer.

Alternative: email [your-email@example.com] with the subject line
beginning `[security]`. PGP encryption is welcome but not required;
the key is published at [link if you have one, or omit this line].

Please include in your report:

- A description of the vulnerability
- Steps to reproduce, including any relevant configuration
- The version or commit hash you tested against
- Your assessment of the impact and affected user populations
- Whether you've disclosed this to anyone else
- How you would like to be credited (or that you prefer to remain
  anonymous)

## What to expect

- **Acknowledgment:** within 7 days of report. If you don't hear back
  by then, please assume the message was lost and re-send.
- **Initial assessment:** within 14 days, you should receive either
  a request for clarification, an acknowledgment that the issue is
  valid and being worked on, or an explanation of why it's considered
  out of scope.
- **Fix timeline:** depends on severity. Critical issues (active
  exploitation, mass data exposure) will be prioritized over all
  other work. Lower-severity issues will be scheduled into the normal
  development cycle. Realistic ranges:
  - Critical: days to a week
  - High: weeks
  - Medium: weeks to a month
  - Low: best effort, may be deferred indefinitely with disclosure

- **Disclosure:** coordinated disclosure is preferred. Once a fix is
  released, the report will be acknowledged in the changelog and (if
  you wish) you will be credited by name. Please do not publish
  technical details of an unpatched vulnerability.

## No bug bounty

This project does not offer monetary rewards for security reports.
Credit in the changelog and the satisfaction of helping the
community is the extent of recognition available.

## Hall of fame

Researchers who have reported valid security issues are listed
below.

<!-- Format: - YYYY-MM-DD: Name (link if desired) — brief description
     of the issue, e.g., "authorization bypass on photo deletion" -->

(none yet)
