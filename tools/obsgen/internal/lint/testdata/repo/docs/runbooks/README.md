# Runbooks

Fixture-only stand-in for docs/runbooks/README.md, sized just for
lint.Run's tests: one row per uid validModel() expands to, in the same
"<uid> - <title>" cell format the runbook-index-row-missing check
parses.

## Alert rules

| Rule                                          | Severity | Triage |
| ---------------------------------------------- | -------- | ------ |
| vg-cluster-thing - Cluster thing               | warn     | [stack.md](stack.md#2-cluster-thing) |
| vg-widget-down - Widget service down           | crit     | [stack.md](stack.md#1-widget-down-hard) |
| vg-widget-spins-high - Widget spins elevated   | warn     | [widget.md](widget.md#2-widgets-queue-backlog) |
| vg-widget-loki-check - Widget error log spike  | warn     | [widget.md](widget.md#2-widgets-queue-backlog) |
