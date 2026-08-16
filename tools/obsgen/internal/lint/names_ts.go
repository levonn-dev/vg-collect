package lint

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// This file adds the TypeScript pass Known's doc comment (names.go)
// promises: a scan of repoRoot/frontend/src for the browser telemetry
// registrations the Go AST scan above cannot see (frontend/src is
// TypeScript, not Go). It is deliberately narrow, not a JS/TS parser:
//
//   - Recognized call shape: RECEIVER.create<Kind>(name, opts?) where
//     RECEIVER is a single bare identifier immediately before the dot
//     (e.g. "meter" in "meter.createCounter(...)", the shape every real
//     registration in frontend/src/telemetryImpl.ts uses today). No
//     import-alias or call-expression-receiver resolution is attempted:
//     "getMeter().createCounter(...)" and a bare "createCounter(...)"
//     with no receiver at all both do not match, by design (see
//     tsCallPattern). Chained member access before the receiver (e.g.
//     "this.telemetry.meter.createCounter(...)") still matches, since
//     only the identifier immediately before the dot is required - the
//     scan does not care what, if anything, qualifies it further left.
//   - <Kind> is one of the seven OTel JS API instrument constructors
//     (see tsInstrumentKind); each maps to the same registrationKind
//     the Go scan's own otelMethodKind uses, so expansion (expandNames,
//     unitSuffix) is shared verbatim - no suffixing rule is duplicated
//     here.
//   - name must be the call's first argument and a single-quoted,
//     double-quoted, or backtick string literal with no interpolation.
//     Anything else in that position (an identifier, a concatenation, a
//     function call) is out of scope by construction and contributes
//     nothing, silently - the same treatment a non-literal name gets on
//     the Go side (see Known's doc comment). A backtick literal that
//     does contain interpolation ("${") is a deliberate exception: it
//     reads as a literal but is not one, so recordTSCall reports a scan
//     error instead of silently resolving nothing.
//   - unit, if present, is read from the first "unit: '<literal>'"
//     property found anywhere in the call body (same three quote
//     forms) - see scanTSCallBody. The scan does not track which
//     argument or object it is inside; it takes the first match at any
//     nesting depth between the call's own parentheses. OTel's
//     MetricOptions shape never nests a second, decoy "unit" key in
//     practice (see the nested "advice" object in the multi-line case
//     above, which carries no "unit" key of its own), so in every real
//     registration this is equivalent to reading it from the
//     options-object argument specifically, without the implementation
//     actually special-casing argument position to get there. Unlike
//     the name, an unresolvable unit (missing, or not a plain literal)
//     is simply treated as no unit at all - the same leniency
//     findWithUnit gives a direct OTel SDK call's metric.WithUnit
//     option on the Go side.
//
// Every *.ts and *.tsx file under frontend/src is walked; nothing is
// skipped (unlike scanTree's _test.go exclusion - frontend metrics are
// registered in exactly one production module today, so there is no
// analogous throwaway-registration file to exclude).
//
// The call body (from the call's own opening "(" to its matching
// close) is read with one hand-written scanner tracking a single
// combined nesting depth across ()/{}/[] plus quote state for
// '/"/` - not three independent per-bracket-kind counters, since the
// scan only needs to know when it has returned to the call's own top
// level, and not a real parser's per-kind matching. This is what lets
// a multi-line options object carrying its own nested {}/[] (e.g. an
// "advice: { explicitBucketBoundaries: [...] }" option, a real shape
// in telemetryImpl.ts's web-vitals histograms) resolve correctly
// without ending the call early. Comments inside a call are not
// recognized as comments - a stray quote or bracket character inside
// one could in principle confuse the scanner - but no real registration
// in this repo puts a comment inside a call's own parentheses, and an
// unterminated string or call (malformed source) is handled by simply
// stopping at end-of-source rather than looping or panicking.

// tsInstrumentKind maps the seven OTel JS API meter.create<Kind>
// instrument constructors to the same registrationKind the Go scan's
// otelMethodKind uses for the equivalent OTel SDK methods:
// createCounter/createObservableCounter are monotonic (kindCounter,
// "_total"); createHistogram is kindHistogram
// ("_bucket"/"_count"/"_sum"); createUpDownCounter/
// createObservableUpDownCounter/createObservableGauge and createGauge
// (the JS API's synchronous Gauge instrument - a current-value
// reading, not additive, with no Go-side equivalent method registered
// anywhere in this repo yet) are all non-monotonic (kindGauge, no
// structural suffix beyond the unit).
var tsInstrumentKind = map[string]registrationKind{
	"createCounter":                 kindCounter,
	"createObservableCounter":       kindCounter,
	"createHistogram":               kindHistogram,
	"createUpDownCounter":           kindGauge,
	"createObservableUpDownCounter": kindGauge,
	"createGauge":                   kindGauge,
	"createObservableGauge":         kindGauge,
}

// tsCallPattern matches RECEIVER.create<Kind>( - see this file's doc
// comment above for exactly what RECEIVER may and may not be. Built
// from tsInstrumentKind's own keys (sorted for a deterministic pattern
// string) so the two can never drift apart.
var tsCallPattern = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*\.(` + tsInstrumentAlternation() + `)\s*\(`)

func tsInstrumentAlternation() string {
	return strings.Join(slices.Sorted(maps.Keys(tsInstrumentKind)), "|")
}

// scanTSTree walks every .ts and .tsx file under root, folding each
// file's registrations into known. Unlike scanTree's services/libs/go
// roots (see TestKnown_MissingTree), a root that does not exist at all
// is not an error here: it contributes zero names, the same outcome as
// an existing-but-empty frontend/src. A repoRoot pointing at the wrong
// place entirely is already caught by the services/libs/go scans (a
// real vgkeep checkout always has both alongside frontend/src), so
// treating frontend/src's own absence as fatal would only ever fire
// for a fixture tree with no frontend content to model - this
// package's own testdata/names-valid and testdata/repo among them,
// neither of which carries a frontend/src directory. Any other Stat
// failure (permissions, an actual I/O error) still propagates: only
// "does not exist" gets the lenient treatment. Like scanTree, a file
// that yields a scan error stops the walk immediately (filepath.WalkDir
// aborts on any non-nil callback error) rather than continuing past
// it - Known discards the whole known set on any error regardless, so
// there is no benefit to scanning further once one file has failed.
func scanTSTree(root string, known map[string]struct{}) error {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("checking %s: %w", root, err)
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", path, err)
		}
		if d.IsDir() || !isTSSourceFile(path) {
			return nil
		}
		return scanTSFile(path, known)
	})
}

// isTSSourceFile reports whether path is a TypeScript source file this
// pass scans: any .ts or .tsx file, nothing else excluded.
func isTSSourceFile(path string) bool {
	return strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")
}

// scanTSFile reads one file and folds its registrations into known.
func scanTSFile(path string, known map[string]struct{}) error {
	content, err := os.ReadFile(path) //nolint:gosec // G304: path comes from WalkDir over a fixed repo-relative root, never external input.
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	return scanTSSource(path, string(content), known)
}

// scanTSSource finds every tsCallPattern match in src and records each
// one. Matches are found by one regexp pass over the whole file text -
// not comment- or string-aware at this outer level (see this file's
// doc comment) - then each candidate call's own body is read by a
// bounded hand scan (recordTSCall) rather than by continuing the
// regexp forward, since only that scan can correctly find where a call
// carrying nested {}/[] actually ends.
func scanTSSource(path, src string, known map[string]struct{}) error {
	var errs []error
	for _, loc := range tsCallPattern.FindAllStringSubmatchIndex(src, -1) {
		// loc[0]:loc[1] is the whole match, ending in the call's own
		// literal "("; loc[2]:loc[3] is the create<Kind> capture group.
		kind := tsInstrumentKind[src[loc[2]:loc[3]]]
		openParen := loc[1] - 1
		recordTSCall(known, &errs, path, src, openParen, kind)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// recordTSCall extracts one meter.create<Kind>(...) call's name and
// optional unit, starting from openParen (the index of the call's own
// "(" in src), and folds every Prometheus name they expand to into
// known via expandNames - the same expansion every Go-side call shape
// already goes through. See this file's doc comment for the three
// outcomes a first argument can have (literal, silently out of scope,
// or an interpolated-template scan error).
func recordTSCall(known map[string]struct{}, errs *[]error, path, src string, openParen int, kind registrationKind) {
	name, literal, interpolated := readTSFirstArg(src, openParen+1)
	if interpolated {
		*errs = append(*errs, fmt.Errorf("%s: metric name %q is a template literal with interpolation, cannot resolve statically", path, name))
		return
	}
	if !literal {
		return
	}

	unit, _ := scanTSCallBody(src, openParen)
	names, err := expandNames(name, unit, kind)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: metric %q: %w", path, name, err))
		return
	}
	for _, n := range names {
		known[n] = struct{}{}
	}
}

// readTSFirstArg reports whether the call's first argument (starting
// at argStart, the index right after the call's own opening "(") is a
// string literal: value holds the literal's raw text (unescaped - see
// readTSQuotedLiteral) whenever literal or interpolated is true, empty
// otherwise. Only whitespace may precede the literal; anything else
// (an identifier, a number, another expression) means literal and
// interpolated are both false and value is empty - not a literal at
// all, out of scope by construction. The literal must also be the
// argument in full: only whitespace may follow it before the call's
// own "," (another argument follows) or ")" (the call has one
// argument) - anything else, most commonly a "+" starting a
// concatenation with more source to its right, means the literal is
// only part of a larger expression, not a literal argument by itself,
// so literal is false in that case too (interpolated is unaffected by
// this trailing check - an interpolated backtick literal is already
// resolved, and reported, before reaching it). This is the same
// out-of-scope-by-construction treatment a concatenation gets on the
// Go side, where a *ast.BinaryExpr simply fails stringLit's own
// *ast.BasicLit type assertion (see names.go).
func readTSFirstArg(src string, argStart int) (value string, literal, interpolated bool) {
	i := skipTSWhitespace(src, argStart)
	if i >= len(src) {
		return "", false, false
	}
	c := src[i]
	if c != '\'' && c != '"' && c != '`' {
		return "", false, false
	}
	val, interp, end, ok := readTSQuotedLiteral(src, i)
	if !ok {
		return "", false, false
	}
	if interp {
		return val, false, true
	}
	j := skipTSWhitespace(src, end)
	if j < len(src) && (src[j] == ',' || src[j] == ')') {
		return val, true, false
	}
	return "", false, false
}

func skipTSWhitespace(src string, pos int) int {
	for pos < len(src) {
		switch src[pos] {
		case ' ', '\t', '\n', '\r':
			pos++
		default:
			return pos
		}
	}
	return pos
}

// readTSQuotedLiteral reads one quoted literal starting at src[pos],
// which must be the opening quote character (single, double, or
// backtick); ok is false otherwise. value is the raw text between the
// quotes - backslash
// escapes are recognized only well enough to find the correct
// terminating quote (the escaped character is never treated as a
// terminator), not decoded into the returned value, since a real
// metric name or unit never carries one. interpolated reports whether
// a backtick literal contains an unescaped "${" before its closing
// backtick; single- and double-quoted literals can never be
// interpolated (JS/TS gives that meaning to backticks only) and always
// report false. end is the index just past the closing quote, or
// len(src) if the literal runs off the end of the source unterminated
// (malformed input - treated as "not found", not a crash).
func readTSQuotedLiteral(src string, pos int) (value string, interpolated bool, end int, ok bool) {
	if pos >= len(src) {
		return "", false, pos, false
	}
	quote := src[pos]
	if quote != '\'' && quote != '"' && quote != '`' {
		return "", false, pos, false
	}
	contentStart := pos + 1
	i := contentStart
	for i < len(src) {
		switch src[i] {
		case '\\':
			i += 2
		case quote:
			return src[contentStart:i], interpolated, i + 1, true
		default:
			if quote == '`' && src[i] == '$' && i+1 < len(src) && src[i+1] == '{' {
				interpolated = true
			}
			i++
		}
	}
	return "", false, len(src), false
}

// scanTSCallBody walks src from openParen+1 to the matching close of
// the call's own opening "(" at openParen, tracking one combined
// nesting depth across ()/{}/[] (starts at 1 for the call's own paren)
// and treating the contents of any '/"/` string as opaque, brackets
// included - see this file's doc comment for why a single combined
// counter is enough here. Along the way it looks for the first
// "unit: '<literal>'" property and returns its value, or "" if none is
// found (the same "no suffix" treatment an absent metric.WithUnit
// option gets on the Go side - see findWithUnit). end is the index
// just past the call's closing ")", or len(src) if the call runs off
// the end of the source unterminated.
func scanTSCallBody(src string, openParen int) (unit string, end int) {
	depth := 1
	i := openParen + 1
	for i < len(src) && depth > 0 {
		switch src[i] {
		case '\'', '"', '`':
			_, _, next, ok := readTSQuotedLiteral(src, i)
			if !ok {
				return unit, len(src)
			}
			i = next
		case '(', '{', '[':
			depth++
			i++
		case ')', '}', ']':
			depth--
			i++
		default:
			if unit == "" {
				if val, next, ok := matchTSUnitLiteral(src, i); ok {
					unit = val
					i = next
					continue
				}
			}
			i++
		}
	}
	return unit, i
}

// matchTSUnitLiteral reports whether src[i:] begins a bare "unit"
// object-literal key - word-bounded, so "units" or "myUnit" do not
// match - followed by ":" and a quoted, non-interpolated value. On a
// match it returns that value (raw, unescaped - see
// readTSQuotedLiteral) and the index just past the value's closing
// quote.
func matchTSUnitLiteral(src string, i int) (value string, end int, ok bool) {
	const key = "unit"
	if i+len(key) > len(src) || src[i:i+len(key)] != key {
		return "", 0, false
	}
	if i > 0 && isTSIdentChar(src[i-1]) {
		return "", 0, false
	}
	j := i + len(key)
	if j < len(src) && isTSIdentChar(src[j]) {
		return "", 0, false
	}
	j = skipTSWhitespace(src, j)
	if j >= len(src) || src[j] != ':' {
		return "", 0, false
	}
	j = skipTSWhitespace(src, j+1)
	val, interp, next, ok := readTSQuotedLiteral(src, j)
	if !ok || interp {
		return "", 0, false
	}
	return val, next, true
}

func isTSIdentChar(b byte) bool {
	return b == '_' || b == '$' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
