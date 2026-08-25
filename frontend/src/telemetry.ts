import type { Counter, Histogram } from '@opentelemetry/api'
import type { MetricReader } from '@opentelemetry/sdk-metrics'
import type { SpanProcessor } from '@opentelemetry/sdk-trace-web'

// Light half of the telemetry split: record* API with only type-level
// OTel imports, so the SDK never rides the entry chunk. SDK lives in
// telemetryImpl.ts, loaded lazily below.

let started = false

// Created inside telemetryImpl.start, assigned here; undefined until
// init completes is what makes record* calls safe beforehand.
let localeBootCounter: Counter | undefined
let catalogFailureCounter: Counter | undefined
let localeSwitchCounter: Counter | undefined
let proseFallbackCounter: Counter | undefined
let errorsCounter: Counter | undefined
let apiFailuresCounter: Counter | undefined

// Web-vitals histograms, same shape as the counters above;
// handleWebVital records into them post-init.
let lcpHistogram: Histogram | undefined
let inpHistogram: Histogram | undefined
let clsHistogram: Histogram | undefined

// Locale boot count fires during first render, inside the window
// before the impl chunk lands, so record* calls queue here and
// initTelemetry replays them; overflow past the cap drops silently.
let pending: Array<() => void> | undefined = []
const PENDING_CAP = 100

function enqueue(replay: () => void): boolean {
  if (!pending) return false
  if (pending.length < PENDING_CAP) pending.push(replay)
  return true
}

// Set from VITE_BUILD_VERSION (baked in at image build); empty string
// normalizes to undefined for a single truthy check.
let buildVersion: string | undefined

// Pulls in the SDK chunk and starts it. Idempotent; optional
// processor/metricReader are test seams (production exports via OTLP).
// Deliberately async and unawaited from main.tsx, so first render never
// waits; errors thrown before the impl's listeners attach are lost.
export async function initTelemetry(processor?: SpanProcessor, metricReader?: MetricReader): Promise<void> {
  if (started) return
  started = true
  buildVersion = import.meta.env.VITE_BUILD_VERSION || undefined
  const { start } = await import('./telemetryImpl')
  const instruments = start(processor, metricReader, buildVersion)
  localeBootCounter = instruments.localeBoot
  catalogFailureCounter = instruments.catalogFailure
  localeSwitchCounter = instruments.localeSwitch
  proseFallbackCounter = instruments.proseFallback
  errorsCounter = instruments.errors
  apiFailuresCounter = instruments.apiFailures
  lcpHistogram = instruments.lcp
  inpHistogram = instruments.inp
  clsHistogram = instruments.cls
  const queued = pending
  pending = undefined
  queued?.forEach((replay) => replay())
}

// Fires once per successful activateBoot: active locale, resolution
// rung, and browser language. Truncated to primary subtag (en-GB -> en)
// to bound cardinality.
export function recordLocaleBoot(
  locale: string,
  source: 'stored' | 'browser' | 'fallback',
  browserLanguage: string | undefined,
): void {
  if (!localeBootCounter && enqueue(() => recordLocaleBoot(locale, source, browserLanguage))) return
  localeBootCounter?.add(1, {
    locale,
    source,
    browser_language: browserLanguage?.split('-')[0],
  })
}

// Fires when a non-en catalog chunk fails: boot (falls back to en) or
// switch (keeps prior locale).
export function recordCatalogFailure(stage: 'boot' | 'switch', locale: string): void {
  if (!catalogFailureCounter && enqueue(() => recordCatalogFailure(stage, locale))) return
  catalogFailureCounter?.add(1, { stage, locale })
}

// Fires once per setLocale call reaching a supported locale, before
// activation; "to" may equal "from" on a reselect.
export function recordLocaleSwitch(from: string, to: string): void {
  if (!localeSwitchCounter && enqueue(() => recordLocaleSwitch(from, to))) return
  localeSwitchCounter?.add(1, { from, to })
}

// Fires when ProsePage serves the English variant to a non-en locale
// (no translation contributed).
export function recordProseFallback(page: string): void {
  if (!proseFallbackCounter && enqueue(() => recordProseFallback(page))) return
  proseFallbackCounter?.add(1, { page })
}

// Fires from window error listeners, or ErrorBoundary.componentDidCatch
// ('boundary', never double-counted with those listeners). version
// rides along via conditional spread when set.
export function recordUncaughtError(kind: 'error' | 'unhandledrejection' | 'boundary'): void {
  if (!errorsCounter && enqueue(() => recordUncaughtError(kind))) return
  errorsCounter?.add(1, { kind, ...(buildVersion ? { version: buildVersion } : {}) })
}

// Fires on a rejected fetch() promise (DNS, connection refused, CORS);
// a completed request with an HTTP error status never reaches this.
export function recordApiNetworkFailure(): void {
  if (!apiFailuresCounter && enqueue(() => recordApiNetworkFailure())) return
  apiFailuresCounter?.add(1)
}

// Seam onLCP/onINP/onCLS delegate to; also exported so tests can drive
// synthetic values (jsdom can't produce real paint/input events).
export function handleWebVital(name: 'LCP' | 'INP' | 'CLS', value: number, rating: string): void {
  if (!lcpHistogram && enqueue(() => handleWebVital(name, value, rating))) return
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
