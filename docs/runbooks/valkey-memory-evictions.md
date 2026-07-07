# Valkey evicting keys or memory unusually high

Severity: warn. A Valkey instance evicted keys, or held over 200MiB of
memory, for 5 minutes.

Query (combines the same two queries the Datastores dashboard
(vg-datastores) plots separately: eviction rate and memory used):

    rate(redis_evicted_keys_total[5m]) > 0 or redis_memory_used_bytes > 209715200

Triage:

1. Check the `service` label on the firing series to identify which Valkey
   instance (bff, collection or enrichment) is affected.
2. Evictions mean the cache is thrashing: Valkey's storage is an emptyDir
   with no maxmemory configured, so growth is unbounded until the pod's
   memory limit forces evictions or an OOM kill.
3. Consider shorter TTLs on the hot keys for that service; if memory keeps
   growing without any evictions, suspect a key leak (keys written without a
   TTL) instead.
