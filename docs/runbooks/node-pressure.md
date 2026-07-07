# Node under memory, disk or PID pressure

Severity: page. A node reported a memory, disk or PID pressure condition, or
had under 10 percent memory available, for 5 minutes.

Query (combines the same two queries the Node details dashboard plots
separately: the pressure condition panel and the available-memory ratio
panel):

    kube_node_status_condition{condition=~"MemoryPressure|DiskPressure|PIDPressure", status="true"} > 0 or (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) < 0.10

Triage:

1. Open the Node details dashboard (vg-node-details) and confirm which
   condition is set and how low available memory has gone.
2. On Docker Desktop or another local VM-backed cluster, raise the VM's
   memory allocation; this is almost always a host sizing problem, not an
   application bug.
3. Identify the top memory or disk consumer on the Pod details dashboard
   (vg-pod-details) in case one pod is disproportionately responsible.
