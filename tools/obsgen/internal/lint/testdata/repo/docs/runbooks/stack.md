# Widget stack runbook

Fixture-only stand-in for docs/runbooks/stack.md, sized just for
lint.Run's tests: cluster-level headings its expandedItem fixtures cite,
plus one clean fenced PromQL block (AST-checked path) referencing a
metric the paired Go fixtures under services/ and libs/go/ register.

## Alerting

### 1. Widget down, hard

The vg-widget-down rule fires when widget has no ready pods answering
scrapes:

```
up{namespace="vgkeep", pod=~"widget-.*"}
```

### 2. Cluster thing

A cluster-scoped rule's anchor target.
