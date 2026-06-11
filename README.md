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
| `task lint` | golangci-lint every Go module + helm lint every chart |
| `task test` | go test every module (testcontainers need Docker) |
| `task test:cover` | tests + the 80% coverage gate (generated code and cmd/ wiring excluded) |
| `task gen` | regenerate OpenAPI server stubs/types |
| `task build` | compile every module |
| `task run` / `task down` | tilt up / down |

## Secrets (dev)

`.env` (gitignored) → Tilt renders an external-secrets `fake` ClusterSecretStore →
per-service `ExternalSecret`s materialize k8s Secrets. The same ExternalSecrets run
against AWS Secrets Manager in the documented production path.

## Repo layout

- `api/` OpenAPI contracts
- `libs/go/` shared modules (one concern each)
- `services/` one Go module per service
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
