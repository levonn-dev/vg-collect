import { context, propagation, trace } from '@opentelemetry/api'
import { InMemorySpanExporter, SimpleSpanProcessor } from '@opentelemetry/sdk-trace-web'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const okResponse = () => new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })

function headersOf(call: unknown[]): Headers {
  const [input, init] = call as [RequestInfo, RequestInit | undefined]
  if (input instanceof Request) return input.headers
  return new Headers(init?.headers)
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
    initTelemetry(new SimpleSpanProcessor(exporter))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    // @opentelemetry/api keeps its registered tracer/propagator on the
    // real globalThis (Symbol.for-keyed), so it outlives vi.resetModules().
    // Without disabling here, only the first test's provider.register()
    // ever wins and later tests silently export through its processor.
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
    initTelemetry(new SimpleSpanProcessor(exporter))
    await fetch('/api/tags')
    await vi.waitFor(() => expect(exporter.getFinishedSpans().length).toBe(1))
  })
})
