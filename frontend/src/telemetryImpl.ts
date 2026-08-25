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

// Heavy half of the telemetry split: OTel SDK lives here so
// telemetry.ts stays out of the SDK's module graph, ships as its own
// lazy chunk.

// Telemetry leaves through the bff's same-origin OTLP relay; CSP
// (connect-src 'self') forbids external collectors.
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

// Turns on fetch tracing (span + traceparent per call, sharing one
// trace with server work) and creates the metric instruments.
// processor/metricReader are test seams; the idempotency gate lives in
// the facade, not here.
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
        // Never trace the relay itself (self-upload spans would feed
        // the exporters forever). Short-circuits before span creation,
        // so applyCustomAttributesOnSpan below needs no separate guard.
        ignoreUrls: [/\/api\/otlp\//],
        // result is the real Response even on an HTTP error status;
        // only a rejected fetch() promise (DNS, refused, CORS) hands
        // back an Error.
        applyCustomAttributesOnSpan: (_span, _request, result) => {
          if (result instanceof Error) recordApiNetworkFailure()
        },
      }),
    ],
  })

  // Named, not inlined, so the visibilitychange listener below can
  // force-flush this exact reader instance.
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
    // Explicit bucket boundaries via instrument "advice" (sdk-metrics
    // 2.9's DefaultAggregation reads it, no View needed). Straddle each
    // metric's official thresholds; default buckets are useless for CLS's 0-1 range.
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

  // Default mode (no reportAllChanges): each callback fires once per
  // page load with the final value. jsdom's PerformanceObserverStub
  // (test/setup.ts) reports empty supportedEntryTypes, so these three
  // calls register inertly under test (never invoke, never throw).
  onLCP((metric) => handleWebVital('LCP', metric.value, metric.rating))
  onINP((metric) => handleWebVital('INP', metric.value, metric.rating))
  onCLS((metric) => handleWebVital('CLS', metric.value, metric.rating))

  // Guarded by the facade's started check. No message/stack read off
  // either event: unbounded cardinality; the trace pipeline already
  // carries that detail.
  window.addEventListener('error', () => recordUncaughtError('error'))
  window.addEventListener('unhandledrejection', () => recordUncaughtError('unhandledrejection'))

  // LCP/CLS/INP only finalize once the page hides; the periodic
  // reader's export interval isn't guaranteed to tick again before the
  // tab closes, dropping that beacon. Best-effort tail flush; a
  // failure here is swallowed on purpose, loss is accepted.
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      void reader.forceFlush().catch(() => {})
    }
  })

  return instruments
}
