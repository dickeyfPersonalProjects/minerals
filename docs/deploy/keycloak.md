# Keycloak setup for minerals

This guide covers the full Keycloak path for minerals: what this repo
provides, what a per-env GitOps overlay must add on top, how to run the
Terraform module that configures the realm, and the docker-compose
quickstart for local development.

It pairs with:

- [`README.md`](./README.md) — the base/overlay split and Flux flow.
- [`encrypt.md`](./encrypt.md) — kubeseal workflow for the
  `keycloak-db` SealedSecret.
- [`secrets.md`](./secrets.md) — Secret inventory (note: the
  `keycloak-db` Secret is not in that inventory yet because Keycloak
  is consumed by the *cluster*, not by the minerals app pod; it lives
  in the Keycloak namespace, not `mineral-<env>`).

> Keycloak runs once per cluster (not once per minerals env). The
> minerals app talks to it via its public auth hostname. The bits in
> this repo are intentionally cluster-scoped, not minerals-namespace-scoped.

---

## What the minerals repo provides

Two artifacts, deliberately split:

### 1. Env-agnostic base manifests — [`example/keycloak/`](./example/keycloak/)

The shape every env needs, with the env-specific fields deliberately
absent:

| File | What it declares |
|---|---|
| [`keycloak.yaml`](./example/keycloak/keycloak.yaml) | `Keycloak` CR (`k8s.keycloak.org/v2alpha1`) — single instance, `quick-theme` feature, `proxy.headers: xforwarded`, DB ref to the `keycloak-db` Secret (`username` + `password` keys). **Omits** `http`, `ingress`, `hostname` — those are env-specific. |
| [`database.yaml`](./example/keycloak/database.yaml) | CNPG `Database` CR that provisions the `keycloak` database inside the env's existing Postgres cluster. `spec.cluster.name` is a placeholder. |
| [`kustomization.yaml`](./example/keycloak/kustomization.yaml) | Aggregates the two resources. The header comments enumerate what every overlay must add. |

The base is **operator-input only**: it assumes the Keycloak operator
(`k8s.keycloak.org`) and CloudNativePG are already installed in the
cluster. It does not install operators.

### 2. Terraform module — [`terraform/keycloak/`](../../terraform/keycloak/)

Configures the realm itself after the operator brings Keycloak up. The
module owns:

- **Realm `minerals`** — token lifespans, password policy, optional
  SMTP, optional self-registration.
- **Clients** — `minerals-frontend` (public, PKCE) and
  `minerals-backend` (confidential, with a `view-users`
  realm-management role on its service account). Optional
  `minerals-test` password-grant client when `test_environment = true`.
- **Roles** — `admin`, `collector`, `viewer`.
- **Admin user** — `admin@${env_domain}` with an auto-generated 24-char
  password and the `realm-admin` role scoped to the minerals realm
  (not master-realm admin).
- **Optional OIDC IdPs** — Google and GitHub, enabled by setting the
  corresponding `*_client_id` / `*_client_secret` vars.

The split is deliberate: the operator owns the *runtime* (pods,
ingress, DB schema), Terraform owns the *configuration* (realm,
clients, roles). Either can be re-applied without the other.

---

## What gitops must add per environment

The base intentionally omits four things. Each env's overlay supplies
them.

### 1. `SealedSecret` for `keycloak-db`

Two keys — `username` and `password`. Consumed by `spec.db.usernameSecret`
and `spec.db.passwordSecret` on the Keycloak CR. The `owner` field on
the CNPG `Database` resource must match this `username`.

Follow [`encrypt.md`](./encrypt.md) for the kubeseal mechanics.
Plaintext input goes in `.sec/keycloak-db.yaml`; only the encrypted
output is committed. Ciphertext is bound to the Keycloak namespace, so
each cluster needs its own pass.

### 2. `Certificate` for `auth.${env_domain}`

A cert-manager `Certificate` that issues a TLS cert for the public auth
hostname. The resulting Secret is then referenced from the Keycloak CR
patch's `ingress.tlsSecret`. The pattern matches
[`example/staging/certificate.yaml`](./example/staging/certificate.yaml)
exactly — only the `dnsNames` and `secretName` differ.

### 3. Strategic merge patch on the Keycloak CR

Adds the three env-specific blocks the base omits:

```yaml
apiVersion: k8s.keycloak.org/v2alpha1
kind: Keycloak
metadata:
  name: keycloak
spec:
  hostname:
    hostname: auth.example.com
  http:
    tlsSecret: keycloak-tls   # name of the cert-manager-issued Secret
  ingress:
    enabled: true
    annotations:
      # whatever the cluster's ingress controller expects, e.g.
      # nginx.ingress.kubernetes.io/backend-protocol: HTTPS
```

…plus a patch on `database.yaml` setting `spec.cluster.name` to the
real CNPG cluster name (e.g. `prod-pg`), and a patch on `keycloak.yaml`
setting `spec.db.host` to that cluster's RW service
(e.g. `prod-pg-rw.prod.svc.cluster.local`).

### 4. Example overlay directory structure

A workable shape for the fleet-infra GitOps repo:

```
clusters/<cluster>/keycloak/
├── kustomization.yaml          ← lists the resources below, in lifecycle order
├── namespace.yaml              ← `keycloak` (or whatever you name it)
├── certificate.yaml            ← cert-manager Certificate for auth.<domain>
├── keycloak-db.yaml            ← SealedSecret (username + password)
├── patches/
│   ├── keycloak-cr.yaml        ← merge patch: hostname + http + ingress
│   ├── keycloak-db-host.yaml   ← merge patch: spec.db.host
│   └── database-cluster.yaml   ← merge patch: spec.cluster.name
└── kustomization.yaml          ← also pulls minerals repo's kustomize/base/keycloak via remote ref
```

The kustomization uses a remote base pointing at this repo's
`kustomize/base/keycloak`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: keycloak
resources:
  - namespace.yaml
  - certificate.yaml
  - keycloak-db.yaml
  - https://github.com/dickeyfPersonalProjects/minerals//kustomize/base/keycloak?ref=main
patches:
  - path: patches/keycloak-cr.yaml
    target: {kind: Keycloak, name: keycloak}
  - path: patches/keycloak-db-host.yaml
    target: {kind: Keycloak, name: keycloak}
  - path: patches/database-cluster.yaml
    target: {kind: Database, name: keycloak}
```

Pin `?ref=` to a tag (not `main`) once the base manifests stabilize, so
GitOps doesn't track upstream drift.

---

## Running the Terraform module

The module lives at [`terraform/keycloak/`](../../terraform/keycloak/).
It uses the `mrparkers/keycloak` provider and supports two
authentication modes:

- **Master-realm username/password** — `keycloak_admin_user` +
  `keycloak_admin_password`. Used for bootstrap and local dev.
- **Service-account OIDC** — `keycloak_client_id` +
  `keycloak_client_secret`. Preferred for staging/prod (no admin
  password in Terraform state). Provision the client manually in the
  master realm first; grant it `realm-admin` on the minerals realm.

The module switches modes automatically based on whether
`keycloak_client_id` is set.

### Prerequisites

- Keycloak running and reachable at the URL the module will target.
  For local dev that's `http://localhost:8080`; for cluster envs that's
  `https://auth.${env_domain}` after the operator + ingress are up.
- `terraform` ≥ 1.5.0.
- For OIDC-auth mode: a service-account client already created in the
  `master` realm with `realm-admin` on the minerals realm.

### Local dev (Mode: master credentials)

```bash
cd terraform/keycloak
cp dev.tfvars.example dev.tfvars     # admin/admin against localhost:8080
terraform init
terraform apply -var-file=dev.tfvars
```

The shipped `dev.tfvars.example` targets `http://localhost:8080` with
the `admin`/`admin` credentials baked into the docker-compose service
(see [Local dev quickstart](#local-dev-quickstart) below). It does not
set OIDC IdP credentials, so only the local admin user can sign in.

> **Heads-up on `frontend_url` in local dev.** With
> `env_domain = "localhost"`, the module derives
> `frontend_url = "https://www.localhost"`, which is not how you'll
> actually run the SPA locally. Add your real dev SPA origin via
> `additional_redirect_uris` and `additional_web_origins`, e.g.:
> ```hcl
> additional_redirect_uris = ["http://localhost:5173/*"]
> additional_web_origins   = ["http://localhost:5173"]
> ```

### Staging / prod (Mode: OIDC service-account)

```bash
cd terraform/keycloak
terraform init
terraform apply -var-file=staging.tfvars
```

A workable `staging.tfvars`:

```hcl
env_domain             = "staging.example.com"
keycloak_client_id     = "terraform-admin"
keycloak_client_secret = "..."   # from secret manager, do not commit
realm_name             = "minerals"
registration_allowed   = false
smtp_host              = "smtp.sendgrid.net"
smtp_from              = "no-reply@staging.example.com"
smtp_user              = "apikey"
smtp_password          = "..."
google_client_id       = "..."
google_client_secret   = "..."
```

`env_domain` drives both `keycloak_url` (defaults to
`https://auth.${env_domain}`) and `frontend_url`
(`https://www.${env_domain}`). Override `keycloak_url` explicitly via
`keycloak_url_override` if the auth hostname doesn't follow that
convention.

`.tfvars` files containing secrets do NOT belong in this repo. Render
them at apply time from the cluster's secret manager (or pass values
inline via `-var`).

### Key outputs

```bash
terraform output realm_issuer
terraform output oidc_discovery_url
terraform output frontend_client_id
terraform output backend_client_id
terraform output -raw backend_client_secret    # sensitive
terraform output -raw admin_password           # sensitive — capture once
```

The two sensitive outputs are the load-bearing ones:

- **`backend_client_secret`** — the Go backend's confidential-client
  credential. Goes into a SealedSecret consumed by the Deployment.
- **`admin_password`** — the realm admin's initial password. Capture
  it once, store it in a password manager, then it can be rotated
  out-of-band.

### Wiring outputs into k8s Secrets for the Go backend

The pattern (manual, gitops-side; not automated by the module today):

1. `terraform output -raw backend_client_secret` to retrieve the value.
2. Place it in a plaintext `.sec/minerals-oidc.yaml` Secret manifest
   alongside the issuer URL and client ID.
3. Run kubeseal per [`encrypt.md`](./encrypt.md) to produce
   `minerals-oidc.yaml` in the env overlay.
4. Add it to the env's `kustomization.yaml` resources list.
5. Reference it from `kustomize/base/deployment.yaml` via `envFrom`
   when the auth implementation bead lands.

The "auto-rotate the SealedSecret when Terraform rotates the secret"
loop is out of scope today — the secret is set once and rotated
manually if needed. External-Secrets + Vault would close that loop;
see [`encrypt.md`](./encrypt.md) for the broader external-secrets
discussion.

---

## Local dev quickstart

```bash
# 1. Start Keycloak alone (port 8080, admin/admin, in-memory H2).
docker compose --profile keycloak up -d keycloak

# 2. Wait ~10s for it to come up, then verify:
curl -fsS http://localhost:8080/realms/master >/dev/null && echo OK

# 3. Configure the realm.
cd terraform/keycloak
cp dev.tfvars.example dev.tfvars       # already targets localhost:8080
# (optional) edit dev.tfvars to add SPA redirect URIs — see warning above.
terraform init
terraform apply -var-file=dev.tfvars

# 4. Sign in to the realm admin console at:
#    http://localhost:8080/admin/master/console/#/minerals
#    Username: admin
#    Password: $(terraform output -raw admin_password)
```

### Why `--profile keycloak`?

`docker-compose.yml` gates the `keycloak` service behind the `keycloak`
profile because the `app` service and Keycloak both bind host port
8080 — they cannot run simultaneously. The two supported shapes are:

```bash
# Keycloak alone (no app, no DB needed — start-dev uses in-memory H2):
docker compose --profile keycloak up -d keycloak

# Mode B (app deps only) + Keycloak:
docker compose up -d postgres minio
docker compose --profile keycloak up -d keycloak
```

`docker compose up -d` with no args (Mode A) starts the app on
:8080 and does not start Keycloak. That's intentional — most dev work
on the app doesn't need Keycloak running.

### `dev.tfvars` is gitignored

The `dev.tfvars.example` file in the repo is the template. The actual
`dev.tfvars` is gitignored (see `terraform/keycloak/.gitignore` if
present, or the repo-root `.gitignore`) because individual developers
may put real OIDC IdP credentials in it for testing Google/GitHub
sign-in locally.

---

## How the app consumes OIDC

> **Forward reference.** The Go backend does not consume these
> variables yet — the auth implementation lands in a later bead. This
> section documents the contract Terraform's outputs already
> establish, so the auth bead has a fixed target.

The backend will need three values at runtime, derived from the
Terraform outputs:

| Setting | Source (Terraform output) | Notes |
|---|---|---|
| OIDC issuer URL | `realm_issuer` (e.g. `https://auth.example.com/realms/minerals`) | Used to discover endpoints and verify token `iss`. |
| Frontend client ID | `frontend_client_id` (`minerals-frontend`) | The SPA's public client. No secret. |
| Backend client ID | `backend_client_id` (`minerals-backend`) | Confidential client used for token introspection / userinfo lookups. |
| Backend client secret | `backend_client_secret` (sensitive) | Paired with the backend client ID. |

The expected env-var names follow the existing CONFIG.md convention
(`SCREAMING_SNAKE_CASE`, no prefix). Likely shape:

```
OIDC_ISSUER_URL=https://auth.example.com/realms/minerals
OIDC_FRONTEND_CLIENT_ID=minerals-frontend
OIDC_BACKEND_CLIENT_ID=minerals-backend
OIDC_BACKEND_CLIENT_SECRET=...    # from SealedSecret
```

Final names are decided by the auth bead. When that bead lands it must
update [`CONFIG.md`](../../CONFIG.md) (settings inventory) and
[`secrets.md`](./secrets.md) (Secret inventory) in the same PR.

The realm exposes three roles — `admin`, `collector`, `viewer` — which
the backend will map to the per-row authorization rules in
[`CONTRACT.md`](../../CONTRACT.md) §13. See
[`docs/design/05-auth-slot.md`](../design/05-auth-slot.md) for the
current stub and how it gets replaced.
