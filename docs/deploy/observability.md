# Observability: metrics, alerts, dashboards, routing

This is the operator's guide to **watching minerals in production**. It
extends the metrics foundation documented in
[`README.md` § Observability](./README.md#observability) (mi-2b1k — the
`/metrics` endpoint + `ServiceMonitor`) with the piece that closes the
"scraped but nobody's watching" gap (mi-vp0w):

- **Alerting rules** — `PrometheusRule` for the failure modes already seen.
- **Alert routing** — `AlertmanagerConfig` deciding where alerts go.
- **Dashboards** — a Grafana overview dashboard.

> **Prerequisite:** a Prometheus-Operator stack (e.g. kube-prometheus-stack)
> running in the cluster, already scraping minerals via the ServiceMonitor.
> Verify scraping first (`up{job="minerals"} == 1`) — every alert here
> assumes the target is being scraped.

---

## What ships, and where

All three live in the **example overlay**, mirroring how the operator's
gitops repo is laid out (the base in this repo declares *what the app is*;
per-env operational config lives in the overlay — see
[README § The base/overlay split](./README.md#the-baseoverlay-split)):

| File | Kind | Env(s) | Purpose |
|------|------|--------|---------|
| [`example/prod/prometheusrule.yaml`](./example/prod/prometheusrule.yaml) | `PrometheusRule` | prod | Alert rules (critical + warning). |
| [`example/staging/prometheusrule.yaml`](./example/staging/prometheusrule.yaml) | `PrometheusRule` | staging | Same coverage, warning-only, no backup rules. |
| [`example/prod/alertmanagerconfig.yaml`](./example/prod/alertmanagerconfig.yaml) | `AlertmanagerConfig` | prod | Routes alerts → email + webhook. |
| [`example/prod/minerals-alertmanager-smtp.yaml`](./example/prod/minerals-alertmanager-smtp.yaml) | `SealedSecret` | prod | SMTP password for the email receiver. |
| [`example/prod/grafana-dashboard.yaml`](./example/prod/grafana-dashboard.yaml) | `ConfigMap` | prod | Grafana overview dashboard (sidecar-imported). |

Each is wired into its overlay's `kustomization.yaml`.

> **The three selectors that fail silently.** ServiceMonitor, PrometheusRule,
> and AlertmanagerConfig are each picked up by the operator only if a
> selector on the Prometheus/Alertmanager CR matches a label on the CRD.
> The examples all use `release: kube-prometheus-stack` (that chart's
> default). If your stack uses different selectors, the CRDs apply cleanly
> but are **never loaded** — no error, no effect. Verify all three:
>
> ```bash
> kubectl get prometheus -A -o jsonpath='{range .items[*]}{.metadata.name}: smSel={.spec.serviceMonitorSelector} ruleSel={.spec.ruleSelector}{"\n"}{end}'
> kubectl get alertmanager -A -o jsonpath='{range .items[*]}{.metadata.name}: cfgSel={.spec.alertmanagerConfigSelector}{"\n"}{end}'
> ```

---

## The metric sources behind the alerts

The app exposes a **thin** metric set (mi-2b1k + the V2 BFF session
histograms): `minerals_session_active`,
`minerals_session_lookup_duration_seconds`,
`minerals_session_refresh_duration_seconds`, and the default Go/process
collectors. It does **not** yet emit per-endpoint HTTP request/status
counters or a DB-pool connection gauge.

So most alerts are built on metrics the **cluster** already collects, not
the app:

| Failure mode | Alert(s) | Metric source |
|---|---|---|
| Pod down / unscrapeable | `MineralsTargetDown` | Prometheus `up` |
| Pod NotReady (readyz failing — the mi-hkh6 incident) | `MineralsPodNotReady`, `MineralsReadinessFlapping` | kube-state-metrics |
| 5xx rate / 429 rate / latency | `MineralsHighErrorRate`, `MineralsHigh429Rate`, `MineralsHighLatency` | **ingress controller** (see caveat below) |
| DB-pool saturation (proxy) | `MineralsSessionLookupSlow` | app `/metrics` |
| Restarts / CrashLoop / OOM / memory | `MineralsContainerRestarting`, `MineralsCrashLooping`, `MineralsOOMKilled`, `MineralsMemoryNearLimit` | kube-state-metrics + cAdvisor |
| Backup freshness (mi-lhsu) | `MineralsBackupTooOld`, `MineralsBackupMissing` | CloudNativePG (`cnpg_collector_*`) |
| TLS cert expiry | `MineralsCertExpiringSoon/Critical` | cert-manager |

An alert whose source metric isn't scraped is **indistinguishable from
healthy** — it just never fires. After applying, confirm each rule's
metric returns series in Prometheus (§ Verifying alerts have data).

### DB-pool saturation has no direct signal yet

The mi-hkh6 incident was DB-pool exhaustion surfacing as failing readyz.
There is no `pgxpool` connections gauge today, so it is covered two ways:
(1) `MineralsPodNotReady` catches the end state (readyz fails), and (2)
`MineralsSessionLookupSlow` catches the leading edge — every authenticated
request does a Postgres session lookup, so its p99 latency spikes as the
pool contends. The follow-up bead below adds a direct gauge.

---

## Request-rate alerts and the ingress controller

⚠ **The 5xx / 429 / latency alerts assume ingress-nginx.** They use
`nginx_ingress_controller_*` metrics. The example `Ingress` deliberately
leaves `ingressClassName` unset (stock k3s ships **Traefik**, not
ingress-nginx — see [README § Optional add-ons](./README.md#optional-add-ons-deliberately-omitted-from-the-example)).
**On a Traefik cluster these three alerts silently never fire.** Pick one:

**Option A — ingress-nginx (no rule edits).** Install ingress-nginx, set
`ingressClassName: nginx` on the overlay's `ingress.yaml`, and ensure the
controller's ServiceMonitor is scraped (the ingress-nginx chart ships one;
enable `controller.metrics.enabled=true` and
`controller.metrics.serviceMonitor.enabled=true`).

**Option B — Traefik.** Enable Traefik's Prometheus metrics
(`--metrics.prometheus=true`, scraped by a ServiceMonitor) and replace the
three `nginx_*` expressions:

```promql
# 5xx ratio  (MineralsHighErrorRate)
sum(rate(traefik_service_requests_total{service=~"mineral-prod-minerals-.*", code=~"5.."}[5m]))
/ sum(rate(traefik_service_requests_total{service=~"mineral-prod-minerals-.*"}[5m])) > 0.05

# 429 rate   (MineralsHigh429Rate)
sum(rate(traefik_service_requests_total{service=~"mineral-prod-minerals-.*", code="429"}[5m])) > 0.5

# p95 latency (MineralsHighLatency)
histogram_quantile(0.95,
  sum by (le) (rate(traefik_service_request_duration_seconds_bucket{service=~"mineral-prod-minerals-.*"}[5m]))) > 1
```

(Traefik's `service` label is `<namespace>-<ingress>-<svc>@kubernetes` —
confirm the exact value in Prometheus and tighten the matcher.)

**Option C — best long-term: app-native metrics.** Once the follow-up bead
adds HTTP request/status/latency counters to the app, rewrite this group
against them. App metrics are controller-agnostic and already scraped by
the existing ServiceMonitor — no extra exporter, no silent-no-fire trap.

---

## Alert routing — where alerts GO

[`alertmanagerconfig.yaml`](./example/prod/alertmanagerconfig.yaml) routes
by the `severity` label the rules set:

- **critical** → email (operator inbox) **and** a webhook, 1h repeat.
- **warning** → webhook only, 4h repeat (no inbox noise).

The webhook example targets an [ntfy](https://ntfy.sh) topic (self-hostable,
trivial); swap the URL for a Slack/Discord incoming webhook or a Cloudflare
Worker. The only secret is the SMTP password, supplied via the
`minerals-alertmanager-smtp` SealedSecret — generate it per the comments in
that file (and [`encrypt.md`](./encrypt.md)). Webhook-only? Drop the
`emailConfigs` block and the secret entirely.

Staging rules are **warning-only**; route the `mineral-staging` namespace to
a low-priority receiver (or leave on the default) so pre-prod noise never
pages.

### Solo-ops minimum

One email destination satisfies the acceptance criteria. Everything else
(webhook, severity split, per-env routing) is refinement you can add later.

---

## Dashboards

[`grafana-dashboard.yaml`](./example/prod/grafana-dashboard.yaml) is a
`ConfigMap` labelled `grafana_dashboard: "1"`. The kube-prometheus-stack
Grafana runs a **sidecar** that watches for that label cluster-wide and
imports the JSON automatically — no API call, no manual upload. Confirm the
sidecar is enabled:

```bash
kubectl -n monitoring get deploy -l app.kubernetes.io/name=grafana -o yaml \
  | grep -A2 -i 'sidecar\|LABEL_DASHBOARD'
```

The dashboard ("Minerals — Overview") has a `namespace` template variable
(default `mineral-prod`) so one ConfigMap serves any env — staging
operators just switch the variable. Panels: pods-ready / scrape-up / active
sessions / restarts, request rate by status, 5xx ratio, latency
percentiles, 429 rate, session-lookup latency, and memory-vs-limit. The
request panels share the ingress-nginx assumption above.

---

## Testing an alert end-to-end

The acceptance bar (mi-vp0w) is **a test alert fires end-to-end** — proving
rule → Alertmanager → destination actually works, not just that the YAML
applied. Cheapest reliable rehearsal:

1. **Trip a rule deliberately.** `MineralsHigh429Rate` is easy if you can
   generate load; `MineralsMemoryNearLimit` / `MineralsTargetDown` work by
   scaling or pausing the pod in a maintenance window. Or temporarily add a
   throwaway always-true rule to the PrometheusRule:

   ```yaml
   - alert: MineralsAlertingSelfTest
     expr: vector(1)
     for: 1m
     labels: { severity: critical, app: minerals }
     annotations:
       summary: "minerals alerting self-test — DELETE after confirming delivery"
   ```

2. **Watch it climb the stack:**
   ```bash
   # Rule loaded + firing:
   kubectl -n monitoring port-forward svc/<prometheus> 9090
   #   UI → Alerts → MineralsAlertingSelfTest → Pending → Firing
   # Reached Alertmanager + routed:
   kubectl -n monitoring port-forward svc/<alertmanager> 9093
   #   UI → the alert is grouped under the minerals-critical receiver
   ```

3. **Confirm delivery** — the email lands / the webhook posts.

4. **Remove the self-test rule** and re-apply. Leaving `vector(1)` firing
   forever trains operators to ignore the channel.

### Verifying alerts have data

A rule with no underlying series never fires. Spot-check the load-bearing
metrics exist after install:

```promql
up{job="minerals"}                                             # scrape health
kube_pod_status_ready{namespace="mineral-prod"}                # kube-state-metrics present
minerals_session_lookup_duration_seconds_bucket               # app histograms scraped
cnpg_collector_last_available_backup_timestamp{cluster="minerals-pg"}  # CNPG metrics
certmanager_certificate_expiration_timestamp_seconds{namespace="mineral-prod"}  # cert-manager
nginx_ingress_controller_requests                             # ingress metrics (or Traefik equiv)
```

Any that return *no data* mean the corresponding alerts are dormant — wire
up that exporter or adjust the rule.

---

## Follow-up: app-native HTTP & pool metrics

The thin metric set forces two compromises documented above: 5xx/429/latency
come from the ingress controller (silent-no-fire on the wrong controller),
and DB-pool saturation is only a latency proxy. The durable fix is to emit
these from the app itself:

- an HTTP middleware request counter (`minerals_http_requests_total` by
  method/route/status) + duration histogram, and
- a `pgxpool` stats collector (acquired / idle / max connections).

Then the request-rate and pool-saturation alerts become controller-agnostic
and are scraped by the existing ServiceMonitor with no extra exporter. This
is filed as a follow-up bead (see mi-vp0w notes) — out of scope for the
gitops-only delivery here, but the highest-value observability improvement
after this lands.

---

## Cross-references

- Metrics foundation (`/metrics`, admin port, ServiceMonitor): [README § Observability](./README.md#observability).
- Rate limiting (the 429 source): [`rate-limiting.md`](./rate-limiting.md).
- Backups + restore (the backup-freshness alert): [`restore.md`](./restore.md).
- SealedSecret workflow (SMTP secret): [`encrypt.md`](./encrypt.md).
- Design rationale for the two-port/metrics model: [`../design/07-build-embed-observability.md`](../design/07-build-embed-observability.md).
