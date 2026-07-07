# MongoDB unreachable

Severity: page. mongodb_up read below 1, or produced no data at all, for 2
minutes; this rule treats missing data the same as down.

Query (same as the alert and the Datastores dashboard (vg-datastores)):

    mongodb_up

Triage:

1. Run `kubectl -n vg-collect get pods enrichment-mongo-0` to see whether
   the pod is down, crash-looping or just unready.
2. Read the container logs (`kubectl -n vg-collect logs enrichment-mongo-0
   -c mongo`) for the startup or crash reason.
3. While mongo is down, the enrichment service degrades to stale catalog
   reads rather than failing outright; confirm on the Service HTTP RED
   dashboard (vg-service-red) whether enrichment's error rate actually
   moved.
