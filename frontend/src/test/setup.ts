import '@testing-library/jest-dom/vitest'
import { configure } from '@testing-library/dom'
import { i18n } from '@lingui/core'
import { messages } from '../locales/en.po'

// findBy*/waitFor default to a 1s ceiling, sized for an idle machine.
// Under coverage instrumentation or a loaded 2-core CI runner the
// suite runs several times slower and legitimate renders blow through
// that second, so the ceiling gets room. Only failing waits pay the
// difference; passing ones return the moment they resolve.
configure({ asyncUtilTimeout: 5000 })

// Recharts' ResponsiveContainer needs a ResizeObserver; jsdom has
// none. The stub reports a fixed size on observe so charts render
// real SVG under test instead of measuring the layoutless DOM at 0x0
// (recharts overwrites its own initialDimension with a mount-time
// getBoundingClientRect, so a sized observation is the only seam).
// The synchronous callback batches with that mount measurement inside
// the same effect, which is what keeps the 0x0 warn path unreached.
class ResizeObserverStub {
  #callback: ResizeObserverCallback
  constructor(callback: ResizeObserverCallback) {
    this.#callback = callback
  }
  observe(target: Element) {
    this.#callback(
      [{ target, contentRect: { width: 600, height: 256 } } as ResizeObserverEntry],
      this,
    )
  }
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver

// jsdom has no matchMedia; the theme code queries and subscribes to
// it. Plain assignment (not vi.stubGlobal) so vi.unstubAllGlobals()
// in test files cannot remove it mid-file.
window.matchMedia ??= ((query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addEventListener: () => {},
  removeEventListener: () => {},
  addListener: () => {},
  removeListener: () => {},
  dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia

// openapi-fetch constructs a Request for every call; browsers resolve
// its relative /api URL against the page origin, but Node's Request
// (undici) rejects relative URLs outright. Resolve them against the
// same placeholder base the fixtures' calledPath uses, so transport
// code runs under jsdom exactly as written. Plain assignment (not
// vi.stubGlobal) so vi.unstubAllGlobals() cannot remove it mid-file.
const NativeRequest = globalThis.Request
class RelativeUrlRequest extends NativeRequest {
  constructor(input: RequestInfo | URL, init?: RequestInit) {
    super(
      typeof input === 'string' && input.startsWith('/')
        ? new URL(input, 'http://test.local')
        : input,
      init,
    )
  }
}
globalThis.Request = RelativeUrlRequest

// The OTel fetch instrumentation watches resource timings; jsdom has
// no PerformanceObserver.
class PerformanceObserverStub {
  observe() {}
  disconnect() {}
  takeRecords() {
    return []
  }
  static supportedEntryTypes: string[] = []
}
globalThis.PerformanceObserver ??=
  PerformanceObserverStub as unknown as typeof PerformanceObserver

// Tests run with the English catalog active on the Lingui singleton,
// so components render the same English copy assertions always saw.
i18n.load('en', messages)
i18n.activate('en')
