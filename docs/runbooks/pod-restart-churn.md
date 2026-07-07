# Pod restart churn or OOM kill

Severity: warn. A pod restarted more than 3 times in 15 minutes, or was
OOM-killed, sustained for 5 minutes.

Query (combines the same two queries the Pod details dashboard plots
separately: restart count and OOM-kill count):

    sum by (namespace, pod) (increase(kube_pod_container_status_restarts_total{namespace=~"vg-collect|vg-platform"}[15m])) > 3 or sum by (namespace, pod) (kube_pod_container_status_last_terminated_reason{reason="OOMKilled", namespace=~"vg-collect|vg-platform"}) > 0

Triage:

1. Run `kubectl -n <namespace> describe pod <pod>` and read the last
   termination reason and event list.
2. If the reason is OOMKilled, the container's memory limit in its chart's
   resources block is too low for real usage, or the process is leaking;
   check the memory panel (panel 8) on the Service HTTP RED dashboard
   (vg-service-red) for a climbing trend before the kill.
3. If the reason is a crash rather than OOM, open the Pod details dashboard
   (vg-pod-details) for the same namespace and pod to correlate CPU and
   memory with the restarts, then read the container logs for the panic or
   fatal error.
