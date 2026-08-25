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
// promises: RECEIVER.create<Kind>(name, opts?), where RECEIVER is a
// single bare identifier immediately before the dot (no alias/receiver
// resolution); <Kind> is one of seven OTel JS API instrument
// constructors (see tsInstrumentKind), mapped to the same
// registrationKind the Go scan uses, so expansion is shared verbatim.
//
// name must be the first argument, a single/double/backtick string
// literal with no interpolation; anything else contributes nothing
// silently, except a backtick literal WITH interpolation ("${"), which
// is a scan error rather than a silent skip. unit, if present, is read
// from the first "unit: '<literal>'" property anywhere in the call
// body; an unresolvable unit is treated as no unit.
//
// Every *.ts/*.tsx under frontend/src is walked; nothing is excluded
// (no _test.go-style skip - there's no throwaway registration file).
//
// The call body is read by one hand-written scanner tracking a single
// combined nesting depth across ()/{}/[] plus quote state, so a
// multi-line options object with its own nested {}/[] resolves
// correctly without ending the call early.

// tsInstrumentKind maps the seven OTel JS API instrument constructors
// to the same registrationKind otelMethodKind uses: Counter variants ->
// kindCounter, Histogram -> kindHistogram, everything else (non-monotonic) -> kindGauge.
var tsInstrumentKind = map[string]registrationKind{
	"createCounter":                 kindCounter,
	"createObservableCounter":       kindCounter,
	"createHistogram":               kindHistogram,
	"createUpDownCounter":           kindGauge,
	"createObservableUpDownCounter": kindGauge,
	"createGauge":                   kindGauge,
	"createObservableGauge":         kindGauge,
}

// tsCallPattern matches RECEIVER.create<Kind>(, built from
// tsInstrumentKind's own keys (sorted) so the two can never drift apart.
var tsCallPattern = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*\.(` + tsInstrumentAlternation() + `)\s*\(`)

func tsInstrumentAlternation() string {
	return strings.Join(slices.Sorted(maps.Keys(tsInstrumentKind)), "|")
}

// scanTSTree walks every .ts/.tsx file under root. Unlike scanTree's
// services/libs/go roots, a missing root is not an error (contributes
// zero names); other Stat failures still propagate. A file's scan error
// stops the walk immediately.
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
	content, err := os.ReadFile(path) //nolint:gosec // G304: path comes from WalkDir over a fixed repo-relative root, not external input.
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	return scanTSSource(path, string(content), known)
}

// scanTSSource finds every tsCallPattern match in src; each candidate
// call's body is then read by a hand scan (recordTSCall), the only way
// to find where nested {}/[] ends the call.
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

// recordTSCall extracts one call's name and optional unit, folding
// every expanded name into known. The first argument has three
// outcomes: literal, silently out of scope, or an interpolated-template scan error.
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

// readTSFirstArg reports whether the argument at argStart is a string
// literal in full: only whitespace may precede it, and only whitespace
// may follow before "," or ")" - anything else (e.g. a "+"
// concatenation) means it's part of a larger expression, not a literal
// argument, so literal is false.
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

// readTSQuotedLiteral reads one quoted literal at src[pos] (the
// opening quote). Backslash escapes are recognized only to find the
// terminating quote, never decoded. interpolated reports an unescaped
// "${" before a backtick's close (single/double quotes are never
// interpolated). Unterminated input is treated as "not found", not a crash.
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

// scanTSCallBody walks from openParen+1 to the matching close, tracking
// one combined nesting depth across ()/{}/[] and treating string
// contents as opaque. Returns the first "unit: '<literal>'" property found, or "".
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

// matchTSUnitLiteral reports whether src[i:] begins a word-bounded
// "unit" key ("units"/"myUnit" don't match) followed by ":" and a
// quoted, non-interpolated value.
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
