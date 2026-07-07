# Postgres connections above 80 percent of max

Severity: warn. A Postgres instance's active connections stayed above 80
percent of max_connections for 5 minutes.

Query (divides the same two values the Datastores dashboard shows as
separate panels):

    sum by (service) (pg_stat_activity_count) / max by (service) (pg_settings_max_connections)

Triage:

1. Open the Datastores dashboard (vg-datastores); panel 1 shows which
   service's Postgres instance is affected.
2. Look for a connection leak: pool metrics carried on traces show whether a
   service is opening more connections than it returns; consider lowering
   that service's pool size.
3. max_connections is left at the postgres image's default; raise it via the
   chart's postgres args only with a documented reason, not as a first
   response.
