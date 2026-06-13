# vg-collect

Video-game collection tracker with granular per-item detail: OIDC login,
IGDB metadata enrichment, PriceCharting market pricing, full observability.

## Prerequisites

- go ≥1.26
- docker
- kubectl
- helm ≥3.14
- tilt ≥0.33
- task ≥3.38
- golangci-lint ≥2.1
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
| `task build` | compile every module + the frontend bundle |
| `task e2e` | Playwright login smoke against the running stack |
| `task run` / `task down` | tilt up / down |

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
| 5173 | Vite dev server (the manual `frontend-dev` Tilt resource; proxies `/api` to 8090) |

## Frontend

In-cluster, the bff serves the built SPA bundle at the same origin as
the API, so there is no separate frontend deployment. For SPA iteration,
trigger the `frontend-dev` Tilt resource (or run `npm run dev` in
`frontend/`) and open http://localhost:5173; its `/api` requests proxy
to the gateway on 8090, so login and cookie flows run against the real
edge. See `frontend/README.md` for the frontend task list.

## Secrets (dev)

`.env` (gitignored) → Tilt renders an external-secrets `fake` ClusterSecretStore →
per-service `ExternalSecret`s materialize k8s Secrets. The same ExternalSecrets run
against AWS Secrets Manager in the documented production path.

## Repo layout

- `api/` OpenAPI contracts
- `libs/go/` shared modules (one concern each)
- `services/` one Go module per service
- `frontend/` React SPA (typed against `api/bff.yaml`, served by the bff)
- `deploy/charts/` Helm (per-service + platform)
- `docs/` diagrams & runbooks.

## Status

Foundations and the user service are complete. The user service API returns
401 in-cluster until the auth service is ready (its JWKS endpoint doesn't exist
yet); integration tests cover the API with a local JWKS. Bruno collections
arrive with the auth service (they need dev-provider tokens to authenticate).

## License

Licensed under the [GNU Affero General Public License v3.0](LICENSE)
(SPDX `AGPL-3.0-only`). You may use, modify, and distribute this code, including
commercially, provided derivative works and any network-hosted modified versions
are released under the same license with complete corresponding source. As the
copyright holder you may also grant separate commercial licenses.
