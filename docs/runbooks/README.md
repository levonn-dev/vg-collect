# Runbooks

One runbook per service plus a stack-level guide live in this
directory. Alert triage lives inside them: every provisioned alert
rule deep-links, via its `runbook_url` annotation, the runbook section
that triages it. Start from the service runbook when you know which
component is sick; start from the alert table below when Grafana told
you first.

## Service runbooks

One per service, plus the stack-level guide. Each service runbook
covers the same ground: what the service does, architecture, running
it, configuration, datastores, telemetry, its dashboard, failure modes
and triage, admin levers, capacity and rollout.

- [stack.md](stack.md) - the whole application: topology, bring-up and
  teardown, ports, the dashboards catalog, alerting posture, telemetry
  pipeline operations, secrets flow, and stack-level failure scenarios
  (the cross-service and infrastructure alerts triage there)
- [auth.md](auth.md) - auth service: OIDC login and token issuing end
  to end; telemetry reference, dashboard vg-auth, failure triage,
  signing-key rotation and session revocation levers
- [bff.md](bff.md) - bff service: the session edge and SPA server;
  telemetry reference, dashboard vg-bff, failure triage, cookie-key
  rotation and cache reset levers
- [collection.md](collection.md) - collection service: user
  collections and read-time value composition; telemetry reference,
  dashboard vg-collection, failure triage, resnapshot and
  normalize-platforms levers
- [enrichment.md](enrichment.md) - enrichment service: catalog and
  pricing quarantine, providers and stub modes, the nightly refresh
  walk; telemetry reference, dashboard vg-enrichment, moderation levers
- [social.md](social.md) - social service: follows, likes, comments,
  and the activity feed over collection's shelves and user's profiles;
  telemetry reference, dashboard vg-social, failure triage, purge
  semantics
- [user.md](user.md) - user service: profile and RBAC source of truth;
  telemetry reference, dashboard vg-user, failure triage, psql role
  levers

## Alert rules

The twenty-one rules live in
`deploy/charts/platform/files/alerting/vg-rules.yaml`, provisioned
into the `vgkeep` folder and evaluated every 1m. Each rule's
`runbook_url` annotation deep-links the runbook section below; that
section quotes the rule's query verbatim in an indented code block,
so the number you read while triaging is the number that fired. When
a rule fires, follow its `runbook_url` from the alert instance or
from Alerting > Alert rules in Grafana (localhost:3000).

| Rule | Severity | Triage |
|---|---|---|
| vg-service-5xx - Service 5xx ratio above 5 percent | page | [stack.md](stack.md#1-service-5xx-ratio-above-5-percent) |
| vg-service-p99 - Service p99 latency above 500ms | warn | [stack.md](stack.md#2-service-p99-latency-above-500ms) |
| vg-pod-churn - Pod restart churn or OOM kill | warn | [stack.md](stack.md#4-pod-restart-churn-or-oom-kill) |
| vg-node-pressure - Node under memory, disk or PID pressure | page | [stack.md](stack.md#5-node-under-memory-disk-or-pid-pressure) |
| vg-pg-saturation - Postgres connections above 80 percent of max | warn | [stack.md](stack.md#6-postgres-connections-above-80-percent-of-max) |
| vg-valkey-pressure - Valkey evicting keys or memory unusually high | warn | [stack.md](stack.md#7-valkey-evicting-keys-or-memory-unusually-high) |
| vg-mongo-down - MongoDB unreachable | page | [enrichment.md](enrichment.md#2-mongo-down) |
| vg-collector-drops - OTel collector dropping telemetry | warn | [stack.md](stack.md#telemetry-pipeline-operations) |
| vg-denylist-failopen - BFF denylist failing open | page | [bff.md](bff.md#1-valkey-unreachable) |
| vg-loki-errors - Error log spike | warn | [stack.md](stack.md#3-error-log-spike) |
| vg-auth-jwks-empty - Auth JWKS has no active signing keys | page | [auth.md](auth.md#6-platform-wide-401s-jwks-trouble) |
| vg-auth-refresh-reuse - Auth refresh token reuse detected | warn | [auth.md](auth.md#3-refresh-reuse-detections) |
| vg-auth-provider-errors - Auth login provider failing | warn | [auth.md](auth.md#1-logins-failing-at-the-provider-hop) |
| vg-bff-refresh-failures - BFF session refresh failures spiking | warn | [bff.md](bff.md#3-refresh-failure-storm-mass-logout) |
| vg-bff-valkey-pool-timeouts - BFF Valkey client pool exhausted | warn | [bff.md](bff.md#5-valkey-client-pool-exhaustion) |
| vg-collection-pricing-degraded - Collection pricing composition degraded | warn | [collection.md](collection.md#1-enrichment-unreachable) |
| vg-collection-submissions-backlog - Collection submission queue not draining | warn | [collection.md](collection.md#6-submission-queue-not-draining) |
| vg-enrichment-refresh-stalled - Enrichment nightly price walk has not completed in 26h | warn | [enrichment.md](enrichment.md#4-nightly-walk-missing) |
| vg-enrichment-search-degraded - Enrichment search serving degraded answers | warn | [enrichment.md](enrichment.md#3-search-degraded) |
| vg-user-upsert-5xx - User profile upsert failing (logins blocked) | page | [user.md](user.md#1-logins-fail-at-the-upsert-leg) |
| vg-social-down - social service down | page | [social.md](social.md#1-social-down) |

`page` marks user-visible breakage worth interrupting someone for;
`warn` is everything else worth investigating on the next pass. The
dev tier has no contact point configured on purpose, so nothing here
pages out: alerts are read from the Grafana UI (Alerting > Alert
rules for the current state of all twenty-one, Alerting > Active
alerts for what is firing right now).
