// Proves names.Known's TypeScript pass also walks .tsx files, not just
// .ts, and covers one more instrument kind (UpDownCounter - not
// monotonic, so no structural suffix, same treatment as
// ObservableGauge). Never compiles as part of any real package or the
// frontend build; see telemetry.ts in this same directory for the rest
// of this fixture's cases and its doc comment.

interface FakeMeter {
  createUpDownCounter(name: string, opts?: object): unknown
}

declare const meter: FakeMeter

export function Widget() {
  const active = meter.createUpDownCounter('vg.widget.active_sessions', {
    description: 'Currently active widget sessions',
  })
  return active
}
