# Widget service runbook

Fixture-only stand-in for a per-service runbook. The second heading
below exercises the GitHub slug rules (period and apostrophe both
dropped, not replaced: "Widget's" becomes "widgets", not "widget-s").

## Failure modes

### 2. Widget's queue backlog

Investigate via:

```bash
curl -s http://localhost:9090/api/v1/query --data-urlencode 'query=sum(vg_widget_spins_count_total)'
```
