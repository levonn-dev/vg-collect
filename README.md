# vg-collect

Video-game collection tracker with granular per-item detail: OIDC login,
IGDB metadata enrichment, PriceCharting market pricing, per-service
Postgres/MongoDB/Valkey datastores, full observability.

## Prerequisites

- go >=1.26
- docker
- kubectl
- helm >=3.14
- tilt >=0.33
- task >=3.38
- golangci-lint >=2.1
- node 22+ (with npm); for the browser smoke, `npx playwright install chromium`

## Quickstart

```bash
task bootstrap          # tool checks + .env scaffold (edit .env if you have real keys)
task bootstrap:cluster  # once per cluster: cert-manager, external-secrets, CA issuers
task run                # tilt up: builds images, deploys charts, port-forwards
```

Works against any local Kubernetes context (docker-desktop, kind, k3d, minikube);
add yours to `allow_k8s_contexts` in the Tiltfile if it's not listed.

## Dev commands

| Command | What |
|---|---|
| `task lint` | golangci-lint every Go module + helm lint every chart + eslint the frontend |
| `task test` | go test every module (testcontainers need Docker) + frontend vitest |
| `task test:cover` | tests + the 80% coverage gate (generated code and cmd/ wiring excluded) |
| `task gen` | regenerate OpenAPI server stubs/types + the frontend's typed API client |
| `task tidy` | go mod tidy every module |
| `task build` | compile every module + the frontend bundle |
| `task e2e` | Playwright login smoke against the running stack |
| `task run` / `task down` | tilt up / down |
| `task nuke` | full app-stack reset: tilt down + the vg-collect namespace (see Teardown) |

## Teardown

```bash
task down                    # stop the dev stack; namespace, datastore PVCs, issued TLS secrets survive
task nuke                    # app layer: tilt down + delete the vg-collect namespace (drops PVCs + TLS secrets)
task bootstrap:cluster:down  # remove the platform: helm uninstalls + the vg-platform namespace
```

Reinstalling the platform mints a fresh dev CA, and cert-manager does not
reissue previously issued certificates (they still match their Certificate
specs), so TLS secrets from before a platform-only teardown keep chaining
to the dead CA until their renewal window. For platform down/up cycles
prefer `task nuke && task bootstrap:cluster:down`, which drops those secrets
(and the datastore PVCs) with the vg-collect namespace; 
`task bootstrap:cluster && task run` then brings everything back from scratch.

Platform teardown removes the monitoring stack's owning helm release, but
the kps CRDs applied by hand in `task bootstrap:cluster` are not
helm-managed and remain until deleted by hand; this is harmless, and the
next `task bootstrap:cluster` re-adopts them.

## Edge and ports

The bff is the only public service. It is published through the APISIX
gateway, which applies per-IP rate limits: tight on `/api/auth/*` (the
credential-adjacent surface), looser on the rest of `/api/*`, none on
static assets. Everything the browser touches goes through that gateway;
the other services are reachable in dev only via Tilt port-forwards.

| Port | What |
|---|---|
| 8090 | APISIX gateway: the app's entrypoint (browser and Bruno `bff/`) |
| 8083 | bff, direct (bypasses the gateway; for debugging) |
| 8082 | auth, direct (Bruno `auth/` Bearer flows) |
| 8081 | user, direct (Bruno `user/` Bearer flows) |
| 8084 | enrichment, direct (Bruno `enrichment/` Bearer flows) |
| 8085 | collection, direct (Bruno `collection/` Bearer flows) |
| 5173 | Vite dev server (the manual `frontend-dev` Tilt resource; proxies `/api` to 8090) |
| 3000 | Grafana (anonymous admin in dev) |
| 9090 | Prometheus |
| 16686 | Jaeger |

## Frontend

In-cluster, the bff serves the built SPA bundle at the same origin as
the API, so there is no separate frontend deployment. For SPA iteration,
trigger the `frontend-dev` Tilt resource (or run `npm run dev` in
`frontend/`) and open http://localhost:5173; its `/api` requests proxy
to the gateway on 8090, so login and cookie flows run against the real
edge. See `frontend/README.md` for the frontend task list.

## Observability

Every service pushes OTLP straight to a node-local otel-agent collector;
it forwards to a central otel-gateway, which fans out to Prometheus
(metrics, with exemplars linking histogram buckets to trace IDs), Loki
(logs), and Jaeger (traces). Browser telemetry rides the same pipe
through a session-gated relay on the bff, so one trace stitches the
browser through the bff and into whichever service and database
answered the call.

Five dashboards are provisioned into the `vg-collect` Grafana folder
(localhost:3000, anonymous admin in dev):

- `vg-service-red` - per-service rate/errors/duration
- `vg-apisix-edge` - gateway traffic and status codes
- `vg-datastores` - Postgres/MongoDB/Valkey health
- `vg-pod-details` - per-pod CPU/memory/restarts
- `vg-node-details` - node-level pressure and capacity

Ten alert rules are provisioned alongside them in the same `vg-collect`
folder; each links a runbook under `docs/runbooks/` via its
`runbook_url` annotation.

See one stitched trace:

1. Log in at localhost:8090 and click around the app for a minute.
2. Open Jaeger at localhost:16686.
3. Look up service `frontend` and open its most recent trace.

## Secrets (dev)

`.env` (gitignored) -> Tilt renders an external-secrets `fake` ClusterSecretStore ->
per-service `ExternalSecret`s materialize k8s Secrets. The same ExternalSecrets run
against AWS Secrets Manager in the documented production path.

## Repo layout

- `api/` OpenAPI contracts
- `libs/go/` shared modules (one concern each)
- `services/` one Go module per service
- `frontend/` React SPA (typed against `api/bff.yaml`, served by the bff)
- `deploy/charts/` Helm (per-service + platform)
- `docs/` diagrams, runbooks, production paths.

## Status

Everything is complete and verified: auth (OIDC + dev-provider login),
user, the edge and SPA shell (APISIX gateway, session cookies, the bff
serving the built bundle), enrichment (catalog search and resolve
against IGDB/PriceCharting with scored auto-matching, a daily pricing
walk plus its CronJob, heuristic recommendations scoring, and
credential-less stub-mode fixtures), collection (granular entries,
tags, saved views, and filter-aware dashboard composition with live
enrichment pricing), the frontend (collection views in table, cover
grid, and compact list over the full filter/sort/group matrix, saved
views, a drag-orderable backlog, an add wizard with match confirmation
and a custom-item path, entry editing with pricing affordances, an
account page (profile editing, linking multiple provider logins to
one account with conflict-safe identity-first sign-in, unlinking, and
full account deletion), an
insights strip on the homepage whose stats follow the active filters
(with expandable breakdowns, value-over-time, and recommendations),
and a dark-default theme with a light toggle, all covered end to end
by the Playwright journey), and observability plus docs (five
Grafana dashboards, ten alert rules with runbooks, and traces stitched
from browser to database). Real IGDB and PriceCharting keys remain the
only unexercised path; stub mode is the shipped default. Frontend still
needs a lot of style work and cleaning user flows.

## License

Licensed under the [GNU Affero General Public License v3.0](LICENSE)
(SPDX `AGPL-3.0-only`). You may use, modify, and distribute this code, including
commercially, provided derivative works and any network-hosted modified versions
are released under the same license with complete corresponding source. As the
copyright holder you may also grant separate commercial licenses.
