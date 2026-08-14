# Auth service

The auth service is the OIDC relying party and the only token issuer in
vgkeep. It runs the authorization-code + PKCE dance against Google
and Twitch (plus a fixture-only dev provider), resolves the verified
identity to a user via the user service, and mints Ed25519 access JWTs
paired with opaque rotating refresh tokens. Every other service trusts
its JWKS; nothing else in the system signs a token. It listens on 8080
in-cluster, is never published by the gateway, and keeps all its state
in its own Postgres (auth-pg).

What it serves, as an operator sees it:

- Login with Google or Twitch: `POST /oauth/start` hands out the
  provider redirect, `POST /oauth/callback` exchanges the code,
  verifies the ID token (RS256, issuer, audience, expiry, nonce), and
  requires a verified email before any account is touched.
- Dev fixture login: `POST /oauth/dev/token` mints a session for
  `alice`, `bob`, or `admin` (answers 404 unless `DEV_PROVIDER_ENABLED`).
  Fixtures are the only identities it can mint for; real accounts always
  go through a real provider.
- Access tokens: EdDSA JWTs, `iss vgkeep-auth`, `aud vgkeep`,
  5 minute TTL, `roles` claim read from the user service at mint time.
- Refresh tokens: opaque, single-use, rotated on every
  `POST /token/refresh`; a session family hard-stops 720h (30 days)
  after login. Presenting a consumed token revokes the whole family and
  returns `refresh_reused` with `revoke_jtis` for the bff denylist.
- Logout: `POST /token/revoke` kills the presented token's family;
  idempotent.
- Account linking: `POST /oauth/link/start` and `POST /oauth/dev/link`
  attach another provider login to the caller's account;
  `GET /users/{userId}/identities` lists linked logins;
  `DELETE /identities/{identityId}` unlinks one (the last login on an
  account is refused with `last_identity`).
- Account deletion leg: `DELETE /users/{userId}/auth` erases the
  caller's identities and revokes every refresh family (self only).
- `GET /providers` reports which login buttons can succeed right now.
- `GET /.well-known/jwks.json` serves every non-retired Ed25519 public
  key; consumed by bff, user, enrichment, collection, and social via
  jwtauth.
- `POST /internal/service-token` mints a short-lived (900s) machine
  credential for the catalog-refresh and entry-rematch CronJobs:
  machine-to-machine bootstrap, gated by a static internal secret
  (`X-Internal-Token`) instead of a JWT (a CronJob has no session to
  present). The minted token carries no roles and claim
  `token_use=service`, the signal enrichment's and collection's
  `requireService`/`requireAdminOrService` guards key off downstream.
  Body `{"service": "catalog-refresh" | "entry-rematch"}`.

No endpoint on this service carries Bearer middleware except the
self-service ones (link start, dev link, identities, unlink, auth
wipe): login and JWKS are where tokens come from, and
/internal/service-token authenticates the static secret in-handler
instead. Network reachability is the access control; the
NetworkPolicy admits bff, user, enrichment, collection, and social
pods, plus the catalog-refresh and entry-rematch CronJob pods (their
one call here is the exchange leg), on 8080.

## Architecture

```mermaid
graph LR
    browser[Browser SPA] -->|"/api/auth/* 20 req/min per IP"| apisix[APISIX :8090]
    apisix --> bff[bff]
    bff -->|"login, refresh, revoke, identities"| auth[auth :8080]
    user[user] -.->|"JWKS"| auth
    enrichment[enrichment] -.->|"JWKS"| auth
    collection[collection] -.->|"JWKS"| auth
    social[social] -.->|"JWKS"| auth
    auth -->|"upsert + roles, service JWT"| user
    auth -->|"TLS verify-full"| authpg[("auth-pg Postgres 17")]
    auth -->|"discovery, code exchange, JWKS"| google[Google OIDC]
    auth -->|"discovery, code exchange, JWKS"| twitch[Twitch OIDC]
    eso[ExternalSecret auth-secrets] -.->|"pg password, signing seed, provider credentials"| auth
    certmgr[cert-manager vg-ca] -.->|"auth-pg-tls"| authpg
```

The auth service has no CronJobs and no Valkey; expired OAuth states are
swept opportunistically on every state insert. Its one runtime
dependency besides Postgres is the user service: logins upsert the
profile there, and every refresh re-reads roles so grants propagate at
the next rotation. Provider traffic (discovery, token exchange,
provider JWKS) goes to the real Google and Twitch issuers with a 10s
client timeout; provider metadata is fetched lazily on first use, so a
provider outage degrades logins but cannot crash-loop the pod.

Token refresh is the hot path with ordering that matters:

```mermaid
sequenceDiagram
    participant bff
    participant auth
    participant user
    participant pg as auth-pg
    bff->>auth: POST /token/refresh
    auth->>pg: peek session by token hash
    Note over auth,pg: family already revoked: 401 refresh_reused, empty revoke_jtis
    auth->>user: GET /users/{id} (current roles)
    Note over auth,user: user service down: 503 user_unavailable, token NOT consumed
    auth->>auth: mint access JWT (new jti)
    auth->>pg: rotate in one tx: consume old hash, insert child
    Note over auth,pg: old token already used: revoke family, 401 refresh_reused + revoke_jtis
    auth-->>bff: 200 new token pair
```

Roles are fetched before rotation on purpose: an upstream failure
answers 503 with the token unconsumed, so the client retries with the
same token instead of tripping reuse detection. The reuse branch is the
only channel that feeds the bff denylist: `revoke_jtis` lists the
access-token jtis from the revoked family young enough to still be
alive (created within access TTL + 1 minute).

## Running it

Dev ports (Tilt port-forwards; the gateway on 8090 publishes only the
bff):

- auth: `localhost:8082` -> container 8080
- auth-pg: `localhost:5434` -> container 5432 (`psql -h localhost -p 5434 -U auth auth`, password `PG_AUTH_PASSWORD` from `.env`)

Health endpoints: `GET /healthz` always answers 200 while the process
lives (liveness). `GET /readyz` pings the Postgres pool and answers 503
`not_ready` when the ping fails (readiness); a not-ready pod drops out
of the `auth` Service until Postgres is back.

Migrate mode: `auth migrate` runs the embedded schema migrations
against `DATABASE_URL` and exits. The deployment runs it as an init
container before every pod start, so a rollout is also a migration.
Locally: `task auth:db:migrate`.

Task targets that touch this service:

- `task auth:gen` regenerates the server stubs from `api/auth.yaml` and
  the user-service client from `api/user.yaml` (also runs under root
  `task gen`).
- `task auth:db:migrate` applies migrations to `DATABASE_URL` (also
  runs under root `task migrate`, alongside every other migrate-capable
  service).
- `task grant-fixture-admin` grants the admin role to the dev `admin`
  fixture (logs the fixture in first so its user row exists, then
  inserts the role on user-pg).
- Root `task lint`, `task test:cover`, `task check` cover this module
  like every other.

In Tilt the `auth` resource (label `services`) rebuilds
`vgkeep/auth` from `services/auth/Dockerfile` whenever `libs/go` or
`services/auth` changes, and depends on `secret-store`, `auth-pg`, and
`user`. Tilt enables a real provider only when both halves of its
credential pair are present in `.env`, and then also sets
`env.oauthRedirectUrl` (default `http://localhost:8090/api/auth/callback`).

Bruno flows live in `bruno/auth/` and target `auth_url =
http://localhost:8082` directly with Bearer tokens: `dev-token` logs in
and stores tokens, `refresh` rotates, `reuse-detect` replays the
consumed token and expects `refresh_reused`, plus `jwks`, `identities`,
`dev-link`, `unlink-identity`, `revoke`, and `delete-user-auth`.

## Configuration

All configuration is environment variables (`internal/config`). Chart
values fill most of them; secrets arrive through the ExternalSecret
`auth-secrets` (ClusterSecretStore `vg-fake` in dev, refreshed every
1m), whose store keys Tilt renders from `.env` (see `.env.example`).

| Env var                                     | Default                                       | Comes from                                                                                                                                                                                                                                                  |
| ------------------------------------------- | --------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `HTTP_ADDR`                                 | `:8080`                                       | code default; the chart does not set it                                                                                                                                                                                                                     |
| `DATABASE_URL`                              | required                                      | assembled in deployment.yaml: `postgres://auth:$(PG_PASSWORD)@auth-pg:5432/auth?sslmode=verify-full&sslrootcert=/etc/vg/pg-ca/ca.crt`; `PG_PASSWORD` from Secret `auth-secrets` key `pg-password` (store key `auth/pg-password`, `.env` `PG_AUTH_PASSWORD`) |
| `JWT_SIGNING_KEY`                           | required                                      | Secret `auth-secrets` key `jwt-signing-key` (store key `auth/jwt-signing-key`, `.env` `AUTH_JWT_SIGNING_KEY`); base64 (std) 32-byte Ed25519 seed                                                                                                            |
| `JWT_ISSUER`                                | `vgkeep-auth`                                 | values `env.jwtIssuer`                                                                                                                                                                                                                                      |
| `JWT_AUDIENCE`                              | `vgkeep`                                      | values `env.jwtAudience`                                                                                                                                                                                                                                    |
| `ACCESS_TOKEN_TTL`                          | `5m`                                          | values `env.accessTokenTtl`                                                                                                                                                                                                                                 |
| `REFRESH_TOKEN_TTL`                         | `720h`                                        | values `env.refreshTokenTtl`                                                                                                                                                                                                                                |
| `USER_SERVICE_URL`                          | required                                      | values `env.userServiceUrl` (`http://user:8080`)                                                                                                                                                                                                            |
| `INTERNAL_SERVICE_SECRETS`                  | required                                      | CSV accept set (A/B pair during rotation) for `POST /internal/service-token`'s `X-Internal-Token`; secret key `auth/internal-service-token` (+ `-previous`), `.env` `AUTH_INTERNAL_SERVICE_TOKEN` (+ `_PREVIOUS`)                                          |
| `JWKS_URL`                                  | `http://localhost:8080/.well-known/jwks.json` | code default; the self-service Bearer verifier reads the pod's own JWKS                                                                                                                                                                                     |
| `OAUTH_REDIRECT_URL`                        | empty                                         | values `env.oauthRedirectUrl`; Tilt sets it when a provider pair exists                                                                                                                                                                                     |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | empty                                         | Secret keys `google-client-id` / `google-client-secret`, rendered only when `providers.google.enabled`                                                                                                                                                      |
| `GOOGLE_ISSUER_URL`                         | `https://accounts.google.com`                 | values `env.googleIssuerUrl`                                                                                                                                                                                                                                |
| `TWITCH_CLIENT_ID` / `TWITCH_CLIENT_SECRET` | empty                                         | Secret keys `twitch-client-id` / `twitch-client-secret`, rendered only when `providers.twitch.enabled`                                                                                                                                                      |
| `TWITCH_ISSUER_URL`                         | `https://id.twitch.tv/oauth2`                 | values `env.twitchIssuerUrl`                                                                                                                                                                                                                                |
| `DEV_PROVIDER_ENABLED`                      | `false`                                       | values `env.devProviderEnabled` (chart default `true`; production values files must set `false`)                                                                                                                                                            |
| `SERVICE_VERSION`                           | `dev`                                         | the image tag, stamped onto telemetry as `service.version`                                                                                                                                                                                                  |
| `OTEL_EXPORTER_OTLP_ENDPOINT`               | unset                                         | values `otel.exporterEndpoint` (otel-agent in vg-platform); empty disables export, JSON logs still go to stdout                                                                                                                                             |

What happens when optional pieces are absent:

- A provider with an incomplete credential pair stays disabled: it is
  missing from `GET /providers` and `POST /oauth/start` for it answers
  400 `unknown_provider`. The stack runs with zero real secrets.
- A real provider configured without `OAUTH_REDIRECT_URL` fails config
  validation and the pod exits at boot.
- `DEV_PROVIDER_ENABLED=false` makes `/oauth/dev/token` and
  `/oauth/dev/link` answer plain 404s, indistinguishable from unmounted
  routes. Production values files must set `devProviderEnabled: false`.

## Datastore: auth-pg

Four tables, all owned by `internal/store` (no other package writes
this schema):

- `identities`: (provider, provider_subject) -> user_id, plus a stable
  `id` uuid for list/unlink URLs and an informational `email` refreshed
  at each login. Insert-only binding; an identity never silently moves
  between accounts (the one exception is the heal path after an
  interrupted account deletion).
- `refresh_tokens`: one row per issued refresh token, keyed by hex
  SHA-256 of the opaque token (the raw token is never stored).
  `family_id` groups a login session's rotation chain; `parent_hash` is
  the audit trail; `used_at`/`revoked_at` drive reuse detection;
  `last_access_jti` is what `revoke_jtis` reports.
- `auth_states`: pending OAuth round-trips (state, PKCE verifier,
  nonce, provider, 10 minute expiry, optional `link_user_id` marking a
  link flow). Swept on every insert; no background job.
- `signing_keys`: kid -> base64url Ed25519 public key, `retired_at`
  NULL while the key should appear in the JWKS. Every replica registers
  its kid at boot (kid is derived from the key, so this is idempotent).

Migrations are golang-migrate SQL files embedded in the binary
(`services/auth/migrations`, currently `000001_init` and
`000002_identity_link`), applied by the `migrate` init container.

Connection facts: single-replica StatefulSet `auth-pg` (postgres:17-alpine,
1Gi PVC), TLS on with a cert-manager certificate (`auth-pg-tls`, issuer
`vg-ca`, 2160h duration); the service connects as user `auth` to
database `auth` with `sslmode=verify-full` against the mounted CA. A
postgres-exporter sidecar (v0.20.0) serves metrics on 9187 over
pod-local loopback and is scraped through the `auth-pg` ServiceMonitor
every 30s; the pg NetworkPolicy admits only auth pods on 5432 and
Prometheus on 9187.

Pool visibility comes from pgkit (no labels; filter by resource
attribute `service_name="auth"`): `vg_pgkit_pool_connections`,
`vg_pgkit_pool_connections_idle`, `vg_pgkit_pool_connections_max`,
`vg_pgkit_pool_acquires_total`, `vg_pgkit_pool_empty_acquires_total`,
and `vg_pgkit_pool_acquire_wait_seconds_total`. Server-side truth is
the exporter (`service="auth-pg"`): `pg_stat_activity_count`,
`pg_settings_max_connections`, `pg_stat_database_xact_commit` /
`_rollback`, `pg_stat_database_blks_hit` / `_read`, `pg_locks_count`.

## Telemetry

Everything flows through the shared pipeline: OTLP to otel-agent, then
otel-gateway, then Prometheus (exemplars on), Loki, and Jaeger. There
is no /metrics endpoint and no scrape config for the service itself.

Platform-provided signals:

| Prometheus name                                            | Source                                                                                                         | Answers                                                        |
| ---------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- |
| `http_server_request_duration_seconds_{count,sum,bucket}`  | otelhttp in the router stack; labels `http_route`, `http_response_status_code`, resource `service_name="auth"` | RED per route (probe routes `/healthz` and `/readyz` included) |
| `go_goroutine_count`, `go_memory_used_bytes` (runtime set) | otel runtime instrumentation from Setup()                                                                      | leaks and memory pressure                                      |
| `vg_pgkit_pool_*` (six series above)                       | pgkit pool registration                                                                                        | client-side pool demand, headroom, wait                        |
| `pg_*` exporter set (`service="auth-pg"`)                  | postgres-exporter sidecar                                                                                      | server-side connections, tx, cache, locks                      |

### Domain metrics

Meter name `github.com/levonn-dev/vgkeep/services/auth`; counter
and gauge instruments live as fields on `server.Handlers` (same shape
as the bff cache counter); the provider histogram is recorded inside
`internal/oidc`. All label values are bounded sets listed here; none
carry user ids or free-form strings.

| OTel name                           | Instrument           | Unit        | Labels                                                                                                                                | Prometheus name                                                | Answers                                                                                                                                   |
| ----------------------------------- | -------------------- | ----------- | ------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `vg.auth.login.outcomes`            | Int64Counter         | `{login}`   | `provider` (google, twitch, dev); `flow` (login, link); `outcome` (success, rejected, provider_error, upstream_error, internal_error) | `vg_auth_login_outcomes_total`                                 | Are provider dances completing, and which layer broke: the IdP, our verification, the user service, or our storage                        |
| `vg.auth.token.refreshes`           | Int64Counter         | `{refresh}` | `outcome` (success, rejected, reuse_detected, upstream_error, internal_error)                                                         | `vg_auth_token_refreshes_total`                                | Is session refresh healthy (every bff session depends on it), and is reuse detection firing                                               |
| `vg.auth.provider.request.duration` | Float64Histogram     | `s`         | `provider` (google, twitch); `op` (discovery, token_exchange, jwks); `outcome` (ok, error)                                            | `vg_auth_provider_request_duration_seconds_{count,sum,bucket}` | Which IdP hop is slow or failing; splits "login is slow" into provider vs us                                                              |
| `vg.auth.signing_keys.active`       | Int64ObservableGauge | `{key}`     | none                                                                                                                                  | `vg_auth_signing_keys_active`                                  | How many keys the JWKS serves right now; verifies rotation (1 -> 2 -> 1) and catches an empty JWKS before every service's validation dies |

Emission sites, precisely:

- `vg.auth.login.outcomes` increments once per terminal of a dance
  whose provider is known: the exchange branches of `OauthCallback`
  (`ProviderError` -> `provider_error`, other verify failures ->
  `rejected`), the unknown-fixture 400s of `DevToken` and `DevLink`
  (provider `dev` -> `rejected`), and every terminal of `completeLogin`
  / `completeLink` (unverified email, identity conflict, link target
  gone -> `rejected`; user service Get/Upsert failure ->
  `upstream_error`; store or mint failure -> `internal_error`; session
  minted -> `success`). `flow` is `link` when the consumed state
  carried `link_user_id` (and for `DevLink`), else `login`. Malformed
  bodies, `unknown_provider`, and `invalid_state` are not counted (no
  provider to attribute); they stay visible as 4xx on "Errors by route
  and status".
- `vg.auth.token.refreshes` increments once per `RefreshToken` terminal:
  200 -> `success`; both reuse branches (revoked-family short-circuit
  and rotation-time detection) -> `reuse_detected`; unknown/expired
  token and user-gone -> `rejected`; 503 `user_unavailable` ->
  `upstream_error`; 500 paths -> `internal_error`. Bodies that fail
  validation (malformed JSON, missing `refresh_token`) are not counted.
- `vg.auth.provider.request.duration` records every RP HTTP round trip
  (discovery fetch, token-endpoint POST, provider JWKS fetch) with its
  wall time and `outcome="error"` when the attempt fails at any stage:
  request build, network, non-200, or malformed response. The dev
  provider never appears here. SDK default bucket boundaries are fine
  (provider calls cap at the 10s client timeout).
- `vg.auth.signing_keys.active` is a callback registered in
  `server.New` that counts `ActiveSigningKeys` rows with a 5s timeout;
  on query error it records nothing (a gap, never a false zero).

### Logs

Problem responses never echo internal error details to callers, so the
server side carries them: four structured events (slog, JSON, trace ids
attached by the shared handler) alongside the per-request `http request`
line and panic reports:

| Event                      | Level | Fields                                                                                    | Emitted from                                                                                                                        |
| -------------------------- | ----- | ----------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `provider request failed`  | ERROR | `provider`, `err` (the ProviderError string carries op and status)                        | 502 branches of `OauthStart`, `OauthLinkStart`, `OauthCallback`                                                                     |
| `auth store error`         | ERROR | `op`, `err`                                                                               | every handler branch that answers 500 `internal`                                                                                    |
| `user service unavailable` | ERROR | `op`, `err`                                                                               | user-service failure branches of `completeLogin`, `completeLink`, `RefreshToken` (502 `user_service_error`, 503 `user_unavailable`) |
| `refresh reuse detected`   | WARN  | `user_id` (empty on the revoked-family short-circuit, which never learns it), `jti_count` | both reuse branches of `RefreshToken`                                                                                               |

Never log token material: no refresh tokens, no hashes, no minted JWTs.

## Dashboard: vg-auth

File `deploy/charts/platform/files/dashboards/auth.json`, uid
`vg-auth`, title `Auth Service`, provisioned into the `vgkeep`
folder like every dashboard in that directory. Open it at
http://localhost:3000/d/vg-auth while `task run` holds the Grafana
port-forward. It follows the structural conventions shared by every
vgkeep dashboard: schemaVersion 39, tags `["vgkeep"]`, timezone
`browser`, refresh `30s`, an explicit datasource object per target
(uid `prometheus`, or `loki` for the logs panel). No dual-axis panels;
Grafana default palette; thresholds only on the two state panels noted.

**1. Request rate by route** (timeseries, reqps, legend `{{http_route}}`)

```promql
sum by (http_route) (rate(http_server_request_duration_seconds_count{service_name="auth"}[$__rate_interval]))
```

**2. 5xx ratio** (timeseries, percentunit, legend `5xx ratio`)

```promql
sum (rate(http_server_request_duration_seconds_count{service_name="auth",http_response_status_code=~"5.."}[5m])) / sum (rate(http_server_request_duration_seconds_count{service_name="auth"}[5m]))
```

**3. Latency by route, p95 and p99** (timeseries, s, `"exemplar": true`
on both targets, legends `p95 {{http_route}}` / `p99 {{http_route}}`)

```promql
histogram_quantile(0.95, sum by (le, http_route) (rate(http_server_request_duration_seconds_bucket{service_name="auth"}[$__rate_interval])))
histogram_quantile(0.99, sum by (le, http_route) (rate(http_server_request_duration_seconds_bucket{service_name="auth"}[$__rate_interval])))
```

**4. Errors by route and status** (timeseries, reqps, legend
`{{http_route}} {{http_response_status_code}}`)

```promql
sum by (http_route, http_response_status_code) (rate(http_server_request_duration_seconds_count{service_name="auth",http_response_status_code=~"4..|5.."}[$__rate_interval]))
```

**5. Logins by provider, flow, outcome, 5m** (timeseries, short, legend
`{{provider}} {{flow}} {{outcome}}`)

```promql
sum by (provider, flow, outcome) (increase(vg_auth_login_outcomes_total[5m]))
```

**6. Refresh outcomes, 5m** (timeseries, short, legend `{{outcome}}`)

```promql
sum by (outcome) (increase(vg_auth_token_refreshes_total[5m]))
```

**7. Reuse detections, 24h** (stat, short; state thresholds: green 0,
red >= 1)

```promql
sum(increase(vg_auth_token_refreshes_total{outcome="reuse_detected"}[24h]))
```

**8. Active signing keys** (stat, short; state thresholds: red below 1,
green at 1 and above; max() because every replica exports the gauge and
a surge rollout briefly runs two pods)

```promql
max(vg_auth_signing_keys_active)
```

**9. Provider request p95 by op** (timeseries, s, `"exemplar": true`,
legend `{{provider}} {{op}}`)

```promql
histogram_quantile(0.95, sum by (le, provider, op) (rate(vg_auth_provider_request_duration_seconds_bucket[$__rate_interval])))
```

**10. Provider request errors, 5m** (timeseries, short, legend
`{{provider}} {{op}}`)

```promql
sum by (provider, op) (increase(vg_auth_provider_request_duration_seconds_count{outcome="error"}[5m]))
```

**11. PG pool connections** (timeseries, short, legends `in pool` /
`idle` / `max`)

```promql
vg_pgkit_pool_connections{service_name="auth"}
vg_pgkit_pool_connections_idle{service_name="auth"}
vg_pgkit_pool_connections_max{service_name="auth"}
```

**12. PG pool mean acquire wait** (timeseries, s, legend `mean wait`)

```promql
rate(vg_pgkit_pool_acquire_wait_seconds_total{service_name="auth"}[$__rate_interval]) / rate(vg_pgkit_pool_acquires_total{service_name="auth"}[$__rate_interval])
```

**13. auth-pg connections vs max** (timeseries, short, legends
`connections` / `max`)

```promql
sum(pg_stat_activity_count{service="auth-pg"})
max(pg_settings_max_connections{service="auth-pg"})
```

**14. auth-pg transactions** (timeseries, ops, legends `commit` /
`rollback`)

```promql
sum(rate(pg_stat_database_xact_commit{service="auth-pg",datname!~"template.*"}[$__rate_interval]))
sum(rate(pg_stat_database_xact_rollback{service="auth-pg",datname!~"template.*"}[$__rate_interval]))
```

**15. Pod CPU** (timeseries, short, legend `{{pod}}`; the `auth-.*`
scope intentionally covers auth-pg-0 too)

```promql
sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="vgkeep", pod=~"auth-.*", container!=""}[$__rate_interval]))
```

**16. Pod working-set memory** (timeseries, bytes, legend `{{pod}}`)

```promql
sum by (pod) (container_memory_working_set_bytes{namespace="vgkeep", pod=~"auth-.*", container!=""})
```

**17. Pod restarts, 15m** (timeseries, short, legend `{{pod}}`)

```promql
sum by (pod) (increase(kube_pod_container_status_restarts_total{namespace="vgkeep", pod=~"auth-.*"}[15m]))
```

**18. Goroutines** (timeseries, short, legend `goroutines`)

```promql
go_goroutine_count{service_name="auth"}
```

**19. Heap used** (timeseries, bytes, legend `heap`)

```promql
go_memory_used_bytes{service_name="auth"}
```

**20. Recent error logs** (logs panel, Loki datasource)

```logql
{service_name="auth"} | severity_text="ERROR"
```

## Failure modes and triage

### 1. Logins failing at the provider hop

Symptom: 502s on `/oauth/start` or `/oauth/callback`; "Logins by
provider, flow, outcome, 5m" shows `outcome="provider_error"` for one
provider while the other stays healthy; "Provider request errors, 5m"
names the failing hop. The vg-auth-provider-errors rule fires when one
provider exceeds five `provider_error` outcomes in 10 minutes:

```promql
sum by (provider) (increase(vg_auth_login_outcomes_total{outcome="provider_error"}[10m]))
```

Confirm with the
`provider request failed` log line. `op=discovery` points at issuer
URL or provider metadata trouble, `op=token_exchange` at credentials
(an expired or revoked client secret answers non-200 here),
`op=jwks` at provider key rotation racing our cache. Check the
provider status page before touching anything; a credentials fix means
updating the store keys `auth/google-client-*` or `auth/twitch-client-*`
and re-rolling. Refresh traffic is unaffected: existing sessions ride
out a provider outage.

### 2. Logins or refreshes failing at the user service

Symptom: `outcome="upstream_error"` on "Logins by provider, flow,
outcome, 5m" and "Refresh outcomes, 5m", refresh answering
503 `user_unavailable`, `user service unavailable` log lines. The fault
is the user service; the shared 5xx triage in
[stack.md](stack.md#1-service-5xx-ratio-above-5-percent) applies to
it. Refresh 503s are retryable by design (the token is not
consumed), so sessions recover on their own once user is back.

### 3. Refresh reuse detections

Symptom: "Reuse detections, 24h" above zero; `refresh reuse detected`
WARN lines. The vg-auth-refresh-reuse rule fires on any detection
inside 15 minutes:

```promql
sum(increase(vg_auth_token_refreshes_total{outcome="reuse_detected"}[15m]))
```

One
detection is a replayed refresh token: either theft or a client
regression (a second refresh with a stale token; the bruno
`reuse-detect` flow triggers this deliberately in dev). Detection is
containment working: the family is revoked and `revoke_jtis` flow into
the bff denylist ([bff.md](bff.md#1-valkey-unreachable) describes that
side, including the fail-open case where Valkey is out). A sustained
stream from one user is an incident;
correlate `user_id` in the WARN lines. A spike that spans many users
and starts at a bff deploy is a session-refresh regression, not an
attack: reuse detections begin at the rollout timestamp, track the
failure rate on `vg_bff_session_refreshes_total`, and stop on
rollback.

### 4. Postgres down or saturated

Symptom: every route 500s, `/readyz` answers 503, `auth store error`
logs; the four Postgres panels ("PG pool connections", "PG pool mean
acquire wait", "auth-pg connections vs max", "auth-pg transactions")
flatline or spike. auth-pg is single-replica: while
it restarts, login, refresh, logout, linking, and JWKS reads all fail,
but already-issued access tokens keep validating everywhere (jwtauth
caches keys in each consumer). Sessions resume unharmed afterward since
refresh tokens are only consumed on successful rotation. For
saturation ("auth-pg connections vs max" nearing its max line, or
waits climbing on "PG pool mean acquire wait"), follow
[stack.md](stack.md#6-postgres-connections-above-80-percent-of-max).
Restart churn on auth-pg-0:
[stack.md](stack.md#4-pod-restart-churn-or-oom-kill).

### 5. 429s on login at the edge

Symptom: users report login failures, but "Request rate by route" and
"Errors by route and status" show nothing.
APISIX rate-limits `/api/auth/*` at 20 requests per minute per client
IP (rejected at the edge with 429, never reaching bff or auth), against
300 per minute for the rest of `/api/*`. Confirm on the vg-apisix-edge
dashboard. A shared office NAT can trip this legitimately; the knob is
`rateLimit.authPerMinute` in the bff chart values.

### 6. Platform-wide 401s: JWKS trouble

Symptom: every service starts rejecting Bearer tokens within minutes;
auth itself may look healthy. Check "Active signing keys"; the
vg-auth-jwks-empty rule pages the moment this gauge reads below 1:

```promql
vg_auth_signing_keys_active
```

Zero means the JWKS is empty, which happens when a retire UPDATE hit
every key (the boot-time registration makes 0 impossible otherwise).
A pod restart does not fix this: key registration is insert-only and
skips the existing retired row. Un-retire immediately:

```bash
kubectl -n vgkeep exec statefulset/auth-pg -- psql -U auth -d auth -c "UPDATE signing_keys SET retired_at = NULL WHERE kid = '<kid>';"
```

If the count is fine but 401s persist, compare the `kid` in a rejected
token's header against `curl -s http://localhost:8082/.well-known/jwks.json`:
a mid-rotation mismatch means the deployment rolled before the new key
registered, or consumers cached an old document (they refetch on
unknown kid within 30s).

### 7. Slow logins

Symptom: p95/p99 on `/oauth/callback` climbing on "Latency by route,
p95 and p99". "Provider request p95 by op" splits the suspects: a
provider op near the 10s client timeout is the IdP being slow; flat
provider latency with rising wait on "PG pool mean acquire wait" is
our Postgres; both flat means the user service (its Upsert/Get are
on the login path; check vg-user's latency panels). General latency
triage: [stack.md](stack.md#2-service-p99-latency-above-500ms).

### 8. Dev provider exposure

Symptom check, any environment that should be locked down:
`curl -s http://localhost:8082/providers` must not list `dev`, and
`POST /oauth/dev/token` must answer 404. If `dev` appears, the values
file shipped `devProviderEnabled: true`; fix the values and roll. The
blast radius is bounded to the three fixture accounts (alice, bob,
admin at example.com) because `DevClaims` is a closed literal, but
treat it as a misconfiguration incident anyway.

## Admin levers

There are no backfill endpoints on this service; its data never needs
renormalization. The levers are:

- Signing-key rotation (zero-downtime, manual):
  1. Generate a seed: `openssl rand -base64 32`.
  2. Put it in the secret store at `auth/jwt-signing-key` (dev:
     `AUTH_JWT_SIGNING_KEY` in `.env`; Tilt re-applies).
  3. Roll the deployment. The new pod registers its kid at boot and the
     JWKS serves both keys; in-flight tokens signed by the old key keep
     verifying.
  4. After the last old-key access token expired (ACCESS_TOKEN_TTL,
     5 minutes), retire the old key:

     ```bash
     kubectl -n vgkeep exec statefulset/auth-pg -- psql -U auth -d auth -c "UPDATE signing_keys SET retired_at = now() WHERE kid = '<old-kid>';"
     ```

     "Active signing keys" should read 2 during the overlap and 1
     after. Retired the wrong kid? Set `retired_at = NULL` for it (see
     failure mode 6).
- Revoke every session of one user (compromised account):

  ```bash
  kubectl -n vgkeep exec statefulset/auth-pg -- psql -U auth -d auth -c "UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = '<uuid>' AND revoked_at IS NULL;"
  ```

  The user's next refresh answers 401 `refresh_reused` and the bff
  clears the session. Outstanding access tokens stay valid up to 5
  minutes (this path reports no jtis to the denylist); that window is
  the design tradeoff, same as logout.
- Grant the admin role to the dev admin fixture (dev stacks only):
  `task grant-fixture-admin`. Roles land in the JWT at the fixture's
  next login or refresh. The user service owns roles; auth only mints
  what it reads.
- Dev provider gate: `env.devProviderEnabled` in the auth chart values
  (see failure mode 8 for the verification).
- Internal service secret rotation, zero downtime: publish the new
  value under secret key `auth/internal-service-token` and the old one
  under `auth/internal-service-token-previous` (dev:
  `AUTH_INTERNAL_SERVICE_TOKEN` / `..._PREVIOUS` in `.env`), enable the
  previous-token flag so the service accepts both while the two
  CronJobs (catalog-refresh, entry-rematch) still present the old
  value. After the next green run of both, drop the previous key and
  flip the flag back off - the same shape as enrichment's retired
  internal-refresh-token rotation, now centralized here since both
  CronJobs exchange for a token at this one endpoint instead of each
  holding its own service secret.

## Capacity and rollout

One replica behind a PDB with `minAvailable: 1`; requests 50m CPU /
64Mi, memory limit 128Mi. The pg StatefulSet requests 50m / 128Mi with
a 256Mi limit and a 1Gi PVC, and has its own `minAvailable: 1` PDB, so
a voluntary node drain blocks on both rather than dropping the only
copy. The auth pod's probes carry no custom timings (kubelet defaults:
10s period, 3 failures to trip); auth-pg checks readiness with
`pg_isready` every 5s. The HTTP server allows 30s per request and
drains for up to 10s on SIGTERM.

A rollout with one replica surges (maxSurge rounds to 1, maxUnavailable
to 0): the new pod runs the `migrate` init container, registers its
signing key, passes `/readyz`, and only then does the old pod
terminate. An unchanged signing secret re-registers the same kid as a
no-op. The pod template hashes the ExternalSecret template, so a change
to the secret's shape re-rolls pods by itself; a changed secret value
needs a deliberate roll (step 3 of the rotation procedure).

During an auth-pg restart the service goes not-ready and every
token-issuing path fails until Postgres returns (failure mode 4); no
state is lost and no client token is invalidated by the outage itself.
The one background cost to know about: expired-state sweeping runs
inline on every state insert (`/oauth/start` and `/oauth/link/start`),
so a login burst pays a small extra DELETE and expired rows linger only
until the next login start; there is no cleanup job to babysit.
