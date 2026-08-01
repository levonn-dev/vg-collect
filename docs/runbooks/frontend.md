# Frontend telemetry

This is not a service runbook in the usual sense: the SPA has no pod,
ships no server logs, and owns no alert rule. What it has earned is
its own dashboard (`vg-frontend`) and nine browser-side telemetry
instruments dense enough to deserve a document of their own instead of
a paragraph inside [stack.md](stack.md). Everything below - metrics,
dashboard, failure scenarios - concerns code in
`frontend/src/telemetry.ts`; there is no architecture, running-it,
configuration, datastore, admin-lever, or capacity story to tell,
because there is no running process on the other end of these pages,
only a browser.

## Telemetry

Nine instruments - six counters and three web-vitals histograms - are
created once in `frontend/src/telemetry.ts` and pushed through the
same OTLP pipeline every backend service uses, with one browser-specific
hop: browser -> bff `POST /api/otlp/v1/metrics` (session-gated relay,
1 MiB cap, the same handler and semantics as the traces leg -
[bff.md](bff.md#7-browser-telemetry-relay-failing)) -> otel-agent ->
otel-gateway -> Prometheus (remote write). The SPA also emits traces
over the sibling `POST /api/otlp/v1/traces` route (one span per fetch
call, stitched to whatever backend trace it caused); that signal is
covered by the pipeline topology in
[stack.md](stack.md#telemetry-pipeline-operations) and read back in
Jaeger under service `frontend` - this page is about the nine metric
instruments and their dashboard.

Every instrument here samples signed-in sessions only: the relay
requires the same session cookie as every other `/api` route, so
anonymous and pre-login browsing never reaches a single counter or
histogram below. That is by design, not a coverage gap to close -
these instruments exist to catch regressions in the deployed app, not
to size anonymous traffic.

### Metrics

| Prometheus name | Instrument | Unit | Attrs (bounded values) | Answers |
|---|---|---|---|---|
| `vg_frontend_locale_boot_total` | counter (`vg.frontend.locale.boot`) | (none) | `locale`; `source`: `stored`, `browser`, `fallback`; `browser_language` (primary subtag only, e.g. `en`) | which locale activates at boot and how it was resolved; a rising `source="fallback"` share means the stored-choice and browser-language rungs are missing more often than they should. Summed without any label filter, this counter is also the boot-count denominator - see below |
| `vg_frontend_locale_catalog_failures_total` | counter (`vg.frontend.locale.catalog_failures`) | (none) | `stage`: `boot`, `switch`; `locale` | how often a locale catalog chunk fails to fetch, and whether it happened at boot or a mid-session switch |
| `vg_frontend_locale_switches_total` | counter (`vg.frontend.locale.switches`) | (none) | `from`, `to` | mid-session locale changes and which locales they land on |
| `vg_frontend_prose_fallback_served_total` | counter (`vg.frontend.prose.fallback_served`) | (none) | `page` | which informational pages serve English prose because the active locale has no contributed translation for it |
| `vg_frontend_errors_total` | counter (`vg.frontend.errors`) | (none) | `kind`: `error`, `unhandledrejection`, `boundary`; `version` (present only when the image was built with `VITE_BUILD_VERSION` set) | uncaught errors and unhandled promise rejections reaching `window`, plus render crashes an error boundary caught, by kind; sliceable by build version to catch a stale-cache deploy spike |
| `vg_frontend_api_failures_total` | counter (`vg.frontend.api_failures`) | (none) | none | fetch calls that rejected at the network level (DNS, connection refused, CORS), as opposed to completing with an HTTP error status - see below |
| `vg_frontend_web_vitals_lcp_milliseconds_{count,sum,bucket}` | histogram (`vg.frontend.web_vitals.lcp`) | ms | `rating`: `good`, `needs-improvement`, `poor`; `version` (same condition as above) | Largest Contentful Paint, final value per page load - loading performance |
| `vg_frontend_web_vitals_inp_milliseconds_{count,sum,bucket}` | histogram (`vg.frontend.web_vitals.inp`) | ms | same as LCP | Interaction to Next Paint, final value per page load - responsiveness |
| `vg_frontend_web_vitals_cls_{count,sum,bucket}` | histogram (`vg.frontend.web_vitals.cls`) | (unitless score) | same as LCP | Cumulative Layout Shift, final value per page load - visual stability |

The `_milliseconds_` infix on the two ms-unit histograms is the
OTel-to-Prometheus translator expanding the `ms` unit into a name
suffix - the same mechanism that turns every backend service's
`s`-unit HTTP histogram into `..._seconds_bucket`. `vg.frontend.web_vitals.cls`
declares no unit, so its Prometheus name carries no such infix. Verified
against the live pipeline, not assumed - the dot-to-underscore and
`_total`/`_bucket`/`_sum`/`_count` suffixing is otherwise the same as
every other counter and histogram in this stack.

`vg_frontend_locale_boot_total` fires exactly once per successful app
boot (`recordLocaleBoot`, called once from `activateBoot`), so summed
without any label filter it is the boot count - the denominator for
turning any other counter above into a per-boot rate:

    # error-per-boot rate (5m window)
    sum(rate(vg_frontend_errors_total[5m])) / sum(rate(vg_frontend_locale_boot_total[5m]))

    # catalog-failure-per-boot rate, boot stage only (5m window)
    sum(rate(vg_frontend_locale_catalog_failures_total{stage="boot"}[5m])) / sum(rate(vg_frontend_locale_boot_total[5m]))

Both divide two independent counters, so expect a noisy ratio at low
traffic; read it over the same window every time, and prefer a 1h or
24h `increase()` ratio over dev-tier traffic volumes.

`vg_frontend_api_failures_total` counts only the failure class the
bff's own RED metrics cannot see: a `fetch()` promise that rejected
before any response existed. A completed request that merely carries
an HTTP error status (a 500, a 401) is not counted here - it shows up
in the calling service's own error rate instead. A climbing
`vg_frontend_api_failures_total` with no matching climb in any
backend's 5xx ratio points at the client's own network path (offline,
DNS, a misbehaving proxy, CORS), not a backend incident.

`vg_frontend_errors_total`'s `kind` attribute is `error` or
`unhandledrejection` from the two `window` listeners `initTelemetry`
registers, or `boundary` from `ErrorBoundary.componentDidCatch`
(`frontend/src/components/ErrorBoundary.tsx`): a render crash React
caught, where the user saw the fallback screen instead of a blank
page. A boundary-caught crash never reaches either `window` listener,
so counting it here is not a double count (see the why-comment on
`ErrorBoundary` itself for the verified detail).

### Web vitals

The three histograms feed from the `web-vitals` package's default
reporting mode (no `reportAllChanges`): one data point per vital per
page load, recorded when the browser finalizes that vital's value. LCP
and CLS (and in practice INP too) only finalize once the page is
hidden or backgrounded, so `initTelemetry` also registers a
`visibilitychange` listener that force-flushes the metric reader on
the hidden transition - a best-effort tail flush. A tab that closes
without ever backgrounding (killed process, crash) loses that
session's beacon; this is accepted, not a bug to chase - a p75 over
many sessions tolerates a small amount of tail loss.

Read these panels as **p75, not average**: a mean hides exactly the
slow tail the official thresholds care about. Both the stat and trend
panels on the dashboard compute a rolling one-hour p75:

    histogram_quantile(0.75, sum by (le) (rate(vg_frontend_web_vitals_lcp_milliseconds_bucket[1h])))

(swap the metric name for `_inp_milliseconds_` or `_cls_`). The `[1h]`
window is fixed rather than `$__rate_interval` on purpose: session-gated
browser traffic is low-volume, and a short rolling window starves the
bucket counts a stable quantile needs. Bucket boundaries (each
histogram's `advice.explicitBucketBoundaries`) straddle the thresholds
below so the quantile has real resolution near the good/poor line,
not just wherever a latency-tuned SDK default happens to fall.

Official thresholds - the same bounds the buckets straddle and the
dashboard's stat panels color by:

| Vital | Good | Poor |
|---|---|---|
| LCP | under 2500 ms | over 4000 ms |
| INP | under 200 ms | over 500 ms |
| CLS | under 0.1 | over 0.25 |

The `version` attribute (present only when the image was built with
`VITE_BUILD_VERSION` set - `services/bff/Dockerfile` declares the
build arg, the Tiltfile forwards an explicit `.env` value through
unchanged, and CI owns setting it in a real build) lets a regression
get sliced by build: a p75 jump, or an error-rate jump, that lines up
with one `version` value and not the currently deployed one is a
browser holding a stale cached bundle, not a live incident. `VITE_BUILD_VERSION`
is unset in dev by design and is never derived from a git SHA by
Tilt - explicit-only, so an unset value stays absent rather than
guessed.

### No alert rules

Nothing here pages: `vg-rules.yaml` gains no rule from this dashboard,
on purpose. Catalog failures already degrade gracefully (failure
scenario 1 below); the error counter, the network-failure counter, and
all three vitals histograms are new instruments with no production
baseline yet. A threshold picked before anyone has seen a week of real
traffic is a guess, and a guessed threshold either pages on ordinary
noise or misses a real regression. The approach is baselines first: let
the dashboard accumulate real signal, then propose thresholds backed
by it. Until then, read this dashboard on the same cadence as any
other - after a deploy, or when investigating a report - rather than
waiting to be paged.

## Dashboard: vg-frontend

Provisioned from `deploy/charts/platform/files/dashboards/frontend.json`
into the vgkeep folder, uid `vg-frontend`, title `Frontend Telemetry`.
Open it at http://localhost:3000/d/vg-frontend while `task run` holds
the Grafana port-forward. Same structural conventions as every vgkeep
dashboard (schemaVersion 39, tag `vgkeep`, browser timezone, 30s
refresh, explicit `{"type": "prometheus", "uid": "prometheus"}`
datasource per target) - except the layout contract every service
dashboard follows (HTTP RED per route first, then domain metrics, then
datastores, then pods and logs) does not apply: there is no HTTP RED,
no datastore, and no pod, so the panels run metric family to metric
family.

| Panel | Type | Reads |
|---|---|---|
| Locale boots by source | timeseries | `sum by (source) (rate(vg_frontend_locale_boot_total[5m]))` |
| Browser languages hitting fallback, 24h | table | `topk(10, sum by (browser_language) (increase(vg_frontend_locale_boot_total{source="fallback"}[24h])))` |
| Catalog failures by stage | timeseries | `sum by (stage) (increase(vg_frontend_locale_catalog_failures_total[5m]))` |
| Catalog failures, 24h | stat, red on nonzero | `sum(increase(vg_frontend_locale_catalog_failures_total[24h]))` |
| Locale switches by target | timeseries | `sum by (to) (increase(vg_frontend_locale_switches_total[5m]))` |
| Prose fallbacks by page, 24h | barchart | `sum by (page) (increase(vg_frontend_prose_fallback_served_total[24h]))` |
| Uncaught errors | timeseries | `sum by (kind) (rate(vg_frontend_errors_total[5m]))` |
| Uncaught errors, 24h | stat, red on nonzero | `sum(increase(vg_frontend_errors_total[24h]))` |
| API network failures | timeseries | `rate(vg_frontend_api_failures_total[5m])` |
| LCP p75, 1h | stat, colored at 2500/4000 ms | `histogram_quantile(0.75, sum by (le) (rate(vg_frontend_web_vitals_lcp_milliseconds_bucket[1h])))` |
| INP p75, 1h | stat, colored at 200/500 ms | `histogram_quantile(0.75, sum by (le) (rate(vg_frontend_web_vitals_inp_milliseconds_bucket[1h])))` |
| CLS p75, 1h | stat, colored at 0.1/0.25 | `histogram_quantile(0.75, sum by (le) (rate(vg_frontend_web_vitals_cls_bucket[1h])))` |
| LCP p75 trend | timeseries | same query as the LCP stat |
| INP p75 trend | timeseries | same query as the INP stat |
| CLS p75 trend | timeseries | same query as the CLS stat |

The three vitals land as six panels rather than three or one combined
panel: LCP and INP share a unit (ms) but differ by an order of
magnitude in typical value, and CLS has no unit at all, so a single
shared-axis panel would either mislabel CLS or flatten it against LCP.
Stacking a stat above its own trend, one column per vital, keeps every
number correctly scaled and lets a triager read "is it bad right now"
and "has it been trending that way" in the same glance.

## Failure modes and triage

### 1. Locale catalog fetch failing

A non-en catalog chunk fails to fetch (CDN blip, bad deploy, offline
client). `activateBoot` and `setLocale` both degrade gracefully to the
static English catalog - the user sees the app in English, never an
error screen - and record `vg_frontend_locale_catalog_failures_total{stage,locale}`.
A nonzero rate is a CDN-or-deploy investigation (confirm the failing
locale's chunk actually shipped and is reachable), not a page-worthy
incident: nobody is broken, some users are seeing English they should
not be. "Catalog failures by stage" on the dashboard tells a boot-time
miss (the whole session degrades) from a mid-switch miss (the user
just stays on their prior locale).

### 2. Telemetry relay unreachable

Every instrument on this page depends on the bff's
`POST /api/otlp/v1/metrics` relay (same handler, same session gate,
same 1 MiB cap as the traces leg). When the relay cannot reach the
collector, the failure is silent by design on the client: the browser
SDK's periodic exporter drops the batch and moves on - no user-visible
error, no retry storm. It is not silent server-side: the bff logs
`browser telemetry relay failed` (WARN, field `err`) and its own RED
metrics count the failing response on `POST /api/otlp/v1/metrics`. So
this dashboard going quiet while the rest of the pipeline looks
healthy is a [bff.md](bff.md#7-browser-telemetry-relay-failing)
investigation, not a frontend one - there is nothing to triage on the
client side, because the client has nothing left to tell you.

### 3. Stale-deploy skew

A spike in `vg_frontend_errors_total`, or a vitals regression, that
clusters on one `version` attribute value older than the currently
deployed image tag is a browser holding a stale cached bundle, not a
live incident - see the version attribute note under Web vitals above
(the same attribute rides on the error counter). Compare against the
rolled-out image tag before treating it as a regression:

    kubectl -n vgkeep get deployment bff -o jsonpath='{.spec.template.spec.containers[0].image}'
