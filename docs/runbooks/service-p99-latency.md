# Service p99 latency above 500ms

Severity: warn. A service's p99 request latency stayed above the 500ms
objective for 10 minutes.

Query (same as the alert and the p99 latency panel on the Service HTTP RED
dashboard):

    histogram_quantile(0.99, sum by (le, service_name) (rate(http_server_request_duration_seconds_bucket[5m])))

Triage:

1. Open the Service HTTP RED dashboard (vg-service-red) and use panel 4
   (per-route latency) to find which route is slow for the firing service.
2. Follow a p99 exemplar dot to its Jaeger trace and look for a slow
   database span; otelpgx, otelmongo and redisotel spans name the operation
   they ran.
3. Check the Datastores dashboard (vg-datastores) for the same window in
   case a Postgres, Mongo or Valkey instance is saturated rather than the
   service itself.
