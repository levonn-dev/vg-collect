# vgkeep

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/lockup-dark.png">
  <img src="docs/brand/lockup-light.png" alt="vgkeep: a pixel VG monogram on an indigo tile" width="380">
</picture>

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
- node 22+ (with npm); for the browser suite, `npx playwright install chromium`

## Quickstart

```bash
task bootstrap          # tool checks + .env scaffold (edit .env if you have real keys)
task bootstrap:cluster  # once per cluster: cert-manager, external-secrets, the APISIX gateway, the observability stack, CA issuers
task run                # tilt up: builds images, deploys charts, port-forwards
```

Works against any local Kubernetes context (docker-desktop, kind, k3d, minikube);
add yours to `allow_k8s_contexts` in the Tiltfile if it's not listed.

If you need to test admin only features, you can grant admin to the dev fixture to a running cluster by running:
```bash
task grant-fixture-admin
```

Note: the fixture's handle is literally `admin` on dev databases created before the handle-backfill reserved-word fix (migrations do not re-run); fresh databases suffix it instead (e.g. `admin2`).

## Dev commands

| Command                  | What                                                                                                                                                                                                                                                   |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `task lint`              | golangci-lint every Go module + helm lint every chart + eslint the frontend + obsgen dashboard and alert lint                                                                                                                                          |
| `task test`              | go test every module + frontend vitest. Integration tests share three long-lived datastore containers (started on demand, reused across runs; per-run test databases are swept afterward, even on failure)                                             |
| `task test:cover`        | tests + the 80% coverage gate (generated code and cmd/ wiring excluded)                                                                                                                                                                                |
| `task gen`               | regenerate everything derived from the contracts: bundled contracts, domain tables, server stubs, dashboards and alerts, the frontend's typed client, facets, and locale catalogs. The chain is documented in `api/README.md` and `frontend/README.md` |
| `task tidy`              | go mod tidy every module                                                                                                                                                                                                                               |
| `task migrate`           | run db:migrate for every migrate-capable service (auth, collection, enrichment, social, user)                                                                                                                                                          |
| `task build`             | compile every module + the frontend bundle                                                                                                                                                                                                             |
| `task e2e`               | Parallel Playwright browser suite against the running stack; per-run minted dev fixtures; isolation-first                                                                                                                                              |
| `task run` / `task down` | tilt up / down                                                                                                                                                                                                                                         |
| `task nuke`              | full app-stack reset: tilt down + the vgkeep namespace + the shared test datastore containers (see Teardown)                                                                                                                                           |

Note: `task test` / `task test:cover` run the Go integration suites against
three shared containers; test databases carry a per-package name and a
per-run scope, so concurrent runs stay isolated. Bare `go test` in a module
needs no setup: it boots throwaway containers instead (needs Docker).

## Teardown

```bash
task down                    # stop the dev stack; namespace, datastore PVCs, issued TLS secrets survive
task nuke                    # app layer: tilt down + delete the vgkeep namespace (drops PVCs + TLS secrets) + the shared test datastore containers
task bootstrap:cluster:down  # remove the platform: helm uninstalls + the vg-platform namespace
```

For platform down/up cycles prefer `task nuke && task bootstrap:cluster:down`,
then `task bootstrap:cluster && task run`. What each tier preserves, and why
a platform-only teardown leaves TLS secrets chaining to a dead dev CA, is in
the Teardown section of `docs/runbooks/stack.md`.

## Edge and ports

The bff is the only public service. It is published through the APISIX
gateway, which applies per-IP rate limits: tight on `/api/auth/*` (the
credential-adjacent surface), looser on the rest of `/api/*`, none on
static assets. Everything the browser touches goes through that gateway;
the other services are reachable in dev only via Tilt port-forwards.

| Port  | What                                                                              |
| ----- | --------------------------------------------------------------------------------- |
| 8090  | APISIX gateway: the app's entrypoint (browser and Bruno `bff/`)                   |
| 8083  | bff, direct (bypasses the gateway; for debugging)                                 |
| 8082  | auth, direct (Bruno `auth/` Bearer flows)                                         |
| 8081  | user, direct (Bruno `user/` Bearer flows)                                         |
| 8084  | enrichment, direct (Bruno `enrichment/` Bearer flows)                             |
| 8085  | collection, direct (Bruno `collection/` Bearer flows)                             |
| 8086  | social, direct (no Bruno flows yet)                                               |
| 5173  | Vite dev server (the manual `frontend-dev` Tilt resource; proxies `/api` to 8090) |
| 3000  | Grafana (anonymous admin in dev)                                                  |
| 9090  | Prometheus                                                                        |
| 16686 | Jaeger                                                                            |

The full list, including the per-service Postgres port-forwards, is in the
Ports section of `docs/runbooks/stack.md`.

## Frontend

In-cluster, the bff serves the built SPA bundle at the same origin as
the API, so there is no separate frontend deployment. For SPA iteration,
trigger the `frontend-dev` Tilt resource (or run `npm run dev` in
`frontend/`) and open http://localhost:5173; its `/api` requests proxy
to the gateway on 8090, so login and cookie flows run against the real
edge. Site identity bakes into the bundle at build time from
`VITE_SITE_*` variables (see the frontend section of `.env.example`).
`frontend/README.md` covers the dev commands, the typed API client and
generated modules, and the translation workflow.

## Observability

Every service pushes OTLP through a node-local agent collector to a
central gateway, which fans out to Prometheus (metrics, with exemplars),
Loki (logs), and Jaeger (traces). Browser telemetry rides the same pipe
through a session-gated relay on the bff, so one trace stitches the
browser through the bff and into whichever service and database answered
the call. Twelve dashboards and thirty-two alert rules provision into
the `vgkeep` Grafana folder (localhost:3000, anonymous admin in dev);
every alert links a runbook under `docs/runbooks/`, indexed by
`docs/runbooks/README.md`. The dashboard catalog and the telemetry
pipeline live in `docs/runbooks/stack.md`.

See one stitched trace:

1. Log in at localhost:8090 and click around the app for a minute.
2. Open Jaeger at localhost:16686.
3. Look up service `frontend` and open its most recent trace.

## Secrets (dev)

`.env` (gitignored) -> Tilt renders an external-secrets `fake` ClusterSecretStore ->
per-service `ExternalSecret`s materialize k8s Secrets. The same ExternalSecrets run
against AWS Secrets Manager in the documented production path.

## Repo layout

- `api/` OpenAPI contracts and domain tables; layout and editing flow in
  `api/README.md`
- `libs/go/` shared modules (one concern each)
- `services/` one Go module per service
- `frontend/` React SPA (typed against `api/bff.yaml`, served by the bff);
  see `frontend/README.md`
- `bruno/` API flows against the dev stack; see `bruno/README.md`
- `deploy/charts/` Helm: per-service charts, the platform chart, and
  `vg-lib`, the shared library chart every service chart vendors via
  `task helm:deps` (runs automatically from `task lint`,
  `task bootstrap:cluster`, and the Tiltfile)
- `docs/` diagrams, runbooks, production paths, brand assets (`docs/brand/`)

Translations: see `docs/translations.md` to contribute a language.
Regions: `docs/adding-a-region.md` is the graduation checklist for a
new entry region.

## Status

Everything checked off is verified end to end by Playwright journeys,
per-service test suites, and bruno flows.

- [x] OIDC login and sessions
- [x] IGDB metadata enrichment
- [x] PriceCharting market pricing
- [x] add region support for IGDB and PriceCharting
- [x] add support for more regions
- [x] add developer and publisher fields
- [x] catalog submissions with community promotion
- [x] social profiles, shelves, likes, comments, feed
- [x] i18n with English and Japanese
- [x] Playwright e2e suite
- [x] observability: dashboards, alerts, runbooks
- [ ] docs/diagrams pass
- [ ] frontend style pass and cleaner user flows
- [ ] price-change notifications
- [ ] social notifications
- [ ] support for digital media
- [ ] digital platform sync (Steam/PSN/Epic)
- [ ] smarter recommendations (vector similarity, LLM insights)
- [ ] community translations for native-script names

## License

Licensed under the [GNU Affero General Public License v3.0](LICENSE)
(SPDX `AGPL-3.0-only`). You may use, modify, and distribute this code, including
commercially, provided derivative works and any network-hosted modified versions
are released under the same license with complete corresponding source. As the
copyright holder you may also grant separate commercial licenses.
