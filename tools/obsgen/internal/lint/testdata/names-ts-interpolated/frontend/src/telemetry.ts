// Isolated fixture: a single registration whose name is a template
// literal containing interpolation, proving names.Known's TypeScript
// pass fails loud (a scan error) rather than silently resolving
// nothing - see recordTSCall's doc comment for why this case differs
// from an ordinary non-literal name. Kept in its own tree, separate
// from ../names-ts-valid, for the same reason names-bad-unit is
// separate from names-valid: Known discards its whole return value on
// any scan error, so a clean-path assertion and an error-path
// assertion can never share one fixture tree.

interface FakeMeter {
  createCounter(name: string, opts?: object): unknown
}

declare const meter: FakeMeter
declare const kind: string

export function register() {
  return meter.createCounter(`vg.widget.${kind}`, {
    description: 'Dynamic name - must error, not silently resolve',
  })
}
