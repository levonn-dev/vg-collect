# Service 5xx ratio above 5 percent

Severity: page. More than 5 percent of a service's responses were 5xx
for 5 minutes.

Query (same as the alert and the Service HTTP RED dashboard):

    sum by (service_name) (rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m])) / sum by (service_name) (rate(http_server_request_duration_seconds_count[5m]))

Triage:

1. Open the Service HTTP RED dashboard (vg-service-red), select the
   firing service, and find which route carries the errors.
2. Jump from a p99 exemplar dot to the Jaeger trace, or open the error
   logs panel and follow a trace link from a log line.
3. Check the pod details dashboard (vg-pod-details) for restarts or
   OOM kills on the same window.
4. If the errors are 502 upstream_error from the bff, the fault is in
   the named downstream service, not the bff.
