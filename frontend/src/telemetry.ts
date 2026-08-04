import type { Counter, Histogram } from '@opentelemetry/api'
import type { MetricReader } from '@opentelemetry/sdk-metrics'
import type { SpanProcessor } from '@opentelemetry/sdk-trace-web'

// The light half of the telemetry split: this facade holds the
// record* API that pages and lib code import statically, with only
// type-level OTel imports, so the SDK never rides the entry chunk.
// The SDK lives in telemetryImpl.ts, loaded lazily by initTelemetry
// below; the facade holds the instruments it hands back.

let started = false

// The locale/prose counters, created inside telemetryImpl.start and
// assigned here so the record* exports below can add() to them. Each
// stays undefined until init completes, which is what makes every
// record* call below safe beforehand - module load, or a test that
// never calls initTelemetry - instead of a null-deref crash.
let localeBootCounter: Counter | undefined
let catalogFailureCounter: Counter | undefined
let localeSwitchCounter: Counter | undefined
let proseFallbackCounter: Counter | undefined
let errorsCounter: Counter | undefined
let apiFailuresCounter: Counter | undefined

// The web-vitals histograms, same shape as the counters above - held
// here so handleWebVital below can record() into them once init has
// completed.
let lcpHistogram: Histogram | undefined
let inpHistogram: Histogram | undefined
let clsHistogram: Histogram | undefined

// Events recorded between initTelemetry being called and the impl
// chunk landing would otherwise vanish - and the locale boot count
// (the dashboard's boots denominator) fires during first render,
// squarely inside that window. Each record* below queues itself here
// while the chunk is in flight and initTelemetry replays the queue
// once the instruments exist. Bounded so a broken chunk fetch can
// never grow it past a screenful; overflow drops silently, matching
// the pre-existing accepted-loss posture for telemetry.
let pending: Array<() => void> | undefined = []
const PENDING_CAP = 100

function enqueue(replay: () => void): boolean {
  if (!pending) return false
  if (pending.length < PENDING_CAP) pending.push(replay)
  return true
}

// Set inside initTelemetry from VITE_BUILD_VERSION (baked in at image
// build, same convention as the VITE_SITE_* identity slots). Empty
// string normalizes to undefined here so every reader below can use a
// single truthy check instead of also excluding ''.
let buildVersion: string | undefined

// initTelemetry pulls in the SDK chunk and starts it (fetch tracing,
// metric instruments, web-vitals, error listeners - see
// telemetryImpl.start). Idempotent; the optional processor/metricReader
// are test seams (production callers pass nothing and export both
// signals via OTLP). Deliberately async: main.tsx fires it without
// awaiting so first render never waits on the telemetry chunk, and the
// queue above bridges the gap. Uncaught errors thrown before the impl's
// window listeners attach are lost; that ms-scale window is accepted
// the same way the web-vitals tail flush loss is.
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
  if (!localeBootCounter && enqueue(() => recordLocaleBoot(locale, source, browserLanguage))) return
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
  if (!catalogFailureCounter && enqueue(() => recordCatalogFailure(stage, locale))) return
  catalogFailureCounter?.add(1, { stage, locale })
}

// recordLocaleSwitch fires once per setLocale call that reaches a
// supported locale, before activation - "to" may equal "from" if a
// user reselects their current language.
export function recordLocaleSwitch(from: string, to: string): void {
  if (!localeSwitchCounter && enqueue(() => recordLocaleSwitch(from, to))) return
  localeSwitchCounter?.add(1, { from, to })
}

// recordProseFallback fires when ProsePage serves the English variant
// to a non-en locale because no translation was contributed for it.
export function recordProseFallback(page: string): void {
  if (!proseFallbackCounter && enqueue(() => recordProseFallback(page))) return
  proseFallbackCounter?.add(1, { page })
}

// recordUncaughtError fires from the window 'error'/'unhandledrejection'
// listeners telemetryImpl registers, or directly from
// ErrorBoundary.componentDidCatch for a render crash it caught
// ('boundary' - never reaches those window listeners, so this is not
// a double count; see the why-comment on ErrorBoundary itself). version
// rides along when VITE_BUILD_VERSION was set at build time, via a
// conditional spread so the attribute is absent (not an empty string)
// when it wasn't.
export function recordUncaughtError(kind: 'error' | 'unhandledrejection' | 'boundary'): void {
  if (!errorsCounter && enqueue(() => recordUncaughtError(kind))) return
  errorsCounter?.add(1, { kind, ...(buildVersion ? { version: buildVersion } : {}) })
}

// recordApiNetworkFailure fires from the fetch instrumentation's
// applyCustomAttributesOnSpan callback, wired in telemetryImpl, on a
// rejected fetch() promise - DNS, connection refused, CORS. A
// completed request that merely carries an HTTP error status never
// reaches this: it ends the span through the ordinary response path.
export function recordApiNetworkFailure(): void {
  if (!apiFailuresCounter && enqueue(() => recordApiNetworkFailure())) return
  apiFailuresCounter?.add(1)
}

// handleWebVital is the single seam the onLCP/onINP/onCLS callbacks
// wired in telemetryImpl delegate to - also exported so tests can
// drive it directly with synthetic values, since jsdom cannot produce
// the real paint/input events web-vitals needs to report on its own.
// Safe before initTelemetry completes, like every record* export in
// this file. version rides along under the same conditional spread as
// recordUncaughtError above.
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
