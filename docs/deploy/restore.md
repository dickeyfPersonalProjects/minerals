# Restoring Postgres from a CNPG backup

This is the canonical recovery procedure for the minerals Postgres
cluster in prod. It assumes the backup setup landed by **mi-jgzi** is
healthy: a `Cluster` patched with `backup.barmanObjectStore`, a weekly
`ScheduledBackup`, a dedicated `minerals-postgres-backups` MinIO
bucket with versioning enabled, and a `minerals-pg-backup-creds`
SealedSecret. See [`README.md`](./README.md) for the deploy split and
[`example/prod/`](./example/prod/) for the manifests that produce
these artifacts.

> **Pairs with**: [`secrets.md`](./secrets.md) (the
> `minerals-pg-backup-creds` row), [`README.md`](./README.md) (Flux
> reconcile flow, base/overlay split). CloudNativePG's upstream docs
> at <https://cloudnative-pg.io/documentation/current/recovery/> are
> the source of truth for the `bootstrap.recovery` API surface — this
> file is the project-specific runbook layered on top.

---

## When to use which recovery

Pick the right mode for the failure. Recovery cost grows down the
table; if a cheaper option fits, use it.

| Symptom | Mode | What you do |
|---|---|---|
| A single table was wiped by an accidental `DELETE` / `UPDATE` minutes or hours ago | **Point-in-time, side-by-side restore** | Spin up a recovery cluster as of `T-1m`, dump the table, COPY it back into prod. |
| A bad migration left the schema or data unusable | **Point-in-time restore** to just before the migration | Same as above, then either promote the recovery cluster or dump+restore the affected objects into prod. |
| Whole-DB corruption (filesystem damage, accidental `DROP DATABASE`, ransomware on the PG PVC) | **Replace-cluster restore** | Delete the broken `Cluster`, recreate from the latest valid backup. |
| Cluster + MinIO both lost (cluster destroyed, node disk dead) | **Covered only with an external backup target.** | In-cluster MinIO can't help (same fate-sharing domain). Switch the DB backup to off-cluster B2 first — see ["Backblaze B2 setup"](#backblaze-b2-setup-worked-external-example); then this is a Mode B restore from B2. |

The first two modes share the same recipe: create a **new** `Cluster`
in a throwaway namespace that bootstraps from the backup, then move
the data you need from the recovery cluster into prod. The third mode
replaces the prod `Cluster` in place.

Do the side-by-side restore by default — recovering into a separate
namespace is reversible (you delete the throwaway), keeps prod
serving traffic, and lets you compare recovered data against current
data before merging.

---

## Prerequisites

Before any recovery, confirm:

```bash
# 1. The backup is healthy and recent.
kubectl -n mineral-prod get backups.postgresql.cnpg.io
#   Look for a recent `Phase: completed` entry.

# 2. CNPG sees the backup destination as configured.
kubectl -n mineral-prod get cluster minerals-pg \
  -o jsonpath='{.status.conditions[?(@.type=="ContinuousArchiving")].status}{"\n"}'
#   Expect: True

# 3. The bucket contains base + WAL artifacts.
kubectl -n mineral-prod run --rm -it mc-check --image=minio/mc:latest \
  --restart=Never -- sh -c '
    . /etc/minio-config/config.env
    mc alias set local http://minerals-hl:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
    mc ls -r local/minerals-postgres-backups/minerals-pg/ | head
' --overrides='{"spec":{"volumes":[{"name":"mc","secret":{"secretName":"minerals-minio-config"}}],"containers":[{"name":"mc-check","image":"minio/mc:latest","volumeMounts":[{"name":"mc","mountPath":"/etc/minio-config","readOnly":true}]}]}}'
#   Expect a `base/` and `wals/` listing under minerals-pg/.

# 4. You can read the backup creds (you'll reuse this Secret in the
#    recovery namespace via copy).
kubectl -n mineral-prod get secret minerals-pg-backup-creds \
  -o yaml > /tmp/minerals-pg-backup-creds.yaml
```

If any check fails, stop and fix the backup pipeline first — recovering
from a half-broken bucket eats time without helping.

---

## Mode A: Side-by-side restore into a throwaway namespace

This is the **default**. It is the same recipe used for the dry-run
rehearsal at the end of this document.

### 1. Create the throwaway namespace and copy the backup creds in

```bash
NS=mineral-recover-$(date +%Y%m%d-%H%M)
kubectl create namespace "$NS"

# Copy the backup-creds Secret. The SealedSecret in the prod overlay
# is namespace-scoped (ciphertext bound to `mineral-prod`), so we use
# the controller-decrypted plaintext Secret here, not the sealed one.
kubectl -n mineral-prod get secret minerals-pg-backup-creds -o yaml \
  | sed -e "s/namespace: mineral-prod/namespace: $NS/" \
        -e '/resourceVersion:/d' -e '/uid:/d' -e '/creationTimestamp:/d' \
        -e '/ownerReferences:/,/^[^ ]/d' \
  | kubectl apply -f -
```

### 2. Decide the recovery target

Two knobs:

- `bootstrap.recovery.source` — the name of a `Cluster` definition
  inside `externalClusters[]` describing where the backup is.
- `bootstrap.recovery.recoveryTarget.targetTime` — optional. If unset,
  recovery replays all available WAL and produces a cluster
  consistent with the latest archived WAL position. Set it to roll
  back to a specific moment (e.g. just before a bad migration).

`targetTime` accepts RFC3339 UTC: `"2026-05-18T14:32:00Z"`.

### 3. Apply the recovery Cluster

Save this as `/tmp/recover.yaml` and adjust the namespace + (optional)
`recoveryTarget`:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: minerals-pg-recover
  namespace: REPLACE_NS
spec:
  instances: 1
  storage:
    size: 10Gi
  # Same resource footprint as prod — recovery replay is CPU-light but
  # I/O-heavy; matching prod sizing keeps the rehearsal honest.
  resources:
    requests: { memory: 256Mi, cpu: 100m }
    limits:   { memory: 512Mi, cpu: 500m }

  bootstrap:
    recovery:
      source: minerals-pg-source
      # Uncomment to replay only up to a specific moment (UTC):
      # recoveryTarget:
      #   targetTime: "2026-05-18T14:32:00Z"

  externalClusters:
    - name: minerals-pg-source
      barmanObjectStore:
        # MUST match the prod Cluster's destinationPath EXACTLY.
        # The serverName under the bucket is derived from the source
        # cluster's name; CNPG looks for s3://<bucket>/<serverName>/.
        serverName: minerals-pg
        destinationPath: s3://minerals-postgres-backups/
        endpointURL: http://minerals-hl.mineral-prod.svc:9000
        s3Credentials:
          accessKeyId:
            name: minerals-pg-backup-creds
            key: ACCESS_KEY_ID
          secretAccessKey:
            name: minerals-pg-backup-creds
            key: SECRET_ACCESS_KEY
        wal:
          compression: gzip
        data:
          compression: gzip
```

> **endpointURL note**: from a different namespace you must use the
> FQDN (`minerals-hl.mineral-prod.svc:9000`), not the short
> `minerals-hl:9000` the prod Cluster uses. Same service, longer DNS.

Apply:

```bash
sed -i "s/REPLACE_NS/$NS/" /tmp/recover.yaml
kubectl apply -f /tmp/recover.yaml
```

### 4. Watch recovery

```bash
kubectl -n "$NS" get cluster minerals-pg-recover -w
# Phases: Initializing → Setting up primary → Cluster in healthy state.

kubectl -n "$NS" logs -l postgresql=minerals-pg-recover -c bootstrap-controller --tail=200
# Bootstrap log shows base download + WAL replay; failures here are
# almost always credential/bucket-path mismatches.
```

When healthy: `kubectl -n "$NS" exec -it minerals-pg-recover-1 -c postgres -- psql -U postgres -d minerals -c '\dt'` should list the recovered tables.

### 5. Extract what you need, then merge into prod

Single table:

```bash
# Dump the recovered table to a SQL file.
kubectl -n "$NS" exec minerals-pg-recover-1 -c postgres -- \
  pg_dump -U postgres -d minerals -t public.<table> --data-only \
  > /tmp/<table>.sql

# Apply to prod inside a transaction so a partial COPY rolls back.
kubectl -n mineral-prod exec -i minerals-pg-1 -c postgres -- \
  psql -U postgres -d minerals -1 < /tmp/<table>.sql
```

The `--data-only` flag preserves the prod schema (it may have moved
forward since the backup); only rows are restored. For schema
restoration, drop `--data-only` and review the resulting SQL before
applying.

Whole database (rare; usually you want Mode B instead):

```bash
kubectl -n "$NS" exec minerals-pg-recover-1 -c postgres -- \
  pg_dump -U postgres -d minerals -Fc > /tmp/minerals.dump
# Then point the app at the recovered cluster (Mode B / promotion)
# rather than restoring 10Gi over a live database.
```

### 6. Tear the throwaway namespace down

```bash
kubectl delete namespace "$NS"
```

This deletes the recovery Cluster, its PVC, and the copied Secret in
one step. The backup bucket is untouched (the recovery cluster only
READS from it).

---

## Mode B: Replace-cluster restore (catastrophic)

Use only when prod is unrecoverable in place — corruption that
`pg_dump`-from-recovery-cluster can't fix, or accidental
`DROP DATABASE` on the live cluster. **This stops the app until the
recovery completes.**

1. Scale the app to zero so it doesn't write to the doomed cluster:
   ```bash
   kubectl -n mineral-prod scale deploy/minerals --replicas=0
   ```
2. Take a final backup (best-effort — may fail if the cluster is
   broken; that's OK, we're about to throw it away):
   ```bash
   kubectl -n mineral-prod create -f - <<'EOF'
   apiVersion: postgresql.cnpg.io/v1
   kind: Backup
   metadata: { name: pre-restore-snapshot, namespace: mineral-prod }
   spec:
     cluster: { name: minerals-pg }
   EOF
   ```
3. Delete the prod Cluster and its PVC. Flux will recreate the
   Cluster from the manifest on next reconcile, **but with the
   bootstrap section unchanged it would re-initdb on an empty
   PVC** — destroying any chance of in-place repair. So before
   deleting, edit `kustomize/base/postgres.yaml` (in a temporary
   branch + revert PR) to replace the `bootstrap.initdb` block with
   a `bootstrap.recovery` block pointing at the same backup store
   as the recovery cluster above. Promote that branch with the
   `Promote to prod` workflow.
4. Delete the broken Cluster + PVC:
   ```bash
   kubectl -n mineral-prod delete cluster minerals-pg
   kubectl -n mineral-prod delete pvc -l cnpg.io/cluster=minerals-pg
   ```
5. Flux reconciles, CNPG sees `bootstrap.recovery`, downloads the
   base + replays WAL, and produces a fresh `minerals-pg-1` pod
   with the recovered data.
6. After the cluster reports healthy and a smoke test passes
   (`psql ... -c '\dt'` plus an app-level health check), revert
   the bootstrap edit so the manifest goes back to `initdb` (so a
   future env stand-up doesn't accidentally recovery-bootstrap from
   prod's bucket).
7. Scale the app back up:
   ```bash
   kubectl -n mineral-prod scale deploy/minerals --replicas=1
   ```

---

## Dry-run / rehearsal

Run this **quarterly at minimum** (or after any change to the backup
config) so the recipe doesn't bit-rot. The procedure is exactly Mode A
with no merge-into-prod step at the end:

1. `NS=mineral-recover-drill-$(date +%Y%m%d)`
2. Create the namespace, copy the backup creds, apply the recovery
   `Cluster` manifest from Mode A step 3 (omit `recoveryTarget` to
   restore to the latest WAL).
3. Wait for the recovery Cluster to reach healthy. Time how long.
   Record the result in the runbook or a follow-up bead.
4. Spot-check one expected table:
   ```bash
   kubectl -n "$NS" exec minerals-pg-recover-1 -c postgres -- \
     psql -U postgres -d minerals -c 'SELECT COUNT(*) FROM <table>;'
   ```
5. Tear it down: `kubectl delete namespace "$NS"`.

Two things to verify and log:

- **Recovery time** — `Cluster created` to `Phase: Cluster in healthy state`.
  Trends in this number signal bucket-latency or backup-size issues
  before they show up in a real incident.
- **Bucket reachability from a different namespace** — uses the FQDN
  endpoint, exercises the SealedSecret copy, exposes any
  NetworkPolicy that quietly blocks cross-namespace MinIO traffic.

---

## Migrating to external storage

When in-cluster MinIO is no longer enough (scale, regulatory, RPO/RTO
demands), swapping the backup destination is a contained change:

1. Stand up the external bucket (AWS S3, Backblaze B2, etc.) with
   the same access-control model: a dedicated user/key with scoped
   write access to ONLY the backup bucket.
2. Seal the external creds into a new `minerals-pg-backup-creds`
   ciphertext (same Secret name, same key contract — only the
   plaintext values change).
3. Edit the prod overlay's Cluster patch (`mineral.yaml`):
   - `destinationPath`: `s3://<external-bucket>/`
   - `endpointURL`: the external provider's S3 endpoint (omit
     entirely for AWS S3 — boto-style defaults take over).
   - Optionally add `s3Credentials.region` and a `Region` field if
     the provider requires it.
4. Delete the in-cluster `postgres-backup-bucket-init` Job and
   remove it from `kustomization.yaml` — the external bucket is
   created out of band.
5. After the next ScheduledBackup completes against the new
   destination AND a rehearsal recovery succeeds from the new
   destination, decommission the in-cluster MinIO bucket
   (`mc rb --force --dangerous local/minerals-postgres-backups`).

The CNPG Cluster's `backup` block stays the same shape; only
`destinationPath` + `endpointURL` + sealed creds change. File a fresh
bead at that time.

---

## Backblaze B2 setup (worked external example)

This is the concrete, copyable instance of "Migrating to external
storage" for **Backblaze B2** — the recommended primary DB-backup
target for public prod (V3 prerequisite, mi-lhsu). B2 is S3-compatible,
~$6/TB/month, with free egress to Cloudflare. The example manifests
live in
[`example/prod/external-b2/`](./example/prod/external-b2/): a B2 Cluster
patch (`mineral-cluster-patch.b2.yaml`) and a B2 SealedSecret stub
(`minerals-pg-b2-creds.yaml`). This section is the operator runbook for
applying them.

> The in-cluster-MinIO example stays the default in the repo. B2 is the
> documented external alternative; switching is the two-field swap below.

### 1. Create the B2 bucket + a bucket-scoped application key (manual)

Operator step, out of band (B2 buckets are not created by an in-cluster
Job — so when you adopt B2 you also drop
`postgres-backup-bucket-init.yaml` from the prod
[`kustomization.yaml`](./example/prod/kustomization.yaml)).

```bash
# Create a private bucket.
b2 create-bucket your-b2-bucket-name allPrivate

# Confirm "Keep all versions" (versioning) is on — this is what defends
# backup artifacts against silent overwrite/delete. Optionally add a
# lifecycle rule to bound storage growth alongside the Cluster's 30d
# retentionPolicy.

# Create an application key scoped to ONLY this bucket (NOT the master
# key). read+write is enough for Barman.
b2 create-key --bucket your-b2-bucket-name minerals-pg-backup \
  listBuckets,listFiles,readFiles,writeFiles,deleteFiles
#   -> prints: <applicationKeyId> <applicationKey>
#      applicationKey is shown ONCE — record it now.
```

### 2. Note the B2 S3 endpoint + region

B2's S3 endpoint is `https://s3.<region>.backblazeb2.com`. The
`<region>` comes from the bucket (B2 console, or `b2 get-bucket
your-b2-bucket-name`), e.g. `us-west-004`. Unlike AWS, the region is
**not** optional — it is encoded in the endpoint host. The aws-sdk
Barman uses derives virtual-hosted-style URLs
(`<bucket>.s3.<region>.backblazeb2.com`) from this endpoint; B2 supports
that, so no path-style override is needed.

### 3. Seal the B2 creds into `minerals-pg-backup-creds`

Same Secret name + key contract as the in-cluster example, so only the
plaintext values change (see
[`example/prod/external-b2/minerals-pg-b2-creds.yaml`](./example/prod/external-b2/minerals-pg-b2-creds.yaml)
and [`encrypt.md`](./encrypt.md)):

```bash
# .sec/minerals-pg-backup-creds.yaml (plaintext, NOT committed):
#   stringData:
#     ACCESS_KEY_ID:     <applicationKeyId>
#     SECRET_ACCESS_KEY: <applicationKey>
kubeseal --scope strict --format yaml \
  < .sec/minerals-pg-backup-creds.yaml \
  > example/prod/external-b2/minerals-pg-b2-creds.yaml  # then commit the sealed file
```

### 4. Swap the Cluster patch to B2

Replace the in-cluster-MinIO `Cluster` patch block in
[`example/prod/mineral.yaml`](./example/prod/mineral.yaml) with the B2
block from
[`example/prod/external-b2/mineral-cluster-patch.b2.yaml`](./example/prod/external-b2/mineral-cluster-patch.b2.yaml)
(`destinationPath: s3://your-b2-bucket-name/`, `endpointURL:
https://s3.<region>.backblazeb2.com`). Keep
[`scheduledbackup.yaml`](./example/prod/scheduledbackup.yaml) (cadence
is storage-independent). Promote with the `Promote to prod` workflow.

### 5. Verify, then decommission the old in-cluster bucket

```bash
# After the next ScheduledBackup completes against B2:
kubectl -n mineral-prod get backups.postgresql.cnpg.io
#   Look for a recent Phase: completed entry written to B2.

kubectl -n mineral-prod get cluster minerals-pg \
  -o jsonpath='{.status.conditions[?(@.type=="ContinuousArchiving")].status}{"\n"}'
#   Expect: True
```

Then run the **restore-from-B2 dry run** below. Only after a recovery
succeeds from B2, decommission the in-cluster bucket
(`mc rb --force --dangerous local/minerals-postgres-backups`).

### Restore from B2

Recovery is **identical to [Mode A](#mode-a-side-by-side-restore-into-a-throwaway-namespace)**;
only the `externalClusters[].barmanObjectStore` endpoint + path point at
B2, and the creds Secret you copy into the throwaway namespace holds the
B2 application key. In the Mode A step-3 manifest, change:

```yaml
  externalClusters:
    - name: minerals-pg-source
      barmanObjectStore:
        serverName: minerals-pg
        destinationPath: s3://your-b2-bucket-name/
        endpointURL: https://s3.your-region.backblazeb2.com
        s3Credentials:
          accessKeyId:    { name: minerals-pg-backup-creds, key: ACCESS_KEY_ID }
          secretAccessKey: { name: minerals-pg-backup-creds, key: SECRET_ACCESS_KEY }
        wal:  { compression: gzip }
        data: { compression: gzip }
```

The B2 endpoint is reachable from any namespace (it's external — no
in-cluster FQDN nuance, no cross-namespace NetworkPolicy concern that
the MinIO path has). The bucket-reachability prerequisite check
(["Prerequisites"](#prerequisites) step 3) becomes a B2-side `b2 ls`
or `mc ls` against the B2 alias instead of the in-cluster MinIO `mc`.

A restore-from-B2 dry run into a throwaway namespace is the acceptance
gate for adopting B2 — run it before decommissioning the MinIO bucket
(step 5 above), and quarterly thereafter per
["Dry-run / rehearsal"](#dry-run--rehearsal).

---

## Out of scope

These failures need work beyond what mi-jgzi shipped:

- **Total cluster loss** (node + MinIO PVC + cluster all destroyed).
  In-cluster MinIO is on the same fate-sharing domain as Postgres. Fix:
  off-cluster backup destination — the worked example is ["Backblaze B2
  setup"](#backblaze-b2-setup-worked-external-example) (recommended for
  V3 public prod); the generic recipe is ["Migrating to external
  storage"](#migrating-to-external-storage).
- **Backup-bucket integrity attack**. Versioning gives object-level
  rollback but the bucket itself can still be wiped by an attacker
  with bucket-admin creds. The dedicated `minerals-pg-backup-creds`
  user is object-scoped, not admin-scoped, which limits the blast
  radius — but the MinIO root credentials still have full access.
  Fix: cross-cluster replication of the backup bucket.
- **Automated restore-rehearsal in CI**. The quarterly drill above is
  manual. Worth automating; not in scope for this bead.

---

## Cross-references

- Manifests that produce this setup:
  [`example/prod/mineral.yaml`](./example/prod/mineral.yaml) (Cluster
  backup patch), [`example/prod/scheduledbackup.yaml`](./example/prod/scheduledbackup.yaml),
  [`example/prod/postgres-backup-bucket-init.yaml`](./example/prod/postgres-backup-bucket-init.yaml),
  [`example/prod/minerals-pg-backup-creds.yaml`](./example/prod/minerals-pg-backup-creds.yaml).
- External-B2 backup variant (the documented alternative):
  [`example/prod/external-b2/`](./example/prod/external-b2/)
  (`mineral-cluster-patch.b2.yaml`, `minerals-pg-b2-creds.yaml`, `README.md`).
- Secret inventory (key contract, provisioning steps):
  [`secrets.md`](./secrets.md).
- kubeseal mechanics:
  [`encrypt.md`](./encrypt.md).
- CNPG upstream recovery API:
  <https://cloudnative-pg.io/documentation/current/recovery/>.
- CNPG upstream backup API (Barman / S3 options):
  <https://cloudnative-pg.io/documentation/current/backup/>.
