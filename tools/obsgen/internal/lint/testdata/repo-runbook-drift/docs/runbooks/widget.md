# Widget service runbook (drift fixture)

Deliberately cites a metric nothing in this fixture's (empty) services/
or libs/go/ registers, in both a PromQL-parseable fenced block and a
shell-quoted one - the walk->step class of drift this check exists to
catch. Section 3 is the jurisdiction control: a real, non-vg_ metric
name (this repo's own HTTP server instrumentation emits it, but never
registers it by hand, so it can never appear in Known()'s set either)
that must NOT fire, proving the runbook-scan path's unknown-metric
check is narrowed to vg_-prefixed names the same as the manifest/panel
path.

## Failure modes

### 1. Catalog refresh missing

```
sum(increase(vg_widget_refresh_walk_seconds_count[26h]))
```

### 2. Alternate view

```bash
curl -s http://localhost:9090/api/v1/query --data-urlencode 'query=vg_widget_refresh_walk_seconds_count'
```

### 3. Non-vg_ name, outside jurisdiction

```
sum(rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m]))
```
