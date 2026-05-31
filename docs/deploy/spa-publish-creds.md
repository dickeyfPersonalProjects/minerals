# SPA-publish credentials: MinIO → K8s → GitHub Actions (full gitops)

This is the credential pipeline that lets CI publish the decoupled SPA bundle
to the `minerals-web` object-storage bucket **without any human handling a
secret value**. A write-scoped MinIO key is provisioned in-cluster, lands in a
Kubernetes Secret, and the External Secrets Operator (ESO) pushes it out to the
minerals GitHub repo's Actions secrets — and re-pushes automatically when the
key rotates.

> **Scope (mi-1im1).** This document and its manifests cover the *credential*
> chain only. The CI publish workflow (uploading assets, content-types,
> no-cache `index.html`, manifest-based GC) and the *serving* side (making
> `minerals-web` public-read, routing `/` + `/assets` to it, flipping
> `WEB_SERVE_MODE=disabled`, retiring the nginx `web.yaml`) are separate
> follow-up beads.

---

## The chain

```
MinIO Tenant (in-cluster)
   │  web-publisher.yaml: idempotent `mc` Job
   │    • create write-only inline policy (PutObject/DeleteObject/ListBucket
   │      on minerals-web ONLY — no GetObject, not admin)
   │    • create a service account bound to that policy
   ▼
Secret  minerals-web-publisher-creds        (keys AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY)
   │  web-publisher-eso.yaml: ESO PushSecret
   │    • SecretStore `github-actions` (github provider, GitHub App auth)
   ▼
GitHub Actions secrets on dickeyfPersonalProjects/minerals
        S3_ACCESS_KEY_ID  /  S3_SECRET_ACCESS_KEY
```

A MinIO key rotation flows the whole way with no manual step:

```
rotate svcacct → web-publisher.yaml updates the Secret → ESO re-pushes → GitHub secrets update
```

---

## What lives where

| Piece | File | Notes |
|---|---|---|
| MinIO policy + svcacct + Secret materialization | [`kustomize/base/web-publisher.yaml`](../../kustomize/base/web-publisher.yaml) | Reusable base. SA + Role + RoleBinding + idempotent `mc`/`kubectl` Job. |
| ESO `SecretStore` + `PushSecret` | [`kustomize/base/web-publisher-eso.yaml`](../../kustomize/base/web-publisher-eso.yaml) | Reusable base. Placeholder GitHub App IDs (the per-env gate). |
| Both wired into the base | [`kustomize/base/kustomization.yaml`](../../kustomize/base/kustomization.yaml) | |
| GitHub App private key (sealed) | [`example/prod/github-app-eso.yaml`](./example/prod/github-app-eso.yaml) | **Publishing env only.** Placeholder ciphertext. |
| SecretStore App-ID patch | inline in [`example/prod/mineral.yaml`](./example/prod/mineral.yaml) | Fills the placeholder IDs for prod. |

**Real secret values live in fleet-infra (operator-owned, human-approved).**
Everything here is a base manifest or a redacted example — the operator seals
the real GitHub App key and patches the real App IDs in the gitops repo.

---

## Operator prerequisites

These operators must already be installed in the cluster (they are *not*
declared by this app's base):

- **MinIO Operator** — provides the `Tenant` the `mc` Job talks to
  (`kustomize/base/minio.yaml`).
- **External Secrets Operator (ESO)** — provides the `SecretStore` and
  `PushSecret` CRDs.
- **Sealed Secrets controller** — decrypts the `github-app-eso` SealedSecret
  (same controller the other example SealedSecrets already use; see
  [`encrypt.md`](./encrypt.md)).

---

## One-time setup (publishing env)

1. **Create a GitHub App** on the repo owner (`dickeyfPersonalProjects`) with
   Repository permission **Secrets: Read and write** (Actions secrets), and
   install it on the `minerals` repo. Record the **App ID** and **installation
   ID** (both non-secret).
2. **Generate the App private key** (`.pem`), seal it into `github-app-eso`,
   and commit it to the publishing overlay — recipe in
   [`example/prod/github-app-eso.yaml`](./example/prod/github-app-eso.yaml).
3. **Patch the SecretStore App IDs** in the overlay's Flux `Kustomization` —
   see the `github-actions` SecretStore patch in
   [`example/prod/mineral.yaml`](./example/prod/mineral.yaml).
4. **Set the non-secret S3 config as GitHub Actions _variables_** (see below).
5. Reconcile. Verify (see [Verifying](#verifying)).

A non-publishing env (e.g. staging) simply skips steps 1–4: the base
SecretStore keeps its placeholder App IDs and never authenticates, so it pushes
nothing. That placeholder is the deliberate gate that prevents two
environments — which share one GitHub repo — from racing on the same Actions
secrets.

---

## Non-secret config → GitHub Actions *variables*

ESO's `github` provider pushes **secrets**, not variables. The non-secret S3
settings the publish workflow needs are set once as repo **Actions variables**
(Settings → Secrets and variables → Actions → *Variables*), or hardcoded in the
workflow:

| Actions variable | Value | Notes |
|---|---|---|
| `S3_ENDPOINT` | e.g. `https://minio.dickey.cloud` | The publish job runs **outside** the cluster, so this is the MinIO *Ingress* hostname, not the in-cluster `minerals-hl:9000` the app uses. |
| `S3_BUCKET` | `minerals-web` | The SPA bundle bucket. |
| `S3_REGION` | `us-east-1` | Matches the Tenant's bucket region. |

> **MinIO needs path-style addressing.** MinIO does not do virtual-host-style
> (`bucket.host`) buckets by default, so the publish job's S3 client must set
> path-style (`aws --endpoint-url …` / `AWS_S3_FORCE_PATH_STYLE=true` / the SDK
> equivalent). This is a property of the client config in the (follow-up) CI
> workflow, noted here so it is not forgotten.

---

## Rotation

- **MinIO key rotation (the credential value):** delete the Secret and the
  svcacct, then delete the init Job so Flux re-creates it (commands in the
  [`web-publisher.yaml`](../../kustomize/base/web-publisher.yaml) header). A
  fresh key pair is generated → the Secret updates → ESO re-pushes → the GitHub
  Actions secrets update automatically. Old in-flight CI runs keep the old key
  until they finish; new runs get the new one.
- **GitHub App key rotation (the ESO auth):** generate a new App private key,
  re-seal `github-app-eso`, commit. ESO re-authenticates on its next sync. The
  *pushed* Actions secret values are unaffected — only the MinIO key rotation
  changes them.

---

## Verifying

```bash
# 1. The scoped MinIO svcacct + Secret materialized.
kubectl -n mineral-prod get job minerals-web-publisher-init
kubectl -n mineral-prod logs job/minerals-web-publisher-init   # shows svcacct info
kubectl -n mineral-prod get secret minerals-web-publisher-creds

# 2. ESO store is ready and the push succeeded.
kubectl -n mineral-prod get secretstore github-actions          # READY=True
kubectl -n mineral-prod get pushsecret minerals-web-publisher-to-github
kubectl -n mineral-prod describe pushsecret minerals-web-publisher-to-github

# 3. The GitHub Actions secrets exist (names only; values are write-only).
gh secret list --repo dickeyfPersonalProjects/minerals
```

A working pipeline shows the Job `Complete`, the SecretStore `Ready=True`, the
PushSecret with a recent successful sync, and `S3_ACCESS_KEY_ID` +
`S3_SECRET_ACCESS_KEY` in `gh secret list`.

---

## ⚠️ Verify against the INSTALLED versions before first apply

The manifests target documented, current CRD shapes but were authored without
access to the live cluster. Confirm before relying on them (and re-flag in the
PR if anything differs):

- **ESO `github` provider is write-only and GitHub App auth ONLY.** There is
  **no PAT** path on `provider.github` — the bead's "App or PAT" reduces to App
  in practice. (A PAT-based `GithubAccessToken` *generator* exists but is a
  different resource that does not push secrets.) Confirm the `github` provider
  and `PushSecret` to *Actions* secrets are supported by the **installed** ESO
  version.
- **API versions differ by stability:** `SecretStore` = `external-secrets.io/v1`,
  `PushSecret` = `external-secrets.io/v1alpha1`. Confirm both are served by the
  installed ESO CRDs.
- **`mc admin user svcacct add/edit/info`** flags are stable across recent `mc`
  releases; confirm against the `mc`/tenant image of the **installed** MinIO
  Operator.

---

## Cross-references

- Base/overlay split, Flux flow: [`README.md`](./README.md).
- kubeseal mechanics, scope, commit rules: [`encrypt.md`](./encrypt.md).
- SealedSecret `.sec/` convention (committed sealed `<dir>/<name>.yaml`,
  gitignored plaintext at `<dir>/.sec/<name>.yaml`): `CONTRACT.md` §17,
  "SealedSecrets & the `.sec/` plaintext convention". The
  [`example/prod/github-app-eso.yaml`](./example/prod/github-app-eso.yaml)
  header carries the concrete `.sec/` plaintext template + kubeseal command
  for the App private key.
- Full Secret inventory: [`secrets.md`](./secrets.md).
