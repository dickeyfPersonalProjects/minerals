# Privacy Policy

**Effective date:** 2026-05-24
**Last updated:** 2026-05-24

This Privacy Policy explains what personal data the Minerals application ("the Service", available at cabinet.rocks) collects, why, how it is stored, and the rights you have over it.

The Service is operated by Francois Dickey ("we", "us"), a personal/non-commercial project based in Quebec, Canada. This Policy is intended to comply with both the EU **General Data Protection Regulation (GDPR)** and Quebec's **Law 25** (the *Act respecting the protection of personal information in the private sector*). If you have any questions, contact us at **privacy@cabinet.rocks**.

## 1. Who is responsible for your data (Privacy Officer)

The person in charge of the protection of personal information (the "Privacy Officer", and the data controller for GDPR purposes) is:

Francois Dickey — Privacy Officer
privacy@cabinet.rocks
Quebec, Canada

You can contact the Privacy Officer at the address above for any question, access request, or complaint regarding your personal information.

## 2. What data we collect

We collect only what is needed to provide the Service:

| Data | Source | Why |
|------|--------|-----|
| Email address | Provided at registration (via our identity provider, Keycloak) | Account identity, login, security notifications |
| Display name | Provided by you at registration | To identify your account and attribute your content |
| Mineral/specimen collection data | Created by you in the app (names, descriptions, localities, collectors, catalog numbers, prices, acquisition details, journal entries) | This is the core function of the Service — cataloguing your collection |
| Uploaded images | Uploaded by you | To display photos of your specimens |
| Technical/session data | Generated automatically (session cookie, server logs, IP address) | To keep you logged in securely and to operate and protect the Service |

We do **not** collect special-category (sensitive) personal data, and we do not ask for it. Please do not enter sensitive personal data into free-text fields.

### Images and metadata

When you upload a photo, we strip GPS location and most embedded metadata by default before storing it, keeping only basic photographic metadata. You may optionally opt in to preserving full metadata (e.g. for provenance), in which case any GPS or other metadata you choose to keep is stored as part of the image.

## 3. Why we use your data and our lawful basis

We use your data only for the purposes described in Section 2 — operating your account and providing the cataloguing Service. We do not use it for advertising or profiling, and we do not sell it.

Our lawful bases (GDPR) / grounds (Law 25) are:

- **Performance of a contract / provision of the Service** — to operate your account and store the collection data you create.
- **Consent** — your consent under this Policy is requested separately from any other information, in clear and simple terms, and is given for the specific purposes above. You may withdraw it at any time by deleting your account (see Section 9).
- **Legitimate interests** — to secure the Service, prevent abuse, and maintain backups.

## 4. Cookies

The Service uses a single, strictly-necessary cookie: a first-party, HttpOnly session cookie used solely to keep you authenticated. It contains no tracking or advertising data and is not shared with third parties. Under the GDPR/ePrivacy rules, strictly-necessary cookies do not require consent, so the Service does not display a cookie banner. We use no analytics, advertising, or third-party tracking cookies.

## 5. Privacy by default

In line with Law 25 (s. 9.1) and GDPR data-protection-by-default principles, the Service applies the highest level of privacy by default: all specimens and their fields are **private** when created. Your content becomes visible to others only if you actively change its visibility to "unlisted" or "public". We do not require you to lower your privacy settings to use the Service.

## 6. No automated decision-making

The Service does not make any decision about you based exclusively on automated processing of your personal information, and does not perform profiling that produces legal or similarly significant effects.

## 7. Where your data is stored, who processes it, and transfers outside Quebec

The Service is self-hosted. Your data is stored in a PostgreSQL database (collection data) and a MinIO object store (uploaded images), running on infrastructure controlled by the operator.

We rely on the following third parties ("processors / sub-processors"):

| Provider | Role | Data involved |
|----------|------|---------------|
| Keycloak (self-hosted identity provider) | Authentication | Email, display name, credentials |
| Cloudflare | Network proxy / CDN / DDoS protection in front of the Service | IP address and request metadata pass through Cloudflare |
| Backblaze B2 | Encrypted off-site backups | A backup copy of database + object storage may be stored here |
| Mindat (mindat.org API) | Mineral species reference lookups | We send mineral-name/species queries you make for autocomplete. We do **not** send your personal data to Mindat. |

**Transfers outside Quebec / the EEA.** Some of these providers, and our hosting or backups, may store or process your data outside Quebec, including outside Canada and the EEA. Before relying on such a provider, we assess whether the data receives protection equivalent to that required under Law 25 (a privacy impact assessment) and, for GDPR transfers, we rely on appropriate safeguards such as the provider's standard contractual clauses. Data sent to backups is encrypted.

## 8. How long we keep your data, and destruction

We keep your account and collection data for as long as your account exists. When the purpose for which information was collected is fulfilled — including when you delete your account (see Section 9) — your personal data and collection content are deleted or anonymized in the live system. Residual copies may persist in encrypted backups for up to **30 days** before being overwritten in the normal backup-rotation cycle.

Server logs containing IP addresses are retained for a short period (**14 days**) for security and debugging, then rotated out.

## 9. Your rights

Under the GDPR and Law 25 you have the right to:

- **Access** — obtain a copy of your data. You can export your collection and images at any time using the in-app Export feature.
- **Rectification** — correct inaccurate or incomplete data by editing it in the app.
- **Erasure ("right to be forgotten")** — delete your account and associated data (see Section 10).
- **De-indexing / cessation of dissemination** — request that we stop disseminating your personal information, or de-index content, where the law allows (e.g. content you previously made public).
- **Portability** — receive the computerized personal information you provided, in a structured, commonly-used technological format (provided by the Export feature).
- **Withdrawal of consent / objection / restriction** — withdraw consent or object, by ceasing use and deleting your account.

To exercise any right not covered by the in-app tools, contact the Privacy Officer at **privacy@cabinet.rocks**. We respond within the timeframes required by law (under Law 25, normally within 30 days).

**Complaints.** If you are not satisfied with our response, you may lodge a complaint with the **Commission d'accès à l'information du Québec (CAI)** — the Quebec supervisory authority — or, if you are in the EU/EEA, with your local data protection authority.

## 10. Account deletion

You may request deletion of your account and associated personal data at any time by contacting privacy@cabinet.rocks. (A self-service "delete my account" option in the app is planned.) Deletion removes your user record, specimens, collectors, photos (including stored image objects), and journal entries from the live system. Backup copies are purged on the normal backup-rotation cycle (see Section 8).

## 11. Confidentiality incidents (data breaches)

If a confidentiality incident (a breach involving your personal information) occurs and presents a risk of serious injury, we will:

- notify the **Commission d'accès à l'information du Québec (CAI)** and the affected individuals as required by Law 25 (and, where the GDPR applies, the relevant supervisory authority within 72 hours);
- take reasonable measures to reduce the risk of injury and prevent recurrence; and
- record the incident in a register of confidentiality incidents, which we maintain as required by law.

## 12. Security

We use HTTPS for all traffic, store no third-party tracking data, keep authentication tokens in HttpOnly cookies, and apply per-user access controls so that private content is only visible to its owner. No system is perfectly secure, but we take reasonable measures to protect your data.

## 13. Children

The Service is not directed at children and we do not knowingly collect data from anyone under 14 (the age below which the consent of a parent or guardian is required under Quebec's Law 25).

## 14. Changes to this Policy

We may update this Policy. Material changes will be reflected by updating the "Last updated" date and, where appropriate, notifying account holders. Continued use after a change constitutes acceptance.

## 15. Contact

Privacy Officer: **privacy@cabinet.rocks**.
