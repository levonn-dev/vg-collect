# BFF denylist failing open

Severity: page. The bff skipped at least one revoked-token check because
Valkey was unreachable. This rule has no for delay: it fires on the first
occurrence.

Query (same signal as the vg_bff_cache_fail_open_total panel on the Service
HTTP RED dashboard (vg-service-red), filtered to denylist operations over a
10 minute window):

    sum(increase(vg_bff_cache_fail_open_total{op=~"denylist_.*"}[10m]))

Triage:

1. Revoked-token checks are being skipped by design (requests proceed rather
   than blocking); this is a degraded-availability signal, not a functional
   bug.
2. The Service HTTP RED dashboard (vg-service-red) plots the same counter
   broken out by op; use it to see whether the spike is isolated to
   denylist checks or every bff cache operation is failing open.
3. Check the bff-valkey pod's health, then search the bff logs for the line
   `valkey unavailable; failing open` to confirm the cause.
4. While this fires, a session that was just logged out or revoked keeps its
   access token usable for the remainder of its TTL (5 minutes max); this is
   a bounded, known exposure window, not an open-ended one.
