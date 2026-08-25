import '@testing-library/jest-dom/vitest'
import { configure } from '@testing-library/dom'
import { i18n } from '@lingui/core'
import { messages } from '../locales/en.po'

// Default 1s ceiling is sized for an idle machine; coverage/CI runs
// several times slower and blows through it. Only failing waits pay
// the difference.
configure({ asyncUtilTimeout: 5000 })

// jsdom has no ResizeObserver; Recharts needs one for ResponsiveContainer.
// Stub reports a fixed size on observe so charts render real SVG
// instead of measuring the layoutless DOM at 0x0 (a sized observation
// is the only seam, since recharts overwrites initialDimension at mount).
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

// jsdom has no matchMedia; theme code queries/subscribes to it. Plain
// assignment, not vi.stubGlobal, so unstubAllGlobals() can't remove it mid-file.
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

// Node's Request (undici) rejects relative /api URLs browsers resolve
// against the page origin; resolves against the same placeholder base
// fixtures' calledPath uses. Plain assignment, not vi.stubGlobal.
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

// OTel fetch instrumentation watches resource timings; jsdom has no PerformanceObserver.
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

// Tests run with the English catalog active, so components render the
// copy assertions always saw.
i18n.load('en', messages)
i18n.activate('en')
