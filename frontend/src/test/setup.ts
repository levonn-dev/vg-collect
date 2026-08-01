import '@testing-library/jest-dom/vitest'
import { i18n } from '@lingui/core'
import { messages } from '../locales/en.po'

// Recharts' ResponsiveContainer needs a ResizeObserver; jsdom has
// none. Charts render zero-sized here - tests assert our own regions
// and copy, not SVG geometry.
class ResizeObserverStub {
  observe() {}
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
