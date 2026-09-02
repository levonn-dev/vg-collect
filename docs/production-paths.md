# Production paths

Every datastore, secret store, and TLS chain in this repo has a
credential-free or self-signed dev stand-in so the whole stack runs
locally with zero setup. This document maps each dev-tier shortcut to
its production equivalent: what changes, and why, layer by layer.

## Datastores to managed services

Postgres moves to RDS for PostgreSQL, one instance per service:
Multi-AZ, KMS at rest, IAM database authentication, automated backups
plus PITR, parameter groups as code, and Performance Insights. Whether
an RDS Proxy sits in front of each instance or a service keeps pooling
through in-cluster PgBouncer is a per-service call: the Go pgx pools
already in use are fine either way, and a Proxy mainly buys failover
smoothing.

Every Postgres-backed chart (auth, user, collection, social,
enrichment) already carries a `postgres.enabled` flag: turning it off
drops the in-cluster StatefulSet, its postgres-exporter sidecar,
PodDisruptionBudget, ServiceMonitor, Certificate, and the pg-scoped
NetworkPolicy rule - all templated once in the shared `vg-lib` library
chart rather than duplicated per service. None of the five wires
`DATABASE_URL` to a dedicated values key at all - every deployment
template composes it unconditionally against the chart's own
`<service>-pg` in-cluster hostname - and the pg-ca volume mount is not
gated on `postgres.enabled` either, so flipping the flag off alone
strands the pod on a Certificate secret that no longer renders.
Pointing at RDS or CloudNativePG today therefore needs a chart change,
not a values override: a first-class `env.databaseUrl` key plus gating
the pg-ca volume on `postgres.enabled` is the natural next step once a
real Postgres target exists to test it against.

Valkey moves to ElastiCache running the Valkey engine, per service.

Both sit in a VPC with private subnets and per-pair security groups;
Terraform provisions them.

The in-cluster alternative to managed services is CloudNativePG per
service (replicas, failover, WAL to S3, PITR, Pooler) and
Sentinel-managed Valkey.

## Secrets

Production reuses the same ExternalSecret resources already defined in
every chart; only the backing store changes. The dev fake
ClusterSecretStore is swapped for AWS Secrets Manager, reached through
EKS Pod Identity (IRSA is the alternative where Pod Identity is not
available). The dev store is Tilt-templated, so a real environment must
provision its own (Cluster)SecretStore, since no chart here ships one.

## SPA delivery

Delivery moves behind Route53 to CloudFront (ACM, WAF), with split
origins: private S3 (OAC) for the hashed assets, and an NLB to APISIX
to the bff for everything under /api/*. Keeping both origins on the
same host preserves the existing cookie/CSRF model unchanged. CI syncs
assets (immutable cache-control, no-cache index.html, invalidate index
only). SPA routing falls back to index.html via a 403/404 mapping on
the distribution. The bff's security headers move 1:1 into a
response-headers policy, and the bff itself runs with
SERVE_STATIC=false since it no longer serves the bundle. A midpoint
without a CDN in front is a static-file pod plus an APISIX route split
between it and the bff.
The CI build step also owns the `VITE_SITE_*` site-identity variables
and `VITE_BUILD_VERSION` (documented in `.env.example`): set them in
the build environment before `npm run build`; the bundle carries them
from there. `VITE_BUILD_VERSION` typically gets the git SHA or release
tag, and lands on browser telemetry so an error or web-vitals
regression can be traced back to the build that shipped it -
[frontend.md](runbooks/frontend.md) covers the attribute.

## Observability hardening

Grafana disables anonymous auth, gets a real admin password, and
either runs HA (replicas plus a shared database) or moves
availability-critical rules to Prometheus evaluation, so paging does
not depend on Grafana's own uptime. Contact points (Slack/PagerDuty/SNS)
and their notification policies are provisioned in the same
provisioning tree as the dashboards and rules already are. Prometheus
gets longer retention on real storage classes. On the collector side,
the gateway scales horizontally (it is stateless) and keeps
memory_limiter regardless of scale. Per-datastore exporters should get
dedicated read-only monitoring credentials instead of the owner/root
users the dev tier reuses (for Valkey that first means adding
authentication at all: the dev-tier Valkey instances are TLS-only but
credential-less, so there is no existing credential to narrow). Jaeger v2 gets a real storage backend
(OpenSearch/Cassandra) instead of in-memory.

## Edge

The APISIX admin key ships as the chart's well-known default and must
be overridden in both apisix.admin.credentials.admin and the
ingress-controller adminKey together, since the two have to match.
etcd goes from a single dev replica to 3 replicas for quorum. The
admission webhook needs APISIX >= 3.17 to validate configs before they
are admitted. The bff NetworkPolicy's gateway admission is already
pod-scoped (namespace plus an APISIX podSelector), and the gateway
answers 404 on /healthz and /readyz instead of proxying them; note the
dev cluster's CNI (kindnet) does not enforce NetworkPolicies, so the
policies are proven by manifest, not by dev-cluster behavior.

## TLS

The self-signed dev ClusterIssuer chain swaps for a real CA (ACM
Private CA or a Vault issuer). The localhost SANs on the datastore
certs exist for the metrics sidecars and stay as they are: that
connection never leaves the pod either way.

## Cluster operations

The kps CRDs are applied server-side by bootstrap and are NOT upgraded
by helm: the helm show crds | kubectl apply --server-side line has to
be re-run by hand on every chart version bump, or the next upgrade
fails against stale CRDs. NetworkPolicies throughout the repo are
ingress-only; an egress-lockdown pass is the documented next hardening
step, not yet done. App pods and the trigger CronJobs run a
restricted securityContext (runAsNonRoot pinned to the image's numeric
nonroot uid so the kubelet can verify it - 65532 for the distroless
services, 100:101 for the curl-based CronJobs; a non-numeric image USER
alone fails admission - no privilege escalation, all capabilities
dropped, read-only root filesystem, RuntimeDefault seccomp) and their
ServiceAccounts do not automount a token; the
datastore StatefulSets stay root because the tls-perms init container
chowns cert material. A service that imports otelhttp directly (bff,
enrichment) pins its own version rather than trusting a
transitive resolution, because a transitively-resolved older version
once regressed response handling; a service that never imports it
directly (auth, collection, social, user) inherits the version through
libs/go/httpkit, which carries the same pin. A new service follows
whichever shape matches how it reaches otelhttp.

## CI

The coverage gate is enforced as a required status check via branch
protection, not just a step that can fail silently. Cluster e2e remains
local-only by design; a kind/k3d job is the extension point if that
ever needs to run in CI. Third-party actions are pinned to commit SHAs
(with a version comment) rather than moving tags, the workflow declares
`contents: read` least-privilege permissions, and the verify job carries
a timeout so a wedged step fails fast instead of holding a runner.
