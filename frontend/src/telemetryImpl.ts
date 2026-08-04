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
import { handleWebVital, recordApiNetworkFailure, recordUncaughtError } from './telemetry'

// The heavy half of the telemetry split: everything that needs the
// OTel SDK lives here so telemetry.ts (the facade every page imports)
// stays out of the SDK's module graph and the SDK ships in its own
// lazy chunk. Only telemetry.ts loads this module, via dynamic import
// inside initTelemetry - the static imports of the facade above are
// safe because the facade is always fully evaluated first.

// Browser telemetry leaves through the bff's same-origin OTLP relay;
// the CSP (connect-src 'self') forbids any external collector host.
const OTLP_PATH = '/api/otlp/v1/traces'
const METRICS_OTLP_PATH = '/api/otlp/v1/metrics'

export interface Instruments {
  localeBoot: Counter
  catalogFailure: Counter
  localeSwitch: Counter
  proseFallback: Counter
  errors: Counter
  apiFailures: Counter
  lcp: Histogram
  inp: Histogram
  cls: Histogram
}

// start turns on fetch tracing: every API call gets a span and a
// traceparent header, so a browser interaction and the server work it
// caused share one trace. It also creates the locale/catalog metric
// instruments and returns them for the facade to hold. The optional
// processor/metricReader are test seams (production callers pass
// nothing and export both signals via OTLP). One-shot: the facade's
// started guard is the idempotency gate, not this function.
export function start(processor?: SpanProcessor, metricReader?: MetricReader, buildVersion?: string): Instruments {
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
  const instruments: Instruments = {
    localeBoot: meter.createCounter('vg.frontend.locale.boot', {
      description: 'Locale activated at app boot, by resolution source',
      valueType: ValueType.INT,
    }),
    catalogFailure: meter.createCounter('vg.frontend.locale.catalog_failures', {
      description: 'Catalog chunk fetch failures, by stage',
      valueType: ValueType.INT,
    }),
    localeSwitch: meter.createCounter('vg.frontend.locale.switches', {
      description: 'Mid-session locale switches',
      valueType: ValueType.INT,
    }),
    proseFallback: meter.createCounter('vg.frontend.prose.fallback_served', {
      description: 'Prose page served in English because the active locale had no contributed variant',
      valueType: ValueType.INT,
    }),
    errors: meter.createCounter('vg.frontend.errors', {
      description: 'Uncaught errors and unhandled promise rejections, by kind',
      valueType: ValueType.INT,
    }),
    apiFailures: meter.createCounter('vg.frontend.api_failures', {
      description: 'Fetch calls that rejected at the network level, as opposed to completing with an HTTP error status',
      valueType: ValueType.INT,
    }),
    // Explicit bucket boundaries via the instrument "advice" mechanism
    // (MetricAdvice.explicitBucketBoundaries, sdk-metrics 2.9's
    // DefaultAggregation reads it straight off the instrument - no View
    // needed on the MeterProvider above). Boundaries straddle each
    // metric's official good/needs-improvement/poor thresholds; the
    // library's own default histogram buckets are tuned for request
    // latencies and are useless for CLS's 0-1 fractional range.
    lcp: meter.createHistogram('vg.frontend.web_vitals.lcp', {
      description: 'Largest Contentful Paint, final value per page load',
      unit: 'ms',
      advice: { explicitBucketBoundaries: [500, 1000, 1500, 2000, 2500, 3000, 4000, 6000, 8000, 12000] },
    }),
    inp: meter.createHistogram('vg.frontend.web_vitals.inp', {
      description: 'Interaction to Next Paint, final value per page load',
      unit: 'ms',
      advice: { explicitBucketBoundaries: [50, 100, 150, 200, 300, 400, 500, 750, 1000, 2000] },
    }),
    cls: meter.createHistogram('vg.frontend.web_vitals.cls', {
      description: 'Cumulative Layout Shift score, final value per page load - unitless',
      advice: { explicitBucketBoundaries: [0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.25, 0.5, 1] },
    }),
  }

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

  // Registered only once, guarded by the facade's started check. No
  // message/stack/reason read off either event: unbounded cardinality
  // on a counter attribute, and the trace pipeline already carries
  // that detail for whichever request was in flight.
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

  return instruments
}
