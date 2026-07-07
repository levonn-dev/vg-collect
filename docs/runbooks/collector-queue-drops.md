# OTel collector dropping telemetry

Severity: warn. The OTel pipeline failed to export at least one batch of
spans, metrics or logs, sustained for 5 minutes.

Query (same as the alert; there is no collector dashboard yet, use Explore
for the exporter breakdown):

    sum(rate({__name__=~"otelcol_exporter_(send|enqueue)_failed_.*"}[5m]))

Triage:

1. Open Explore against Prometheus and run `sum by (exporter)
   (rate({__name__=~"otelcol_exporter_(send|enqueue)_failed_.*"}[5m]))` to
   see which exporter (otlp, prometheusremotewrite) is failing.
2. Check whether the matching backend pod (kps-prometheus, loki, jaeger) is
   down or unreachable; that is the most common cause.
3. If every exporter is affected at once, check the otel-gateway pod for a
   memory_limiter processor refusing data under memory pressure.
4. Check the Pod details dashboard (vg-pod-details) for the otel-gateway
   and otel-agent pods in vg-platform: a collector starved of CPU or
   memory drops data before any backend is even involved.
