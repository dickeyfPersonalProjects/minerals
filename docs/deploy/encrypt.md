# Encrypting Secrets with kubeseal

This document covers the workflow for producing the `SealedSecret`
manifests referenced from the example overlay (`example/staging/`,
`example/prod/`). The output of this workflow is what gets committed to
the GitOps repo; the plaintext input never leaves your workstation.

> **Pairs with** [`secrets.md`](./secrets.md) — the inventory of every
> Secret the deployment consumes, including which ones are
> operator-sealed (this workflow) versus operator-generated (CNPG,
> cert-manager).

## Why SealedSecrets

SealedSecrets are the default for this fleet because they let plaintext
Secrets be encrypted to a public key bound to a specific cluster and
checked into git. Only that cluster's `sealed-secrets` controller can
decrypt them, so a leaked GitOps repo doesn't leak credentials.

> **Prefer an external secret manager when one is available.** A
> HashiCorp Vault or cloud-provider secret manager (AWS Secrets Manager,
> GCP Secret Manager, etc.) wired in via `external-secrets-operator` is
> the better default for any environment that already runs one — it
> separates secret lifecycle from manifest lifecycle, supports rotation
> without redeploying, and avoids permanently checking in ciphertext
> tied to a specific cluster key. Use kubeseal when no such system is
> in place. This guide documents kubeseal as the fallback when no such
> system is available.

Plaintext Secrets in git are not allowed. Plaintext Secrets stored
out-of-band (e.g. an operator's password manager, applied by hand) are
allowed for one-off bootstrap but are not GitOps and should be replaced
with a SealedSecret as soon as practical.

## Prerequisites

- `kubeseal` CLI installed locally (matching the controller's major
  version).
- `kubectl` configured against the target cluster, OR a local copy of
  the cluster's sealed-secrets public cert (use `--cert <path>` to
  encrypt offline).
- The `sealed-secrets` controller installed in the target cluster.

## The `.sec/` staging convention

Plaintext Secret manifests live in a `.sec/` directory **alongside the
sealed manifests they produce** — for a manifest committed at
`<dir>/<name>.yaml`, its plaintext source is `<dir>/.sec/<name>.yaml`
(seal from `<dir>` and the `.sec/` path below is relative to it). `.sec/`
is gitignored fleet-wide (`.sec/` and `**/.sec/`); it never gets
committed. This is the repo-wide rule for every secret example — see
`CONTRACT.md` §17, "SealedSecrets & the `.sec/` plaintext convention".

```
.sec/
├── minerals-minio-config.yaml      # plaintext, never committed
└── minerals-s3-creds.yaml          # plaintext, never committed
```

A plaintext input file looks like an ordinary Kubernetes `Secret`:

```yaml
# .sec/minerals-s3-creds.yaml
apiVersion: v1
kind: Secret
metadata:
  name: minerals-s3-creds
  namespace: mineral-staging
type: Opaque
stringData:
  S3_ACCESS_KEY_ID: AKIAEXAMPLE
  S3_SECRET_ACCESS_KEY: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

## Encrypt

Run `kubeseal` to convert the plaintext Secret into a `SealedSecret`,
writing the output to the path you intend to commit:

```bash
kubeseal -o yaml -f .sec/minerals-s3-creds.yaml > minerals-s3-creds.yaml
```

The output file is the manifest the cluster reconciles. Only that file
is committed; the `.sec/` original stays on your workstation.

### Scope: namespace + name

By default, a SealedSecret is bound to the **namespace + name** of the
input Secret. The same plaintext encrypted for `mineral-staging` will
not decrypt in `mineral-prod` — encrypt once per environment.

If you need to share a SealedSecret across namespaces, pass
`--scope namespace-wide` (any name in one namespace) or
`--scope cluster-wide` (any namespace, any name). Cluster-wide is the
loosest scope and should be the exception, not the default.

### Multi-key Secrets

`kubeseal` encrypts every key in the input Secret in one pass. The
resulting `spec.encryptedData` map has one ciphertext per key. To
rotate just one key, re-encrypt the full Secret — there is no per-key
patch.

## Commit rules

- **Commit only the encrypted output.** The ciphertext is safe in git;
  the plaintext is not.
- **Never commit `.sec/`.** It is gitignored, but check `git status`
  before staging anyway.
- **GitOps-repository commits require human approval.** The gitops
  repository is reconciled directly into a live cluster — every
  commit there changes production state. Polecats and other automated
  agents must not push to the gitops repository; changes go through a
  human-approved PR.

## Rotating a SealedSecret

1. Update the plaintext file in `.sec/`.
2. Re-run `kubeseal` to regenerate the encrypted manifest.
3. Commit the new ciphertext. The controller reconciles it into the
   underlying `Secret`; pods that consume it via `envFrom`/`secretRef`
   pick up the new value on their next restart.

For env-vars consumed via `envFrom`, a `kubectl rollout restart` of
the consuming Deployment is required to pick up the change — Kubernetes
does not propagate `Secret` updates to running pod env vars.
