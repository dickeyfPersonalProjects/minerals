# External Postgres backup → Backblaze B2 (example)

This directory is the **external-storage variant** of the CNPG/Barman
Postgres backup. The default, in-repo prod example backs up to
**in-cluster MinIO** (see [`../mineral.yaml`](../mineral.yaml) Cluster
patch, [`../scheduledbackup.yaml`](../scheduledbackup.yaml),
[`../postgres-backup-bucket-init.yaml`](../postgres-backup-bucket-init.yaml),
[`../minerals-pg-backup-creds.yaml`](../minerals-pg-backup-creds.yaml)).
That stopgap protects against bad migrations and accidental
`DELETE`/`UPDATE`, but it lives on the same cluster as Postgres, so it
does **not** survive catastrophic cluster loss.

These files show the worked example of the "two-field swap" the
in-cluster example's comment describes — pointing `barmanObjectStore`
at **Backblaze B2** (external, S3-compatible, off-cluster) instead.
B2 is cheap (~$6/TB/month, free egress to Cloudflare and many CDNs),
which makes it the right primary target for the DB backup once prod
goes public (V3 prerequisite, mi-lhsu).

> **This is example/docs only.** The operator copies these into
> fleet-infra, fills in the real B2 bucket/region, and seals real
> credentials with `kubeseal`. Nothing here is applied directly and
> no real secret material is committed.

## What's here

| File | What it is |
|---|---|
| [`mineral-cluster-patch.b2.yaml`](./mineral-cluster-patch.b2.yaml) | The B2 variant of the `Cluster` `backup.barmanObjectStore` patch. Drop-in replacement for the in-cluster-MinIO patch block in [`../mineral.yaml`](../mineral.yaml). |
| [`minerals-pg-b2-creds.yaml`](./minerals-pg-b2-creds.yaml) | SealedSecret stub for the B2 application key (keyID + applicationKey), same Secret name + key contract as the MinIO creds so the Cluster patch is the only thing that changes. |

## How the swap works

1. Operator creates the B2 bucket + a bucket-scoped application key,
   enables versioning (operator manual steps — see
   [`../../../restore.md`](../../../restore.md) → "Backblaze B2 setup").
2. Seal the B2 creds into `minerals-pg-backup-creds` (same Secret name
   the in-cluster example uses — only the plaintext values differ).
   This stub documents the key contract; see
   [`../../../encrypt.md`](../../../encrypt.md) for kubeseal mechanics.
3. Replace the in-cluster-MinIO Cluster patch block in `mineral.yaml`
   with the B2 block from `mineral-cluster-patch.b2.yaml`.
4. Drop the in-cluster bucket-init Job: remove
   `postgres-backup-bucket-init.yaml` from
   [`../kustomization.yaml`](../kustomization.yaml) — the B2 bucket is
   created out of band, not by an in-cluster Job. Keep
   `scheduledbackup.yaml` (cadence is storage-independent).

The `ScheduledBackup` cadence (weekly base + continuous WAL) is
unchanged: it references the `Cluster`, not the storage backend.

## Cost model

- **DB → B2 (external): cheap.** Backups are gzip-compressed base +
  WAL; even with 30-day retention this is small. ~$6/TB/month, free
  egress to Cloudflare.
- **Image objects: NOT here.** Images are large; external storage cost
  would be high. They get a **zero-cost local mirror bucket** instead
  (delete-marker replication off) — tracked separately in **mi-a3pt**.
  External image backup is deferred until budget justifies it.
