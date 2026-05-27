# Moderation & Abuse Handling

> Operator runbook + policy for publicly visible user-generated content.
> Bead: `mi-b2q0` (V3 launch prerequisite). Related: `mi-jjzc` (console
> takedown hooks), `mi-3gxz` (Keycloak account disable), `mi-tnru` (rate
> limiting), CONTRACT.md §13 (visibility model).

## Why this exists

Once registration is open, any user can publish content — specimen
names, descriptions, journal entries, and uploaded photos — at `public`
or `unlisted` visibility (CONTRACT.md §13). `public`/`unlisted` content
is served to **anonymous** viewers, so it is genuinely internet-facing.
That surface needs a defensible abuse-handling story before launch.

This is a **small personal-project SaaS run by a solo operator**, not a
trust-and-safety org. The baseline below is sized accordingly.

## Policy: post-moderation

We use **post-moderation**, not pre-moderation:

- Content publishes immediately. There is no review queue gating
  publication.
- The operator **reacts to reports** and takes content down after the
  fact.

Pre-moderation (review-before-publish) is explicitly rejected: it does
not scale to solo operation and adds latency to every publish for a
threat that is rare at this scale.

### Defense layers already in place

| Layer | Mechanism | Bead |
|---|---|---|
| Spam volume | Per-account + per-IP token-bucket rate limiting (429s) | `mi-tnru` ✅ |
| Visibility default | New specimens default to `private` | CONTRACT §13 |
| Authz | Casbin per-resource enforcement; anonymous can only read `public`/`unlisted` | `mi-aw3b` ✅ |
| Operator power | `admin` role holds the Casbin `*:*:*` superset → can edit/delete **any** user's content | CONTRACT §13 |

## What ships in this baseline (`mi-b2q0`)

1. **Public report affordance.** A "Report" button on every specimen
   detail page (`frontend/src/routes/SpecimenDetail.svelte`) opens a
   modal (reason + optional details) that POSTs to
   `POST /api/v1/specimens/{id}/report`. Anonymous callers are accepted.
2. **Report delivery to the operator.** The report endpoint emits a
   structured `WARN` log event (`event=moderation.report`) — see
   [How reports reach you](#how-reports-reach-you). No moderation
   queue/dashboard is built (out of scope at launch scale).
3. **Operator takedown action.** `POST /api/v1/admin/specimens/{id}/takedown`
   forces a specimen's visibility to `private`, with an audit log event
   (`event=moderation.takedown`). Gated on `specimens:edit` for the
   target, which only the `admin` superset satisfies for content it does
   not own.

### Deferred (tracked as follow-up beads)

- **Console takedown UI + photo/journal removal** — `mi-jjzc`. The
  baseline takedown only force-privates a *specimen*. To remove an
  individual abusive **photo** or **journal entry** today, the operator
  uses the existing owner-style endpoints (the `admin` superset lets an
  admin call them on any user's content):
  - `DELETE /api/v1/photos/{id}`
  - `DELETE /api/v1/journal/{id}`
- **Account disable / suspension** — `mi-3gxz`. Disabling a Keycloak
  account requires a Keycloak **admin** REST client, which is not yet
  wired. Until then, disable accounts directly in the Keycloak admin
  console (see [Disable an account](#disable-an-account)).
- **Automated content scanning** (image classification, text toxicity)
  — out of scope for launch; revisit if report volume warrants it.

## Operator runbook

### How reports reach you

Reports are written to the application log as a structured event:

```
level=WARN msg="moderation report received" event=moderation.report
  report_id=<uuid> specimen_id=<uuid> author_id=<uuid>
  visibility=public reason=abuse reporter=<user-uuid|anonymous>
  details="<reporter's free text>"
```

Surface these via your log alerting (the production logging/monitoring
stack, `mi-vp0w`). A simple alert rule on `event=moderation.report`
(or `msg="moderation report received"`) is sufficient at launch scale.
The `report_id` correlates the alert to a specific report; `details`
carries the reporter's context so you can triage without a second
lookup.

> **Note:** there is no persistent reports table. If/when report volume
> justifies a reviewable history, add one (and a console queue) — that's
> the `mi-jjzc` console work, not this baseline.

### Respond to a report

1. **Review.** Open the reported specimen: `/specimens/<specimen_id>`.
   Judge it against the usage policy (abusive/illegal/spam/etc.).
2. **Take the content down** (if it violates policy):
   - **Whole specimen** → force it private:
     ```
     POST /api/v1/admin/specimens/<specimen_id>/takedown
     { "reason": "policy violation: <short note>" }
     ```
     This flips visibility to `private` (removing it from all public and
     unlisted reach) and logs `event=moderation.takedown`. Idempotent.
   - **A single photo** → `DELETE /api/v1/photos/<photo_id>` (admin can
     delete any user's photo).
   - **A single journal entry** → `DELETE /api/v1/journal/<entry_id>`.
3. **Repeat / escalate** for a repeat offender → disable the account.
4. **Record** the action if it constitutes a confidentiality incident
   (Law 25 register — admin console `incident-register` surface, planned
   in `mi-agff`).

All admin actions are authenticated and authz-gated; takedowns and
reports are logged, giving you an audit trail in the log stream.

### Disable an account

Until `mi-3gxz` wires a Keycloak admin client into the app:

1. Open the **Keycloak admin console** (see `docs/deploy/keycloak.md`).
2. Realm → **Users** → find the user (match on email/username) →
   **Enabled: Off**. This blocks new logins and token issuance.
3. The user's existing `public`/`unlisted` content stays visible until
   you take it down — Keycloak disable does not cascade to app content.
   Force-private their specimens (step 2 above) for each piece of
   offending content. (App-side "hide all content on ban" is a possible
   future enhancement, tracked with `mi-3gxz`.)
4. For a GDPR/Law 25 **erasure** request (distinct from a ban), use the
   account deletion path (`UserStatusDeleted` tombstone), not a Keycloak
   disable.

## Threat → mitigation summary

| Threat | Mitigation |
|---|---|
| Illegal/abusive image upload made public | Report → admin takedown (force-private) or `DELETE /photos/{id}`; disable repeat-offender account |
| Abusive text (name/description/journal) | Same: report → takedown / delete journal entry |
| Spam accounts / spam content | Rate limiting (`mi-tnru`) blunts volume; report → takedown; disable account |
| Public page as an attack vector | Content is force-privateable instantly; anonymous reach removed on takedown |

## Endpoints reference

| Endpoint | Auth | Purpose |
|---|---|---|
| `POST /api/v1/specimens/{id}/report` | Public (anonymous OK) | File an abuse report. 404 if the specimen isn't visible to the caller (no leak). |
| `POST /api/v1/admin/specimens/{id}/takedown` | `admin` | Force specimen → `private`. Audit-logged. Idempotent. |
| `DELETE /api/v1/photos/{id}` | owner or `admin` | Remove a photo (admin can remove any). |
| `DELETE /api/v1/journal/{id}` | owner or `admin` | Remove a journal entry (admin can remove any). |
