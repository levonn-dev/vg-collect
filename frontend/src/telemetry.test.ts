import { context, propagation, trace } from '@opentelemetry/api'
import { AggregationTemporality, InMemoryMetricExporter, PeriodicExportingMetricReader } from '@opentelemetry/sdk-metrics'
import type { DataPoint, Histogram } from '@opentelemetry/sdk-metrics'
import { InMemorySpanExporter, SimpleSpanProcessor } from '@opentelemetry/sdk-trace-web'
import { ATTR_SERVICE_NAME, ATTR_SERVICE_VERSION } from '@opentelemetry/semantic-conventions'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Well beyond any test run: PeriodicExportingMetricReader starts this
// interval on bind, and tests only read via forceFlush().
const NEVER_TICK_MILLIS = 24 * 60 * 60 * 1000

const okResponse = () => new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })

function headersOf(call: unknown[]): Headers {
  const [input, init] = call as [RequestInfo, RequestInit | undefined]
  if (input instanceof Request) return input.headers
  return new Headers(init?.headers)
}

// Flush then look up one metric's points by descriptor name; each
// block builds its own MeterProvider under vi.resetModules(), so
// callers pass their own reader/exporter. Type param picks counter vs histogram shape.
async function findDataPoints<T = number>(
  metricReader: PeriodicExportingMetricReader,
  metricExporter: InMemoryMetricExporter,
  name: string,
): Promise<DataPoint<T>[]> {
  await metricReader.forceFlush()
  const [resourceMetrics] = metricExporter.getMetrics()
  const metric = resourceMetrics?.scopeMetrics
    .flatMap((sm) => sm.metrics)
    .find((m) => m.descriptor.name === name)
  return (metric?.dataPoints ?? []) as DataPoint<T>[]
}

describe('initTelemetry', () => {
  let exporter: InMemorySpanExporter
  let fetchStub: ReturnType<typeof vi.fn>

  beforeEach(async () => {
    vi.resetModules()
    exporter = new InMemorySpanExporter()
    fetchStub = vi.fn().mockResolvedValue(okResponse())
    vi.stubGlobal('fetch', fetchStub)
    const { initTelemetry } = await import('./telemetry')
    await initTelemetry(new SimpleSpanProcessor(exporter))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    // @opentelemetry/api keeps its tracer/propagator on real globalThis,
    // outliving vi.resetModules(); without disabling, only the first
    // test's register() wins.
    trace.disable()
    context.disable()
    propagation.disable()
  })

  it('traces a fetch and injects the traceparent header', async () => {
    await fetch('/api/entries')
    await vi.waitFor(() => expect(exporter.getFinishedSpans().length).toBe(1))
    const span = exporter.getFinishedSpans()[0]
    expect(span.attributes['http.url']).toContain('/api/entries')
    expect(headersOf(fetchStub.mock.calls[0]).get('traceparent')).toMatch(/^00-[0-9a-f]{32}-[0-9a-f]{16}-/)
  })

  it('never traces the relay path itself', async () => {
    await fetch('/api/otlp/v1/traces', { method: 'POST', body: '{}' })
    await new Promise((r) => setTimeout(r, 50))
    expect(exporter.getFinishedSpans()).toHaveLength(0)
    expect(headersOf(fetchStub.mock.calls[0]).get('traceparent')).toBeNull()
  })

  it('is idempotent: a second init never double-instruments', async () => {
    const { initTelemetry } = await import('./telemetry')
    await initTelemetry(new SimpleSpanProcessor(exporter))
    await fetch('/api/tags')
    await vi.waitFor(() => expect(exporter.getFinishedSpans().length).toBe(1))
  })
})

describe('locale and prose metric counters', () => {
  it('are safe no-ops before initTelemetry runs', async () => {
    vi.resetModules()
    const { recordCatalogFailure, recordLocaleBoot, recordLocaleSwitch, recordProseFallback } =
      await import('./telemetry')
    expect(() => recordLocaleBoot('en', 'stored', 'en-GB')).not.toThrow()
    expect(() => recordCatalogFailure('boot', 'en')).not.toThrow()
    expect(() => recordLocaleSwitch('en', 'de')).not.toThrow()
    expect(() => recordProseFallback('about')).not.toThrow()
  })

  it('replays a record buffered while init is pending', async () => {
    // Boot counter's survival path: main.tsx fires initTelemetry
    // unawaited while activateBoot records in the gap; a dropped boot
    // skews the dashboard denominator.
    vi.resetModules()
    const telemetry = await import('./telemetry')
    telemetry.recordLocaleBoot('ja', 'stored', 'ja-JP')
    const metricExporter = new InMemoryMetricExporter(AggregationTemporality.CUMULATIVE)
    const metricReader = new PeriodicExportingMetricReader({
      exporter: metricExporter,
      exportIntervalMillis: NEVER_TICK_MILLIS,
    })
    await telemetry.initTelemetry(new SimpleSpanProcessor(new InMemorySpanExporter()), metricReader)
    const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.locale.boot')
    expect(points).toHaveLength(1)
    expect(points[0].attributes).toEqual({ locale: 'ja', source: 'stored', browser_language: 'ja' })
    trace.disable()
    context.disable()
    propagation.disable()
  })

  describe('once initialized', () => {
    let metricExporter: InMemoryMetricExporter
    let metricReader: PeriodicExportingMetricReader
    let telemetry: typeof import('./telemetry')

    beforeEach(async () => {
      vi.resetModules()
      // InMemoryMetricExporter + manual forceFlush() gives a
      // deterministic read with no timer, mirroring InMemorySpanExporter above.
      metricExporter = new InMemoryMetricExporter(AggregationTemporality.CUMULATIVE)
      metricReader = new PeriodicExportingMetricReader({
        exporter: metricExporter,
        exportIntervalMillis: NEVER_TICK_MILLIS,
      })
      telemetry = await import('./telemetry')
      await telemetry.initTelemetry(new SimpleSpanProcessor(new InMemorySpanExporter()), metricReader)
    })

    afterEach(() => {
      // Same global-registry leak as the trace describe above.
      trace.disable()
      context.disable()
      propagation.disable()
    })

    it('records a locale boot with the locale, source, and browser primary subtag', async () => {
      telemetry.recordLocaleBoot('en', 'stored', 'en-GB')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.locale.boot')
      expect(points).toHaveLength(1)
      expect(points[0].value).toBe(1)
      expect(points[0].attributes).toEqual({ locale: 'en', source: 'stored', browser_language: 'en' })
    })

    it('omits browser_language when no browser language is available', async () => {
      telemetry.recordLocaleBoot('en', 'fallback', undefined)
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.locale.boot')
      expect(points[0].attributes).toEqual({ locale: 'en', source: 'fallback' })
    })

    it('records a catalog failure with the stage and locale', async () => {
      telemetry.recordCatalogFailure('switch', 'de')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.locale.catalog_failures')
      expect(points).toHaveLength(1)
      expect(points[0].value).toBe(1)
      expect(points[0].attributes).toEqual({ stage: 'switch', locale: 'de' })
    })

    it('records a locale switch with from and to', async () => {
      telemetry.recordLocaleSwitch('en', 'de')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.locale.switches')
      expect(points).toHaveLength(1)
      expect(points[0].value).toBe(1)
      expect(points[0].attributes).toEqual({ from: 'en', to: 'de' })
    })

    it('records a prose fallback with the page', async () => {
      telemetry.recordProseFallback('help')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.prose.fallback_served')
      expect(points).toHaveLength(1)
      expect(points[0].value).toBe(1)
      expect(points[0].attributes).toEqual({ page: 'help' })
    })

    it('accumulates repeated calls into the same counter', async () => {
      telemetry.recordLocaleSwitch('en', 'de')
      telemetry.recordLocaleSwitch('en', 'de')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.locale.switches')
      expect(points).toHaveLength(1)
      expect(points[0].value).toBe(2)
    })
  })
})

describe('uncaught-error and network-failure counters', () => {
  it('are safe no-ops before initTelemetry runs', async () => {
    vi.resetModules()
    const { recordApiNetworkFailure, recordUncaughtError } = await import('./telemetry')
    expect(() => recordUncaughtError('error')).not.toThrow()
    expect(() => recordUncaughtError('unhandledrejection')).not.toThrow()
    expect(() => recordUncaughtError('boundary')).not.toThrow()
    expect(() => recordApiNetworkFailure()).not.toThrow()
  })

  describe('once initialized', () => {
    let fetchStub: ReturnType<typeof vi.fn>
    let metricExporter: InMemoryMetricExporter
    let metricReader: PeriodicExportingMetricReader
    let telemetry: typeof import('./telemetry')

    beforeEach(async () => {
      vi.resetModules()
      fetchStub = vi.fn().mockResolvedValue(okResponse())
      vi.stubGlobal('fetch', fetchStub)
      metricExporter = new InMemoryMetricExporter(AggregationTemporality.CUMULATIVE)
      metricReader = new PeriodicExportingMetricReader({
        exporter: metricExporter,
        exportIntervalMillis: NEVER_TICK_MILLIS,
      })
      telemetry = await import('./telemetry')
      await telemetry.initTelemetry(new SimpleSpanProcessor(new InMemorySpanExporter()), metricReader)
    })

    afterEach(() => {
      vi.unstubAllGlobals()
      trace.disable()
      context.disable()
      propagation.disable()
    })

    it('records an uncaught error via the window error listener', async () => {
      window.dispatchEvent(new ErrorEvent('error'))
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.errors')
      expect(points).toHaveLength(1)
      expect(points[0].value).toBe(1)
      expect(points[0].attributes).toEqual({ kind: 'error' })
    })

    // jsdom has no PromiseRejectionEvent; dispatch matches by event.type
    // alone and kind comes from the listener's closure, so a base
    // Event exercises the real wiring.
    it('records an uncaught rejection via the window unhandledrejection listener', async () => {
      window.dispatchEvent(new Event('unhandledrejection'))
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.errors')
      expect(points).toHaveLength(1)
      expect(points[0].attributes).toEqual({ kind: 'unhandledrejection' })
    })

    it('records all three kinds through recordUncaughtError directly', async () => {
      telemetry.recordUncaughtError('error')
      telemetry.recordUncaughtError('unhandledrejection')
      telemetry.recordUncaughtError('boundary')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.errors')
      expect(points).toHaveLength(3)
      expect(points.map((p) => p.attributes.kind).sort()).toEqual(['boundary', 'error', 'unhandledrejection'])
    })

    it('accumulates repeated calls of the same kind into one data point', async () => {
      telemetry.recordUncaughtError('error')
      telemetry.recordUncaughtError('error')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.errors')
      expect(points).toHaveLength(1)
      expect(points[0].value).toBe(2)
    })

    it('records api_failures through recordApiNetworkFailure directly', async () => {
      telemetry.recordApiNetworkFailure()
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.api_failures')
      expect(points).toHaveLength(1)
      expect(points[0].value).toBe(1)
      expect(points[0].attributes).toEqual({})
    })

    it('records a network failure when a fetch call itself rejects', async () => {
      fetchStub.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      await expect(fetch('/api/entries')).rejects.toThrow('Failed to fetch')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.api_failures')
      expect(points).toHaveLength(1)
      expect(points[0].value).toBe(1)
    })

    it('does not record a network failure for a completed response carrying an HTTP error status', async () => {
      fetchStub.mockResolvedValueOnce(new Response('{}', { status: 500 }))
      await fetch('/api/entries')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.api_failures')
      expect(points).toHaveLength(0)
    })

    it('does not record a network failure for the ignored relay path (no self-counting)', async () => {
      fetchStub.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      await expect(fetch('/api/otlp/v1/traces', { method: 'POST', body: '{}' })).rejects.toThrow()
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.api_failures')
      expect(points).toHaveLength(0)
    })
  })

  describe('build-version stamping', () => {
    afterEach(() => {
      vi.unstubAllGlobals()
      vi.unstubAllEnvs()
      trace.disable()
      context.disable()
      propagation.disable()
    })

    async function initWithVersion(version: string | undefined) {
      vi.resetModules()
      if (version !== undefined) vi.stubEnv('VITE_BUILD_VERSION', version)
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(okResponse()))
      const spanExporter = new InMemorySpanExporter()
      const metricExporter = new InMemoryMetricExporter(AggregationTemporality.CUMULATIVE)
      const metricReader = new PeriodicExportingMetricReader({
        exporter: metricExporter,
        exportIntervalMillis: NEVER_TICK_MILLIS,
      })
      const telemetry = await import('./telemetry')
      await telemetry.initTelemetry(new SimpleSpanProcessor(spanExporter), metricReader)
      return { telemetry, spanExporter, metricExporter, metricReader }
    }

    it('stamps service.version onto both providers shared resource when set', async () => {
      const { telemetry, spanExporter, metricExporter, metricReader } = await initWithVersion('1.2.3')
      await fetch('/api/entries')
      await vi.waitFor(() => expect(spanExporter.getFinishedSpans().length).toBe(1))
      expect(spanExporter.getFinishedSpans()[0].resource.attributes).toEqual({
        [ATTR_SERVICE_NAME]: 'frontend',
        [ATTR_SERVICE_VERSION]: '1.2.3',
      })
      // Reader only exports once an instrument has a data point;
      // recordApiNetworkFailure here just makes the resource inspectable below.
      telemetry.recordApiNetworkFailure()
      await metricReader.forceFlush()
      const [resourceMetrics] = metricExporter.getMetrics()
      expect(resourceMetrics?.resource.attributes).toEqual({
        [ATTR_SERVICE_NAME]: 'frontend',
        [ATTR_SERVICE_VERSION]: '1.2.3',
      })
    })

    it('omits service.version from the resource when VITE_BUILD_VERSION is unset', async () => {
      const { telemetry, metricExporter, metricReader } = await initWithVersion(undefined)
      telemetry.recordApiNetworkFailure()
      await metricReader.forceFlush()
      const [resourceMetrics] = metricExporter.getMetrics()
      expect(resourceMetrics?.resource.attributes).toEqual({ [ATTR_SERVICE_NAME]: 'frontend' })
    })

    it('includes version on vg.frontend.errors adds when set', async () => {
      const { telemetry, metricExporter, metricReader } = await initWithVersion('1.2.3')
      telemetry.recordUncaughtError('error')
      const points = await findDataPoints(metricReader, metricExporter, 'vg.frontend.errors')
      expect(points[0].attributes).toEqual({ kind: 'error', version: '1.2.3' })
    })
  })
})

describe('web vitals histograms', () => {
  it('is a safe no-op before initTelemetry runs', async () => {
    vi.resetModules()
    const { handleWebVital } = await import('./telemetry')
    expect(() => handleWebVital('LCP', 1200, 'good')).not.toThrow()
    expect(() => handleWebVital('INP', 150, 'needs-improvement')).not.toThrow()
    expect(() => handleWebVital('CLS', 0.08, 'poor')).not.toThrow()
  })

  // jsdom's PerformanceObserverStub reports empty supportedEntryTypes,
  // so web-vitals' feature detection finds nothing to observe. Drives
  // initTelemetry through real (non-injected) metric reader construction too.
  it('initializes without throwing even though jsdom cannot back the web-vitals PerformanceObservers', async () => {
    vi.resetModules()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(okResponse()))
    const { initTelemetry, handleWebVital } = await import('./telemetry')
    await expect(initTelemetry(new SimpleSpanProcessor(new InMemorySpanExporter()))).resolves.toBeUndefined()
    // onLCP/onINP/onCLS never fire under jsdom, but the seam they'd
    // delegate to is live either way.
    expect(() => handleWebVital('LCP', 1200, 'good')).not.toThrow()
    vi.unstubAllGlobals()
    trace.disable()
    context.disable()
    propagation.disable()
  })

  describe('once initialized', () => {
    let metricExporter: InMemoryMetricExporter
    let metricReader: PeriodicExportingMetricReader
    let telemetry: typeof import('./telemetry')

    beforeEach(async () => {
      vi.resetModules()
      metricExporter = new InMemoryMetricExporter(AggregationTemporality.CUMULATIVE)
      metricReader = new PeriodicExportingMetricReader({
        exporter: metricExporter,
        exportIntervalMillis: NEVER_TICK_MILLIS,
      })
      telemetry = await import('./telemetry')
      await telemetry.initTelemetry(new SimpleSpanProcessor(new InMemorySpanExporter()), metricReader)
    })

    afterEach(() => {
      // Same global-registry leak as other blocks, plus
      // vi.spyOn(document,'visibilityState') below shadows a real
      // getter; restoreAllMocks removes it.
      vi.restoreAllMocks()
      trace.disable()
      context.disable()
      propagation.disable()
    })

    it('records LCP into vg.frontend.web_vitals.lcp with its rating and the configured bucket boundaries', async () => {
      telemetry.handleWebVital('LCP', 1200, 'good')
      const points = await findDataPoints<Histogram>(metricReader, metricExporter, 'vg.frontend.web_vitals.lcp')
      expect(points).toHaveLength(1)
      expect(points[0].value.sum).toBe(1200)
      expect(points[0].value.count).toBe(1)
      expect(points[0].attributes).toEqual({ rating: 'good' })
      expect(points[0].value.buckets.boundaries).toEqual([500, 1000, 1500, 2000, 2500, 3000, 4000, 6000, 8000, 12000])
    })

    it('records INP into vg.frontend.web_vitals.inp with its rating and the configured bucket boundaries', async () => {
      telemetry.handleWebVital('INP', 150, 'needs-improvement')
      const points = await findDataPoints<Histogram>(metricReader, metricExporter, 'vg.frontend.web_vitals.inp')
      expect(points).toHaveLength(1)
      expect(points[0].value.sum).toBe(150)
      expect(points[0].value.count).toBe(1)
      expect(points[0].attributes).toEqual({ rating: 'needs-improvement' })
      expect(points[0].value.buckets.boundaries).toEqual([50, 100, 150, 200, 300, 400, 500, 750, 1000, 2000])
    })

    // CLS is unitless, recorded as-is (never scaled); guards against a
    // x1000-style regression.
    it('records CLS into vg.frontend.web_vitals.cls unscaled, with its rating and the configured bucket boundaries', async () => {
      telemetry.handleWebVital('CLS', 0.08, 'poor')
      const points = await findDataPoints<Histogram>(metricReader, metricExporter, 'vg.frontend.web_vitals.cls')
      expect(points).toHaveLength(1)
      expect(points[0].value.sum).toBeCloseTo(0.08)
      expect(points[0].value.count).toBe(1)
      expect(points[0].attributes).toEqual({ rating: 'poor' })
      expect(points[0].value.buckets.boundaries).toEqual([0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.25, 0.5, 1])
    })

    it('accumulates repeated calls for the same vital into one histogram', async () => {
      telemetry.handleWebVital('LCP', 1200, 'good')
      telemetry.handleWebVital('LCP', 1800, 'good')
      const points = await findDataPoints<Histogram>(metricReader, metricExporter, 'vg.frontend.web_vitals.lcp')
      expect(points).toHaveLength(1)
      expect(points[0].value.count).toBe(2)
      expect(points[0].value.sum).toBe(3000)
    })

    it('force-flushes the metric reader when the page becomes hidden', () => {
      const flushSpy = vi.spyOn(metricReader, 'forceFlush')
      vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
      document.dispatchEvent(new Event('visibilitychange'))
      expect(flushSpy).toHaveBeenCalled()
    })

    it('does not force-flush when visibility changes to a non-hidden state', () => {
      const flushSpy = vi.spyOn(metricReader, 'forceFlush')
      vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
      document.dispatchEvent(new Event('visibilitychange'))
      expect(flushSpy).not.toHaveBeenCalled()
    })
  })

  describe('build-version stamping', () => {
    afterEach(() => {
      vi.unstubAllGlobals()
      vi.unstubAllEnvs()
      trace.disable()
      context.disable()
      propagation.disable()
    })

    it('includes version on a web vital histogram data point when set', async () => {
      vi.resetModules()
      vi.stubEnv('VITE_BUILD_VERSION', '1.2.3')
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(okResponse()))
      const metricExporter = new InMemoryMetricExporter(AggregationTemporality.CUMULATIVE)
      const metricReader = new PeriodicExportingMetricReader({
        exporter: metricExporter,
        exportIntervalMillis: NEVER_TICK_MILLIS,
      })
      const telemetry = await import('./telemetry')
      await telemetry.initTelemetry(new SimpleSpanProcessor(new InMemorySpanExporter()), metricReader)
      telemetry.handleWebVital('CLS', 0.02, 'good')
      const points = await findDataPoints<Histogram>(metricReader, metricExporter, 'vg.frontend.web_vitals.cls')
      expect(points[0].attributes).toEqual({ rating: 'good', version: '1.2.3' })
    })
  })
})
