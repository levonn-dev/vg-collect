// Fixture source for names.Known's TypeScript pass: mirrors the real
// frontend/src/telemetryImpl.ts registration shapes closely enough to
// exercise the scanner end to end. Never compiles as part of any real
// package or the frontend build - this tree sits outside frontend/
// entirely and is read as raw text, the same "testdata is never built"
// precedent the Go-side fixtures (see ../../names-valid) already rely
// on, so the receiver-less bare() call below and the untyped `meter`
// need not resolve to anything real.

interface FakeMeter {
  createCounter(name: string, opts?: object): unknown
  createHistogram(name: string, opts?: object): unknown
  createObservableGauge(name: string, opts?: object): unknown
}

declare const meter: FakeMeter

export function register() {
  // Multi-line options object; unit sits beside a nested advice object
  // carrying its own {}/[] - proves the bracket-depth tracker survives
  // nested brackets without ending the call early or misreading a
  // decoy "unit"-shaped token inside the nested object.
  const latency = meter.createHistogram(
    'vg.widget.latency',
    {
      description: 'Widget latency',
      unit: 'ms',
      advice: { explicitBucketBoundaries: [1, 2, 3] },
    },
  )

  // Unitless counter: no unit property in the options object at all.
  const boot = meter.createCounter('vg.widget.boot', {
    description: 'Widget boots',
  })

  // Backtick literal name with no interpolation - a plain string value
  // like the single/double-quoted forms, not the dynamic case below.
  const errors = meter.createCounter(`vg.widget.errors`, {
    description: 'Widget errors, backtick literal name',
  })

  // No receiver identifier before ".createCounter(" - names.Known's
  // TypeScript pass requires one (see recordTSCall's doc comment) and
  // does not attempt import-alias or call-expression-receiver
  // resolution, so this must not match at all.
  function bare() {
    return createCounter('vg.widget.bare', { description: 'must not match' })
  }

  // A receiver that does match, but the first argument is not any kind
  // of string literal - an identifier here, out of scope by
  // construction the same way a bare-string concatenation or a
  // function-call result would be. Unlike bare() above (which never
  // even reaches recordTSCall), this proves recordTSCall's own
  // non-literal branch specifically.
  const dynamicName = 'vg.widget.dynamic'
  const dynamic = meter.createCounter(dynamicName, {
    description: 'Non-literal name via a receiver that does match - must not resolve',
  })

  // ObservableGauge: not monotonic, so no structural suffix at all
  // beyond the (absent, here) unit suffix.
  const poolConnections = meter.createObservableGauge('vg.widget.pool.connections', {
    description: 'Open pool connections',
  })

  // A concatenated first arg: the leading literal is only the left
  // operand of a "+", not the call's whole first argument - out of
  // scope by construction, the same treatment this file's own doc
  // comment already promises for "a concatenation" and the Go side
  // gives a *ast.BinaryExpr via stringLit's own *ast.BasicLit type
  // assertion failing (see names.go). Must not resolve to the phantom
  // name "vg_widget__total" a scanner that stopped reading at the
  // literal's own closing quote, without checking what follows it,
  // would otherwise record. Quote form.
  const kind = 'k'
  const concatQuote = meter.createCounter('vg.widget.' + kind, {
    description: 'Concatenated name (quote form) - must not resolve',
  })

  // Same, backtick form: a plain (non-interpolated) template literal -
  // no "${" anywhere in it, so this is not the interpolated-template
  // scan-error case - still just the left operand of a "+", so it
  // gets the identical non-literal, silent-skip treatment as the quote
  // form above.
  const concatBacktick = meter.createCounter(`vg.widget.` + kind, {
    description: 'Concatenated name (backtick form) - must not resolve',
  })

  return { latency, boot, errors, bare, dynamic, poolConnections, concatQuote, concatBacktick }
}
