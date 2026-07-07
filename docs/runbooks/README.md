# Alert runbooks

These are the triage guides linked from every Grafana alert rule's
`runbook_url` annotation (the rules themselves live in
`deploy/charts/platform/files/alerting/vg-rules.yaml`, provisioned into
the `vg-collect` folder). When a rule fires, follow its `runbook_url`
from the alert instance or from Alerting > Alert rules in Grafana
(localhost:3000) straight to the matching file below. Every runbook
quotes its rule's query verbatim in an indented code block: the same
text the rule evaluates, and the same text (or the same building
blocks) the dashboard panel it mirrors uses, so the number you read
while triaging is always the number that fired.

| Runbook | Severity | Alert |
|---|---|---|
| [service-5xx-ratio.md](service-5xx-ratio.md) | page | Service 5xx ratio above 5 percent |
| [service-p99-latency.md](service-p99-latency.md) | warn | Service p99 latency above 500ms |
| [pod-restart-churn.md](pod-restart-churn.md) | warn | Pod restart churn or OOM kill |
| [node-pressure.md](node-pressure.md) | page | Node under memory, disk or PID pressure |
| [pg-connection-saturation.md](pg-connection-saturation.md) | warn | Postgres connections above 80 percent of max |
| [valkey-memory-evictions.md](valkey-memory-evictions.md) | warn | Valkey evicting keys or memory unusually high |
| [mongo-down.md](mongo-down.md) | page | MongoDB unreachable |
| [collector-queue-drops.md](collector-queue-drops.md) | warn | OTel collector dropping telemetry |
| [denylist-fail-open.md](denylist-fail-open.md) | page | BFF denylist failing open |
| [loki-error-spike.md](loki-error-spike.md) | warn | Error log spike |

`page` marks user-visible breakage worth interrupting someone for;
`warn` is everything else worth investigating on the next pass. The
dev tier has no contact point configured on purpose, so nothing here
pages out: alerts are read from the Grafana UI (Alerting > Alert
rules for the current state of all ten, Alerting > Active alerts for
what is firing right now).
