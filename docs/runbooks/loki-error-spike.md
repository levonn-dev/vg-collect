# Error log spike

Severity: warn. A service logged more than 20 ERROR lines in a 5 minute
window, sustained for 5 minutes.

Query (same filter as the error logs panel on the Service HTTP RED
dashboard, counted over a 5 minute window):

    sum by (service_name) (count_over_time({service_name=~".+"} | severity_text="ERROR" [5m]))

Triage:

1. Open Explore against Loki with the rule's LogQL (drop the count_over_time
   wrapper to read the raw lines) and read the error messages for the firing
   service.
2. Follow the trace link on a log line (every OTLP log line carries
   trace_id) into Jaeger to see the full request that produced it.
3. Correlate the spike with panel 6 on the Service HTTP RED dashboard
   (vg-service-red) for the same service and window.
