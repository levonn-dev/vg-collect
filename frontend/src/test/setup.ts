import '@testing-library/jest-dom/vitest'

// Recharts' ResponsiveContainer needs a ResizeObserver; jsdom has
// none. Charts render zero-sized here - tests assert our own regions
// and copy, not SVG geometry.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver
