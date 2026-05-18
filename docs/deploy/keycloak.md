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
  `keycloak-db` Secret is not in that inventory yet; it lives in
  the env's `mineral-<env>` namespace alongside the other secrets).

> Keycloak runs **once per environment**, deployed into the same
> namespace as the minerals app (`mineral-staging`, `mineral-prod`)
> so it shares the env's CNPG cluster and lifecycle boundary. The
> base manifests in this repo describe the shape that's the same
> across every env; each per-env overlay supplies hostname, TLS,
> ingress, DB host, and DB credentials.

---

## What the minerals repo provides

Three artifacts, deliberately split:

### 1. Env-agnostic base manifests — [`kustomize/base/keycloak/`](../../kustomize/base/keycloak/)

The shape every env needs, with the env-specific fields deliberately
absent. This base is **never deployed directly** — it is always
consumed via a per-env overlay (see §2).

| File | What it declares |
|---|---|
| [`keycloak.yaml`](../../kustomize/base/keycloak/keycloak.yaml) | `Keycloak` CR (`k8s.keycloak.org/v2alpha1`) — single instance, `quick-theme` feature, `proxy.headers: xforwarded`, DB ref to the `keycloak-db` Secret (`username` + `password` keys). **Omits** `http`, `ingress`, `hostname` — those are env-specific. The `db.host` is a placeholder; overlays patch it. |
| [`database.yaml`](../../kustomize/base/keycloak/database.yaml) | CNPG `Database` CR that provisions the `keycloak` database inside the env's existing Postgres cluster. `spec.cluster.name` is a placeholder; overlays patch it. |
| [`kustomization.yaml`](../../kustomize/base/keycloak/kustomization.yaml) | Aggregates the two resources. The header comments enumerate what every overlay must add. |

The base is **operator-input only**: it assumes the Keycloak operator
(`k8s.keycloak.org`) and CloudNativePG are already installed in the
cluster. It does not install operators.

### 2. Per-env overlay examples — [`example/prod/keycloak/`](./example/prod/keycloak/) and [`example/staging/keycloak/`](./example/staging/keycloak/)

Worked examples of what a GitOps operator adds **per environment**,
on top of the base in §1. The two overlays mirror each other and
only differ in hostname, namespace, and DB host. Each gets copied
(and the literals replaced) into the target gitops repository.

| File | What it declares |
|---|---|
| `certificate.yaml` | cert-manager `Certificate` for the env's public auth hostname (`auth.example.com` for prod, `auth.staging.example.com` for staging). Produces the TLS Secret referenced from the ingress patch. |
| `keycloak-db-secret.yaml` | `SealedSecret` named `keycloak-db` with `username` + `password` keys. Encrypted to the target cluster's sealed-secrets controller; each env needs its own sealing pass. |
| `keycloak-ingress-patch.yaml` | Strategic merge patch on the Keycloak CR. Injects the env-specific `spec.db.host`, `spec.hostname`, `spec.http`, and `spec.ingress` blocks the base omits. |
| `kustomization.yaml` | Pulls the base from `../../../../../kustomize/base/keycloak`, layers in the two resources above, and applies the merge patch. Sets the env-specific namespace. |

These overlays are intentionally *not* aggregated by the
top-level [`example/kustomization.yaml`](./example/kustomization.yaml)
that ties together the per-minerals-env `staging/` + `prod/` app
overlays. Keycloak's lifecycle is independent of the minerals app
deployment and is reconciled as its own Flux Kustomization (or
`kubectl apply -k`).

### 3. Terraform module — [`terraform/keycloak/`](../../terraform/keycloak/)

Configures the realm itself after the operator brings Keycloak up. The
module owns:

- **Realm `minerals`** — token lifespans, password policy, optional
  SMTP, optional self-registration.
- **Clients** — `minerals-frontend` (confidential, drives the V2 BFF
  server-to-server code exchange — mi-1d5i) and `minerals-backend`
  (confidential, with a `view-users` realm-management role on its
  service account). Optional `minerals-test` password-grant client
  when `test_environment = true`.
- **Roles** — `devops-viewer`, `devops-admin` (devops/staff only;
  normal users have no realm role and are authorized by per-row rules
  in [`CONTRACT.md`](../../CONTRACT.md) §13).
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

The base intentionally omits everything env-specific. The example
overlays at [`example/prod/keycloak/`](./example/prod/keycloak/) and
[`example/staging/keycloak/`](./example/staging/keycloak/) show the
complete shape; this section walks through what each piece does and
how they fit together.

### 1. `SealedSecret` for `keycloak-db`

Two keys — `username` and `password`. Consumed by `spec.db.usernameSecret`
and `spec.db.passwordSecret` on the Keycloak CR in the base. The
`owner` field on the CNPG `Database` resource (`keycloak` by default)
must match this `username`, otherwise the operator provisions a role
the Keycloak CR can't authenticate as.

Follow [`encrypt.md`](./encrypt.md) for the kubeseal mechanics.
Plaintext input goes in `.sec/keycloak-db.yaml`; only the encrypted
output is committed. Ciphertext is bound to the Keycloak namespace, so
each env needs its own sealing pass. See
[`example/prod/keycloak/keycloak-db-secret.yaml`](./example/prod/keycloak/keycloak-db-secret.yaml)
(or the staging mirror) for the manifest shape.

### 2. `Certificate` for `auth.${env_domain}`

A cert-manager `Certificate` that issues a TLS cert for the public auth
hostname. The resulting Secret is then referenced from the Keycloak CR
patch's `http.tlsSecret`. See
[`example/prod/keycloak/certificate.yaml`](./example/prod/keycloak/certificate.yaml)
(and the staging mirror) — only the `dnsNames` and `namespace`
differ per env.

### 3. Strategic merge patch on the Keycloak CR

A single strategic merge patch supplies every env-specific block the
base omits — `spec.hostname`, `spec.http`, `spec.ingress`, plus
`spec.db.host`. Kustomize merges this on top of the base's `Keycloak`
CR by matching `kind: Keycloak` + `name: keycloak`.

See [`example/prod/keycloak/keycloak-ingress-patch.yaml`](./example/prod/keycloak/keycloak-ingress-patch.yaml)
(and the staging mirror) for the full shape. The relevant fields:

```yaml
apiVersion: k8s.keycloak.org/v2alpha1
kind: Keycloak
metadata:
  name: keycloak           # must match the base's CR name
spec:
  db:
    host: minerals-pg-rw.mineral-<env>.svc.cluster.local   # CNPG RW service
  hostname:
    hostname: auth.<env-domain>
  http:
    httpEnabled: true
    tlsSecret: <secretName from certificate.yaml>
  ingress:
    enabled: true
    annotations:
      # cluster ingress controller specifics, e.g.
      # nginx.ingress.kubernetes.io/backend-protocol: HTTPS
```

The example does not patch the `Database` CR's `spec.cluster.name` —
the base ships a placeholder (`example-pg`). A real overlay should
add a second patch (or amend this one) targeting
`kind: Database, name: keycloak` to point at the env's actual CNPG
`Cluster` name.

### 4. Overlay kustomization

Each example's [`kustomization.yaml`](./example/prod/keycloak/kustomization.yaml)
pulls the base via a relative path that works inside this repo:

```yaml
resources:
  - ../../../../../kustomize/base/keycloak
  - keycloak-db-secret.yaml
  - certificate.yaml
patches:
  - path: keycloak-ingress-patch.yaml
    target: {kind: Keycloak, name: keycloak}
```

In a real gitops repository checkout, replace the relative path with
a remote ref pointing at this repo:

```yaml
resources:
  - https://github.com/dickeyfPersonalProjects/minerals//kustomize/base/keycloak?ref=<tag>
  - keycloak-db-secret.yaml
  - certificate.yaml
```

Pin `?ref=` to a tag (not `main`) once the base manifests stabilize,
so GitOps doesn't track upstream drift.

A workable directory layout for the gitops repository (one directory
per environment, since each Keycloak instance is reconciled as its
own Flux Kustomization independent of the app):

```
clusters/<cluster>/keycloak-<env>/
├── kustomization.yaml             ← remote base ref + resources + patch
├── certificate.yaml               ← cert-manager Certificate for auth.<env-domain>
├── keycloak-db-secret.yaml        ← SealedSecret (username + password)
└── keycloak-ingress-patch.yaml    ← merge patch: db.host/hostname/http/ingress
```

The examples in this repo and the layout above omit a
`namespace.yaml` because the target namespace (`mineral-<env>`) is
already created by the minerals app overlay — Keycloak is deployed
into the same namespace as the app, so the namespace exists by the
time this overlay reconciles.

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
  For local dev that's `http://localhost:8081` (set by the
  `docker-compose.yml` keycloak service — see [Local dev
  quickstart](#local-dev-quickstart)); for cluster envs that's
  `https://auth.${env_domain}` after the operator + ingress are up.
- `terraform` ≥ 1.5.0.
- For OIDC-auth mode: a `terraform-admin` service-account client
  provisioned in the `master` realm with the `admin` role assigned to
  its service account. See [Terraform Authentication](#terraform-authentication)
  below for the click-through.

### Retrieving the initial Keycloak admin credentials

On first boot the Keycloak operator auto-generates a temporary master-realm
admin user and stores its credentials in a Kubernetes secret named
`keycloak-initial-admin`. Pull the username (usually `temp-admin`) and
password with:

```bash
# Initial Keycloak username (usually: temp-admin)
kubectl get secret -n mineral-prod keycloak-initial-admin -o json | jq -r '.data.username' | base64 -d

# Initial Keycloak password
kubectl get secret -n mineral-prod keycloak-initial-admin -o json | jq -r '.data.password' | base64 -d
```

Replace `mineral-prod` with the actual namespace for your environment. These
credentials are intended for bootstrap only — rotate them (or replace this
user) after initial setup.

### Terraform Authentication

Before configuring either auth mode below, you need the master-realm admin
username/password (Option 2) or a `terraform-admin` client secret (Option 1).
For a fresh cluster, the initial admin credentials live in the
`keycloak-initial-admin` Kubernetes secret — see
[Retrieving the initial Keycloak admin credentials](#retrieving-the-initial-keycloak-admin-credentials)
above.

Two options, switched automatically based on whether `keycloak_client_id`
is set in `terraform.tfvars`.

#### Option 1: OIDC Client Credentials (Recommended)

For CI/CD and shared environments. Requires one-time manual setup in
Keycloak.

1. Log in to Keycloak Admin Console.
2. Select the **master** realm.
3. Go to **Clients → Create client**:
   - Client ID: `terraform-admin`
   - Client type: OpenID Connect → **Next**
4. Capability config:
   - Client authentication: **ON**
   - Service accounts roles: **ON** → **Next** → **Save**
5. Open the **Credentials** tab → copy the **Client secret**.
6. Open the **Service account roles** tab → **Assign role**:
   - Filter by: **Realm roles**
   - Assign the `admin` role (description: `role_admin`).

Then configure `terraform.tfvars`:

```hcl
keycloak_url           = "http://localhost:8081"
keycloak_client_id     = "terraform-admin"
keycloak_client_secret = "your-client-secret-here"
```

No admin password ends up in Terraform state with this mode, which is
why it's preferred for staging and prod.

#### Option 2: Username/Password (Local dev only)

Uses the master-realm admin credentials directly. Suitable for local
development only — the admin password lands in Terraform state.

Configure `terraform.tfvars`:

```hcl
keycloak_url            = "http://localhost:8081"
keycloak_client_id      = "admin-cli"
keycloak_client_secret  = ""   # leave empty to use password auth
keycloak_admin_user     = "admin"
keycloak_admin_password = "admin"
```

### Local dev (Mode: master credentials)

```bash
cd terraform/keycloak
terraform init
terraform apply -var-file=dev.tfvars
```

The committed `dev.tfvars` targets `http://localhost:8081` with the
`admin`/`admin` credentials baked into the docker-compose service (see
[Local dev quickstart](#local-dev-quickstart) below) and already wires
the Vite SPA origin (`http://localhost:5173`) into
`additional_redirect_uris` / `additional_web_origins`. It does not set
SMTP or OIDC IdP credentials, so only the local admin user can sign in
and `registration_allowed = false`.

> **Heads-up on `frontend_url` in local dev.** With
> `env_domain = "localhost"`, the module derives
> `frontend_url = "https://www.localhost"`, which is not how you'll
> actually run the SPA locally. The committed `dev.tfvars` works
> around this with the `additional_*` overrides; bump those if your
> dev SPA origin differs.

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
terraform output -raw frontend_client_secret   # sensitive — V2 BFF (mi-1d5i)
terraform output backend_client_id
terraform output -raw backend_client_secret    # sensitive
terraform output -raw admin_password           # sensitive — capture once
```

The three sensitive outputs are the load-bearing ones:

- **`frontend_client_secret`** — the V2 BFF's confidential-client
  credential (mi-1d5i). Goes into the per-env SealedSecret
  `minerals-oidc-secret` under the `OIDC_CLIENT_SECRET` key, consumed
  by the Deployment via `envFrom: secretRef` (optional, mi-ron5).
- **`backend_client_secret`** — the `minerals-backend` service-account
  credential (used for any server-to-server Keycloak admin calls; not
  the V2 BFF user-auth path).
- **`admin_password`** — the realm admin's initial password. Capture
  it once, store it in a password manager, then it can be rotated
  out-of-band.

### Wiring outputs into k8s Secrets for the Go backend

The V2 BFF (mi-1d5i) reads `OIDC_CLIENT_SECRET` and
`OAUTH_STATE_HMAC_KEY` from the per-env SealedSecret
`minerals-oidc-secret`. The Deployment already references this Secret
via `envFrom: secretRef` with `optional: true`
([`kustomize/base/deployment.yaml`](../../kustomize/base/deployment.yaml)),
so populating it is the only operator step needed.

1. `terraform output -raw frontend_client_secret` to retrieve the
   value Keycloak generated for the (now confidential) `minerals-frontend`
   client.
2. Generate a long-lived HMAC key for the state cookie, e.g.
   `openssl rand -hex 32`. Same value goes in every pod replica; rotate
   it and in-flight logins fail (`400 invalid_state`, users retry).
3. Stage a plaintext `.sec/minerals-oidc-secret.yaml` manifest with
   both data keys (`OIDC_CLIENT_SECRET`, `OAUTH_STATE_HMAC_KEY`).
4. Run kubeseal per [`encrypt.md`](./encrypt.md) to produce the
   encrypted manifest. The example stubs at
   [`example/staging/minerals-oidc-secret.yaml`](./example/staging/minerals-oidc-secret.yaml)
   and [`example/prod/minerals-oidc-secret.yaml`](./example/prod/minerals-oidc-secret.yaml)
   show the target shape.
5. Commit the encrypted manifest to the gitops repo (it's already in
   the example overlay's `kustomization.yaml` resources list).

The Go backend's BFF gate (`cmd/minerals/serve.go`) treats either key
being empty as "BFF disabled" and leaves `/auth/login`,
`/auth/callback`, `/auth/logout` unregistered, so a pod whose
SealedSecret hasn't reconciled yet stays cleanly on the legacy PKCE
path.

The "auto-rotate the SealedSecret when Terraform rotates the secret"
loop is out of scope today — the secret is set once and rotated
manually if needed. External-Secrets + Vault would close that loop;
see [`encrypt.md`](./encrypt.md) for the broader external-secrets
discussion.

### Cutover sequence: applying the PUBLIC → CONFIDENTIAL flip

Per mi-ron5 / mi-1d5i, the `minerals-frontend` Keycloak client is now
provisioned as `CONFIDENTIAL`. The order in which the Terraform apply
and the SealedSecret rollout reach a given environment matters:

**Before `terraform apply` against the env:**

1. **Backend already has `OIDC_CLIENT_SECRET` available.** The
   SealedSecret `minerals-oidc-secret` must be sealed, committed to
   the gitops repo, reconciled by Flux, and visible as a Secret in
   `mineral-<env>` (`kubectl get secret -n mineral-<env>
   minerals-oidc-secret`). Until then the env var is unset; the
   backend stays on PKCE; flipping Keycloak now breaks login.
2. Note that the SealedSecret can be sealed with a *temporary*
   placeholder value for `OIDC_CLIENT_SECRET` first if the operator
   wants the Deployment wired and reconciled before the real secret
   exists — the backend won't try to use it until Keycloak is
   confidential. The `OAUTH_STATE_HMAC_KEY` value is operator-
   generated and can be set in the same sealing pass.

**Apply Terraform.**

3. `terraform apply -var-file=<env>.tfvars` against the env. The plan
   should show the `minerals-frontend` client's `access_type` going
   from `PUBLIC` to `CONFIDENTIAL` as the only in-place update on the
   client (plus a new computed `client_secret`). Capture
   `terraform output -raw frontend_client_secret`.
4. If step 1 used a placeholder, re-seal `minerals-oidc-secret` with
   the real `OIDC_CLIENT_SECRET` value now, commit, and let Flux
   reconcile. The Deployment's `envFrom` re-reads the Secret on the
   next pod roll; force a rollout with
   `kubectl rollout restart deploy/minerals -n mineral-<env>` if
   immediate cutover is needed.

**After Terraform apply and SealedSecret reconciliation:**

5. **Verify the BFF smoke against staging.** The Playwright BFF smoke
   (mi-sap2) is the gate — it drives the full
   `/auth/login` → Keycloak → `/auth/callback` → cookie session
   round-trip against the now-confidential client. A green smoke
   confirms the flip landed cleanly.
6. If the smoke fails, the legacy PKCE path no longer works (Keycloak
   is confidential) — rolling back means either re-applying Terraform
   with `access_type = "PUBLIC"` or fixing the secret plumbing.
   Prefer the latter; the dep chain (mi-1d5i) is one-directional.

---

## Local dev quickstart

```bash
# 1. Start Keycloak alone — host port :8081, admin/admin master creds,
#    in-memory H2. Profile-gated; see "Why --profile keycloak?" below.
docker compose --profile keycloak up -d keycloak

# 2. Apply the realm + seed the test users (idempotent; re-runnable):
bash terraform/keycloak/dev-seed.sh

# 3. Sign in to the realm admin console at:
#    http://localhost:8081/admin/master/console/#/minerals
#    Username: admin
#    Password: $(cd terraform/keycloak && terraform output -raw admin_password)
```

`dev-seed.sh` runs `terraform init` + `terraform apply -var-file=dev.tfvars`
under the hood and then provisions a known set of dev users via the
Keycloak admin API. The committed `dev.tfvars` already wires
`http://localhost:5173/*` into the `minerals-frontend` client's
`additional_redirect_uris` / `additional_web_origins`, so the Vite SPA
can complete the OIDC redirect with no manual editing.

### Test users

All test users share the password `MineralsDev123!` (set in
`dev-seed.sh` as `DEV_PASSWORD`; override via the environment to use a
different value).

| Username | Realm role | Notes |
|---|---|---|
| `user1`, `user2`, `user3`, `user4`, `user5` | _(none)_ | Five generic end users. Authorized at the row level per [`CONTRACT.md`](../../CONTRACT.md) §13. |
| `devops_viewer_user` | `devops-viewer` | Read-only operational access. |
| `devops_admin_user` | `devops-admin` | Full operational access (inherits `devops-viewer` via Casbin, not Keycloak composite roles). |

### Why `--profile keycloak`?

Keycloak binds host `:8081` and the minerals app binds host `:8080`, so
they happily run side-by-side — no port collision. The profile gate
exists purely so the default `docker compose up -d` doesn't bring
Keycloak up for the (common) cases where the work at hand doesn't need
it. Bring it up explicitly when you do:

```bash
# Keycloak alone (no app, no DB needed — start-dev uses in-memory H2):
docker compose --profile keycloak up -d keycloak

# Full stack (app on :8080) + Keycloak (on :8081):
docker compose up -d
docker compose --profile keycloak up -d keycloak
```

### `dev.tfvars` is committed

The repo ships `dev.tfvars` committed and ready to use — no copy step,
no edits required. If you want to test Google/GitHub IdPs locally with
real credentials, set them via `-var` on the command line rather than
editing the committed file.

---

## How the app consumes OIDC

**Backend is the OIDC client.** Under V2 BFF (mi-1d5i) the Go backend
drives the full auth-code flow: it builds the authorization URL,
receives the callback, exchanges the code for tokens server-to-server
using the confidential client's secret, creates a session row, and
hands the browser an httpOnly cookie. The SPA never sees a token. See
[`docs/design/auth-bff.md`](../design/auth-bff.md) for the canonical
contract.

The deployment consumes these BFF env vars. The non-secret ones live
in the per-env ConfigMap patch; the two secrets live in
`minerals-oidc-secret` (per § "Wiring outputs into k8s Secrets" above
and [`secrets.md`](./secrets.md)).

| Env var | Source | Purpose |
|---|---|---|
| `OIDC_ISSUER_URL` | `realm_issuer` (e.g. `https://auth.example.com/realms/minerals`) | Discover endpoints, verify token `iss`. JWKS URL learned via `{OIDC_ISSUER_URL}/.well-known/openid-configuration`. |
| `OIDC_CLIENT_ID` | `frontend_client_id` (`minerals-frontend`) | Identifies the confidential client on the token exchange and the audience the backend expects on issued tokens. |
| `OIDC_CLIENT_SECRET` | `terraform output -raw frontend_client_secret` (sealed into `minerals-oidc-secret`) | Authenticates the backend on the `/token` exchange. |
| `OAUTH_STATE_HMAC_KEY` | Operator-generated, ≥ 32 bytes (sealed into `minerals-oidc-secret`) | Signs the short-lived state cookie issued by `/auth/login` and verified on `/auth/callback`. |
| `PUBLIC_OIDC_REDIRECT_URI` | `https://www.<env_domain>/auth/callback` | The backend's own callback URL. MUST match a `valid_redirect_uris` entry on the `minerals-frontend` client (the Terraform module wires `${frontend_url}/auth/callback` by default). |

The full BFF settings inventory (cookie flags, session lifetimes,
CSRF gate, etc.) lives in [`CONFIG.md`](../../CONFIG.md). Per-env
hostname-dependent values live in the GitOps overlay — see
[`example/staging/mineral.yaml`](./example/staging/mineral.yaml) and
[`example/prod/mineral.yaml`](./example/prod/mineral.yaml) for the
ConfigMap patch shape.

The realm exposes two devops/staff roles — `devops-viewer` and
`devops-admin`. Normal users have no realm role; the backend
authorizes them via the per-row visibility/ownership rules in
[`CONTRACT.md`](../../CONTRACT.md) §13. See
[`docs/design/05-auth-slot.md`](../design/05-auth-slot.md) for the
current stub and how it gets replaced.
