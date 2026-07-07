import { W3CTraceContextPropagator } from '@opentelemetry/core'
import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-http'
import { registerInstrumentations } from '@opentelemetry/instrumentation'
import { FetchInstrumentation } from '@opentelemetry/instrumentation-fetch'
import { resourceFromAttributes } from '@opentelemetry/resources'
import { BatchSpanProcessor, WebTracerProvider } from '@opentelemetry/sdk-trace-web'
import type { SpanProcessor } from '@opentelemetry/sdk-trace-web'
import { ATTR_SERVICE_NAME, ATTR_SERVICE_VERSION } from '@opentelemetry/semantic-conventions'

// Browser telemetry leaves through the bff's same-origin OTLP relay;
// the CSP (connect-src 'self') forbids any external collector host.
const OTLP_PATH = '/api/otlp/v1/traces'

let started = false

// initTelemetry turns on fetch tracing: every API call gets a span and
// a traceparent header, so a browser interaction and the server work
// it caused share one trace. Idempotent; the optional processor is a
// test seam (production callers pass nothing and export via OTLP).
export function initTelemetry(processor?: SpanProcessor): void {
  if (started) return
  started = true
  const provider = new WebTracerProvider({
    resource: resourceFromAttributes({
      [ATTR_SERVICE_NAME]: 'frontend',
      [ATTR_SERVICE_VERSION]: 'dev',
    }),
    spanProcessors: [processor ?? new BatchSpanProcessor(new OTLPTraceExporter({ url: OTLP_PATH }))],
  })
  provider.register({ propagator: new W3CTraceContextPropagator() })
  registerInstrumentations({
    instrumentations: [
      new FetchInstrumentation({
        // Never trace the relay itself: spans about span uploads would
        // feed the exporter forever.
        ignoreUrls: [/\/api\/otlp\//],
      }),
    ],
  })
}
