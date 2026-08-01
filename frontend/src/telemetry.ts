import { ValueType } from '@opentelemetry/api'
import type { Counter, Histogram } from '@opentelemetry/api'
import { W3CTraceContextPropagator } from '@opentelemetry/core'
import { OTLPMetricExporter } from '@opentelemetry/exporter-metrics-otlp-http'
import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-http'
import { registerInstrumentations } from '@opentelemetry/instrumentation'
import { FetchInstrumentation } from '@opentelemetry/instrumentation-fetch'
import { resourceFromAttributes } from '@opentelemetry/resources'
import { MeterProvider, PeriodicExportingMetricReader } from '@opentelemetry/sdk-metrics'
import type { MetricReader } from '@opentelemetry/sdk-metrics'
import { BatchSpanProcessor, WebTracerProvider } from '@opentelemetry/sdk-trace-web'
import type { SpanProcessor } from '@opentelemetry/sdk-trace-web'
import { ATTR_SERVICE_NAME, ATTR_SERVICE_VERSION } from '@opentelemetry/semantic-conventions'
import { onCLS, onINP, onLCP } from 'web-vitals'

// Browser telemetry leaves through the bff's same-origin OTLP relay;
// the CSP (connect-src 'self') forbids any external collector host.
const OTLP_PATH = '/api/otlp/v1/traces'
const METRICS_OTLP_PATH = '/api/otlp/v1/metrics'

let started = false

// The locale/prose counters, created once inside initTelemetry and
// held here so the record* exports below can add() to them. Each
// stays undefined until init runs, which is what makes every record*
// call below a safe no-op beforehand - module load, or a test that
// never calls initTelemetry - instead of a null-deref crash.
let localeBootCounter: Counter | undefined
let catalogFailureCounter: Counter | undefined
let localeSwitchCounter: Counter | undefined
let proseFallbackCounter: Counter | undefined
let errorsCounter: Counter | undefined
let apiFailuresCounter: Counter | undefined

// The web-vitals histograms, same no-op-before-init shape as the
// counters above - held here so handleWebVital below can record()
// into them once initTelemetry has run.
let lcpHistogram: Histogram | undefined
let inpHistogram: Histogram | undefined
let clsHistogram: Histogram | undefined

// Set inside initTelemetry from VITE_BUILD_VERSION (baked in at image
// build, same convention as the VITE_SITE_* identity slots). Empty
// string normalizes to undefined here so every reader below can use a
// single truthy check instead of also excluding ''.
let buildVersion: string | undefined

// initTelemetry turns on fetch tracing: every API call gets a span and
// a traceparent header, so a browser interaction and the server work
// it caused share one trace. It also creates the locale/catalog metric
// counters that lib/locale.ts and ProsePage record through below.
// Idempotent; the optional processor/metricReader are test seams
// (production callers pass nothing and export both signals via OTLP).
export function initTelemetry(processor?: SpanProcessor, metricReader?: MetricReader): void {
  if (started) return
  started = true
  buildVersion = import.meta.env.VITE_BUILD_VERSION || undefined
  const resource = resourceFromAttributes({
    [ATTR_SERVICE_NAME]: 'frontend',
    ...(buildVersion ? { [ATTR_SERVICE_VERSION]: buildVersion } : {}),
  })

  const provider = new WebTracerProvider({
    resource,
    spanProcessors: [processor ?? new BatchSpanProcessor(new OTLPTraceExporter({ url: OTLP_PATH }))],
  })
  provider.register({ propagator: new W3CTraceContextPropagator() })
  registerInstrumentations({
    instrumentations: [
      new FetchInstrumentation({
        // Never trace the relay itself: spans/metrics about their own
        // upload would feed the exporters forever. One prefix covers
        // both /api/otlp/v1/traces and /api/otlp/v1/metrics. A URL
        // matched here short-circuits inside the instrumentation's own
        // _createSpan (returns before any span exists), so
        // applyCustomAttributesOnSpan below never runs for it either -
        // no separate self-counting guard needed.
        ignoreUrls: [/\/api\/otlp\//],
        // result is the real Response on any completed request,
        // including one carrying an HTTP error status (status lives on
        // the Response, not an Error) - only a rejected fetch() promise
        // itself (DNS, connection refused, CORS) hands back an Error.
        applyCustomAttributesOnSpan: (_span, _request, result) => {
          if (result instanceof Error) recordApiNetworkFailure()
        },
      }),
    ],
  })

  // Named (not inlined into the array below) so the visibilitychange
  // listener at the end of this function can force-flush this exact
  // reader instance directly, matching how tests already hold their
  // own injected reader to flush by hand.
  const reader =
    metricReader ??
    new PeriodicExportingMetricReader({ exporter: new OTLPMetricExporter({ url: METRICS_OTLP_PATH }) })
  const meterProvider = new MeterProvider({ resource, readers: [reader] })
  const meter = meterProvider.getMeter('frontend')
  localeBootCounter = meter.createCounter('vg.frontend.locale.boot', {
    description: 'Locale activated at app boot, by resolution source',
    valueType: ValueType.INT,
  })
  catalogFailureCounter = meter.createCounter('vg.frontend.locale.catalog_failures', {
    description: 'Catalog chunk fetch failures, by stage',
    valueType: ValueType.INT,
  })
  localeSwitchCounter = meter.createCounter('vg.frontend.locale.switches', {
    description: 'Mid-session locale switches',
    valueType: ValueType.INT,
  })
  proseFallbackCounter = meter.createCounter('vg.frontend.prose.fallback_served', {
    description: 'Prose page served in English because the active locale had no contributed variant',
    valueType: ValueType.INT,
  })
  errorsCounter = meter.createCounter('vg.frontend.errors', {
    description: 'Uncaught errors and unhandled promise rejections, by kind',
    valueType: ValueType.INT,
  })
  apiFailuresCounter = meter.createCounter('vg.frontend.api_failures', {
    description: 'Fetch calls that rejected at the network level, as opposed to completing with an HTTP error status',
    valueType: ValueType.INT,
  })

  // Explicit bucket boundaries via the instrument "advice" mechanism
  // (MetricAdvice.explicitBucketBoundaries, sdk-metrics 2.9's
  // DefaultAggregation reads it straight off the instrument - no View
  // needed on the MeterProvider above). Boundaries straddle each
  // metric's official good/needs-improvement/poor thresholds; the
  // library's own default histogram buckets are tuned for request
  // latencies and are useless for CLS's 0-1 fractional range.
  lcpHistogram = meter.createHistogram('vg.frontend.web_vitals.lcp', {
    description: 'Largest Contentful Paint, final value per page load',
    unit: 'ms',
    advice: { explicitBucketBoundaries: [500, 1000, 1500, 2000, 2500, 3000, 4000, 6000, 8000, 12000] },
  })
  inpHistogram = meter.createHistogram('vg.frontend.web_vitals.inp', {
    description: 'Interaction to Next Paint, final value per page load',
    unit: 'ms',
    advice: { explicitBucketBoundaries: [50, 100, 150, 200, 300, 400, 500, 750, 1000, 2000] },
  })
  clsHistogram = meter.createHistogram('vg.frontend.web_vitals.cls', {
    description: 'Cumulative Layout Shift score, final value per page load - unitless',
    advice: { explicitBucketBoundaries: [0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.25, 0.5, 1] },
  })

  // Default reporting mode (no reportAllChanges passed): each callback
  // fires once per page load with the metric's final value, which is
  // what a proactive baseline needs - one data point per vital per
  // session rather than a stream of interim readings. jsdom's
  // PerformanceObserverStub (src/test/setup.ts) reports an empty
  // supportedEntryTypes, so web-vitals' own feature detection finds
  // nothing to observe and these three calls register inertly under
  // test - never invoking their callback, never throwing.
  onLCP((metric) => handleWebVital('LCP', metric.value, metric.rating))
  onINP((metric) => handleWebVital('INP', metric.value, metric.rating))
  onCLS((metric) => handleWebVital('CLS', metric.value, metric.rating))

  // Registered only once, guarded by the started check above like
  // everything else in this function. No message/stack/reason read off
  // either event: unbounded cardinality on a counter attribute, and the
  // trace pipeline already carries that detail for whichever request
  // was in flight.
  window.addEventListener('error', () => recordUncaughtError('error'))
  window.addEventListener('unhandledrejection', () => recordUncaughtError('unhandledrejection'))

  // LCP and CLS (and in practice INP too) only finalize - and so only
  // reach the onX callbacks above - once the page is hidden or
  // backgrounded; the periodic reader's own export interval has no
  // guarantee of ticking again before the tab actually closes, which
  // would silently drop that last beacon. A flush failure here must
  // never itself surface as an uncaught rejection, hence the swallowed
  // catch - this is a best-effort tail flush, and losing it is
  // accepted (documented, not silently ignored).
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      void reader.forceFlush().catch(() => {})
    }
  })
}

// recordLocaleBoot fires once per successful activateBoot: the locale
// that ended up active, which rung of the resolution ladder picked it
// (stored choice, browser language, or the en default), and the
// browser's language for correlation. browserLanguage is truncated to
// its primary subtag here (ISO 639-1, e.g. "en" out of "en-GB") so the
// attribute's cardinality stays bounded regardless of what the caller
// passes in.
export function recordLocaleBoot(
  locale: string,
  source: 'stored' | 'browser' | 'fallback',
  browserLanguage: string | undefined,
): void {
  localeBootCounter?.add(1, {
    locale,
    source,
    browser_language: browserLanguage?.split('-')[0],
  })
}

// recordCatalogFailure fires when a non-en catalog chunk fails to
// fetch, at either stage that can trigger one: boot (activateBoot
// falls back to the static en catalog) or switch (setLocale keeps the
// prior locale active instead).
export function recordCatalogFailure(stage: 'boot' | 'switch', locale: string): void {
  catalogFailureCounter?.add(1, { stage, locale })
}

// recordLocaleSwitch fires once per setLocale call that reaches a
// supported locale, before activation - "to" may equal "from" if a
// user reselects their current language.
export function recordLocaleSwitch(from: string, to: string): void {
  localeSwitchCounter?.add(1, { from, to })
}

// recordProseFallback fires when ProsePage serves the English variant
// to a non-en locale because no translation was contributed for it.
export function recordProseFallback(page: string): void {
  proseFallbackCounter?.add(1, { page })
}

// recordUncaughtError fires from the window 'error'/'unhandledrejection'
// listeners initTelemetry registers above, or directly from
// ErrorBoundary.componentDidCatch for a render crash it caught
// ('boundary' - never reaches those window listeners, so this is not
// a double count; see the why-comment on ErrorBoundary itself). version
// rides along when VITE_BUILD_VERSION was set at build time, via a
// conditional spread so the attribute is absent (not an empty string)
// when it wasn't.
export function recordUncaughtError(kind: 'error' | 'unhandledrejection' | 'boundary'): void {
  errorsCounter?.add(1, { kind, ...(buildVersion ? { version: buildVersion } : {}) })
}

// recordApiNetworkFailure fires from the fetch instrumentation's
// applyCustomAttributesOnSpan callback, wired in initTelemetry above,
// on a rejected fetch() promise - DNS, connection refused, CORS. A
// completed request that merely carries an HTTP error status never
// reaches this: it ends the span through the ordinary response path.
export function recordApiNetworkFailure(): void {
  apiFailuresCounter?.add(1)
}

// handleWebVital is the single seam the onLCP/onINP/onCLS callbacks
// wired in initTelemetry above delegate to - also exported so tests
// can drive it directly with synthetic values, since jsdom cannot
// produce the real paint/input events web-vitals needs to report on
// its own. Safe no-op before initTelemetry runs, like every record*
// export in this file. version rides along under the same conditional
// spread as recordUncaughtError above.
export function handleWebVital(name: 'LCP' | 'INP' | 'CLS', value: number, rating: string): void {
  const attributes = { rating, ...(buildVersion ? { version: buildVersion } : {}) }
  switch (name) {
    case 'LCP':
      lcpHistogram?.record(value, attributes)
      break
    case 'INP':
      inpHistogram?.record(value, attributes)
      break
    case 'CLS':
      clsHistogram?.record(value, attributes)
      break
  }
}
