package lint

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// vgotelImportPath and metricImportPath are the two import paths Known
// resolves a per-file local alias against - vgotelImportPath for the
// vgotel.Counter/Histogram wrapper call shape, metricImportPath for
// the metric.WithUnit(...) option a direct OTel SDK registration
// carries. Every real caller today leaves metricImportPath unaliased
// ("metric") but aliases vgotelImportPath ("vgotel" by convention -
// libs/go/otel's own package name is "otel", and every caller already
// imports the upstream SDK's "otel" package too, forcing an alias);
// Known does not hardcode either spelling, only the import paths.
const (
	vgotelImportPath = "github.com/levonn-dev/vgkeep/libs/go/otel"
	metricImportPath = "go.opentelemetry.io/otel/metric"
)

// registration argument positions. vgotel.Counter/Histogram's own
// signature is (meter, name, description, unit, ...): name at index 1,
// unit at index 3. A pass-through closure - the shape social,
// collection and bff each define once per meter and call by literal
// name at every registration site - drops the meter parameter (it is
// captured from the enclosing scope, not passed in), so the same two
// arguments land two positions earlier at the closure's own call
// sites.
// The Logged variants (CounterLogged/HistogramLogged) insert a logger
// parameter after the meter, shifting name and unit one position right.
const (
	directNameIndex  = 1
	directUnitIndex  = 3
	loggedNameIndex  = 2
	loggedUnitIndex  = 4
	closureNameIndex = 0
	closureUnitIndex = 2
)

// registrationKind distinguishes a Counter registration (contributes
// one "_total"-suffixed name) from a Histogram one (contributes three:
// "_bucket", "_count", "_sum") from a Gauge one - an UpDownCounter,
// ObservableUpDownCounter, or ObservableGauge, none of them monotonic,
// so the Prometheus exporter adds no structural suffix at all beyond
// the unit suffix (contributes exactly the base name; see
// libs/go/pgkit's and libs/go/valkeykit's own pool-connection-count
// gauges, already queried unsuffixed in docs/runbooks/stack.md's own
// "Pool gauges emit without traffic" verification section).
type registrationKind int

const (
	kindCounter registrationKind = iota
	kindHistogram
	kindGauge
)

// Known scans every .go file under repoRoot/services and
// repoRoot/libs/go, plus every .ts and .tsx file under
// repoRoot/frontend/src (the browser telemetry - see names_ts.go), for
// metric registrations and returns every Prometheus-form name they
// expand to (see expandNames). It recognizes three Go call shapes, all
// real in this repo: a direct vgotel call (vgotel.Counter(meter,
// "name", "desc", "unit"), user/auth/enrichment's shape - grep for
// vgotel.Counter in services/ to see it), a same-order pass-through
// closure (counter := func(name, desc, unit string) T { c, _ :=
// vgotel.Counter(meter, name, desc, unit); return c }, called as
// counter("name", "desc", "unit") - social/collection/bff's shape),
// and a call directly on the OTel SDK's own metric.Meter - one of the
// twelve Xxx64[Observable]Kind instrument-creation methods (see
// otelMethodKind) - with the metric name as the call's own first
// argument and the unit, if any, in a metric.WithUnit(...) option
// among the rest (libs/go/pgkit's and libs/go/valkeykit's own
// pool-connection gauges/counters, services/collection's
// pending-submissions gauge, services/auth's signing-keys gauge and
// its oidc package's provider-latency histogram - grep services/ and
// libs/go/ for ObservableGauge to see the shape), plus one TypeScript
// call shape (RECEIVER.create<Kind>("name", { unit: "..." }) -
// frontend/src/telemetryImpl.ts's shape, see names_ts.go for its own
// scope notes). Known does not attempt general data-flow analysis
// beyond the one Go closure pattern above: a registration whose name
// is not a literal at one of these four call shapes contributes
// nothing (no error - the manifests can only ever reference a name an
// author could grep for, so a dynamically-built name is out of scope
// by construction, the same discipline every real registration in the
// repo already follows) - except a TypeScript template literal
// containing interpolation, which names_ts.go's doc comment explains
// is a scan error rather than a silent skip.
func Known(repoRoot string) (map[string]struct{}, error) {
	known := make(map[string]struct{})

	roots := []string{
		filepath.Join(repoRoot, "services"),
		filepath.Join(repoRoot, "libs", "go"),
	}
	for _, root := range roots {
		if err := scanTree(root, known); err != nil {
			return nil, err
		}
	}
	if err := scanTSTree(filepath.Join(repoRoot, "frontend", "src"), known); err != nil {
		return nil, err
	}
	return known, nil
}

// scanTree walks every non-test .go file under root, folding each
// file's registrations into known. _test.go files are skipped
// deliberately, not just for speed: libs/go/otel/emit_test.go, a real
// file in this repo, registers throwaway counters/histograms (e.g.
// name "vg.test.count", unit "u") purely to exercise the vgotel
// wrapper itself. Those are not application metrics any manifest would
// ever reference, and "u" is not one of the exporter's recognized unit
// forms, so scanning test files would make Known fail on every run
// instead of building a clean known-metric set.
func scanTree(root string, known map[string]struct{}) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", path, err)
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return scanFile(path, known)
	})
}

// scanFile parses one Go source file and folds its registrations into
// known. Parsing is syntax-only (go/parser, no go/types): Known never
// resolves what an import path's identifiers actually mean or what
// type a call's receiver has, only recognizes the three call shapes
// textually (a direct-OTel-SDK call is matched by method name alone -
// see otelMethodKind - which is why this function never needs to know
// a call's receiver expression at all, unlike the vgotel-specific
// shapes below), so a fixture file whose imports do not resolve to a
// real module (this package's own testdata) still scans correctly.
func scanFile(path string, known map[string]struct{}) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	vgAlias := resolveImportAlias(file, vgotelImportPath)
	metricAlias := resolveImportAlias(file, metricImportPath)
	// passThroughClosures(file, "") - an empty vgAlias, the common case
	// for a file that does not import libs/go/otel at all - is safe and
	// cheap: directKind never matches a "" alias, so it simply finds no
	// closures, the same outcome as skipping the call outright.
	closures := passThroughClosures(file, vgAlias)

	var errs []error
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if kind, ok := directKind(call, vgAlias); ok {
				recordCall(known, &errs, path, call.Args, directNameIndex, directUnitIndex, kind)
			} else if kind, ok := loggedKind(call, vgAlias); ok {
				recordCall(known, &errs, path, call.Args, loggedNameIndex, loggedUnitIndex, kind)
			} else if kind, ok := otelMethodKind[sel.Sel.Name]; ok {
				recordDirectOTelCall(known, &errs, path, call.Args, kind, metricAlias)
			}
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok {
			if kind, ok := closures[ident.Name]; ok {
				recordCall(known, &errs, path, call.Args, closureNameIndex, closureUnitIndex, kind)
			}
		}
		return true
	})
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// resolveImportAlias returns the local identifier file binds
// importPath to, or "" if the file does not import it at all (the
// common case for both import paths Known cares about: most Go files
// in the repo register no metrics at all).
func resolveImportAlias(file *ast.File, importPath string) string {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return filepath.Base(path)
	}
	return ""
}

// directKind reports whether call is a direct alias.Counter/
// alias.Histogram call, and which kind it is.
func directKind(call *ast.CallExpr, alias string) (registrationKind, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok || x.Name != alias {
		return 0, false
	}
	switch sel.Sel.Name {
	case "Counter":
		return kindCounter, true
	case "Histogram":
		return kindHistogram, true
	default:
		return 0, false
	}
}

// loggedKind is directKind's sibling for the alias.CounterLogged/
// alias.HistogramLogged shape, whose logger parameter shifts the
// name/unit argument positions (loggedNameIndex/loggedUnitIndex).
func loggedKind(call *ast.CallExpr, alias string) (registrationKind, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok || x.Name != alias {
		return 0, false
	}
	switch sel.Sel.Name {
	case "CounterLogged":
		return kindCounter, true
	case "HistogramLogged":
		return kindHistogram, true
	default:
		return 0, false
	}
}

// otelMethodKind maps the twelve metric.Meter instrument-creation
// method names to their structural suffix kind, for a registration
// made directly against the OTel SDK rather than through this
// package's vgotel.Counter/Histogram wrapper. Matched by method name
// alone in scanFile - never by resolving the receiver's static type,
// since Known parses syntax only - which is safe here: the twelve
// names are distinctive enough that an unrelated method sharing one
// would be a remarkable coincidence, and grepping services/ and
// libs/go/ while building this confirms none exists. Every Int64/
// Float64 pair maps to the same kind - the SDK's value type never
// changes the Prometheus name it expands to.
var otelMethodKind = map[string]registrationKind{
	"Int64Counter":             kindCounter,
	"Float64Counter":           kindCounter,
	"Int64ObservableCounter":   kindCounter,
	"Float64ObservableCounter": kindCounter,

	"Int64Histogram":   kindHistogram,
	"Float64Histogram": kindHistogram,

	"Int64UpDownCounter":             kindGauge,
	"Float64UpDownCounter":           kindGauge,
	"Int64ObservableUpDownCounter":   kindGauge,
	"Float64ObservableUpDownCounter": kindGauge,
	"Int64ObservableGauge":           kindGauge,
	"Float64ObservableGauge":         kindGauge,
}

// passThroughClosures finds every local closure in file shaped like
// social/collection/bff's counter/histogram helper: a short variable
// declaration (x := func(...) {...}) whose body calls
// alias.Counter/alias.Histogram, passing the closure's own parameters
// straight through in the same order vgotel.Counter/Histogram itself
// declares them (skipping only the meter, which the closure captures
// rather than accepting as a parameter). The returned map is
// closure-variable-name -> which vgotel function it wraps, letting
// scanFile's call-site walk treat a call to that variable exactly like
// a direct call, just at the closure's own (meter-less) argument
// offsets.
func passThroughClosures(file *ast.File, alias string) map[string]registrationKind {
	closures := make(map[string]registrationKind)

	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		ident, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		lit, ok := assign.Rhs[0].(*ast.FuncLit)
		if !ok {
			return true
		}
		if kind, ok := passThroughKind(lit, alias); ok {
			closures[ident.Name] = kind
		}
		return true
	})

	return closures
}

// passThroughKind inspects one function literal's body for a call to
// alias.Counter/alias.Histogram whose arguments (after the meter)
// match lit's own parameters, one-for-one, in declaration order.
func passThroughKind(lit *ast.FuncLit, alias string) (registrationKind, bool) {
	if lit.Type == nil || lit.Type.Params == nil {
		return 0, false
	}
	params := paramNames(lit.Type.Params)

	var (
		found registrationKind
		ok    bool
	)
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if ok {
			return false
		}
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return true
		}
		kind, isDirect := directKind(call, alias)
		if !isDirect {
			return true
		}
		if passThroughArgs(call.Args, params) {
			found, ok = kind, true
			return false
		}
		return true
	})
	return found, ok
}

// paramNames flattens a parameter field list ("func(a, b string)"
// groups a and b under one *ast.Field) into one name per parameter, in
// declaration order.
func paramNames(fl *ast.FieldList) []string {
	var names []string
	for _, f := range fl.List {
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
	}
	return names
}

// passThroughArgs reports whether callArgs (a call to
// vgotel.Counter/Histogram found inside a closure body) passes that
// closure's own params straight through starting at index 1 (index 0
// is the meter, a free variable the closure captures rather than
// receives) - i.e. callArgs[i+1] is a bare reference to params[i], for
// every param including a trailing variadic (histogram's buckets).
func passThroughArgs(callArgs []ast.Expr, params []string) bool {
	if len(callArgs) != len(params)+1 {
		return false
	}
	for i, p := range params {
		arg := callArgs[i+1]
		if el, isSpread := arg.(*ast.Ellipsis); isSpread {
			arg = el.Elt
		}
		ident, ok := arg.(*ast.Ident)
		if !ok || ident.Name != p {
			return false
		}
	}
	return true
}

// recordCall extracts a call's literal name/unit arguments (at
// nameIdx/unitIdx) and folds every Prometheus name they expand to into
// known. A non-literal name or unit (e.g. a name built at runtime)
// contributes nothing - see Known's doc comment - rather than an
// error; only a recognized-but-unrecognized unit on an otherwise-
// literal registration is a scan error (see expandNames).
func recordCall(known map[string]struct{}, errs *[]error, path string, args []ast.Expr, nameIdx, unitIdx int, kind registrationKind) {
	if len(args) <= unitIdx {
		return
	}
	name, ok := stringLit(args[nameIdx])
	if !ok {
		return
	}
	unit, ok := stringLit(args[unitIdx])
	if !ok {
		return
	}

	names, err := expandNames(name, unit, kind)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: metric %q: %w", path, name, err))
		return
	}
	for _, n := range names {
		known[n] = struct{}{}
	}
}

// recordDirectOTelCall extracts one direct metric.Meter registration
// call's literal name - the call's own first argument, since this is
// a method call on the meter itself rather than vgotel.Counter/
// Histogram's free-function shape (there is no separate meter argument
// to skip) - and its unit, if any, from a metric.WithUnit(...) option
// among the rest (see findWithUnit), then folds every Prometheus name
// they expand to into known. Same non-literal-name and unrecognized-
// unit handling as recordCall.
func recordDirectOTelCall(known map[string]struct{}, errs *[]error, path string, args []ast.Expr, kind registrationKind, metricAlias string) {
	if len(args) == 0 {
		return
	}
	name, ok := stringLit(args[0])
	if !ok {
		return
	}

	names, err := expandNames(name, findWithUnit(args[1:], metricAlias), kind)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: metric %q: %w", path, name, err))
		return
	}
	for _, n := range names {
		known[n] = struct{}{}
	}
}

// findWithUnit scans opts - a registration call's option arguments,
// e.g. metric.WithDescription(...), metric.WithUnit(...) - for a
// metricAlias.WithUnit call and returns its literal argument. Returns
// "" (the same "no suffix" treatment an explicit empty unit gets) if
// no such option is present - libs/go/pgkit's and libs/go/valkeykit's
// own real registrations always carry one, but nothing requires it -
// or if metricAlias is "" (the file does not import
// go.opentelemetry.io/otel/metric at all, so it cannot be calling
// metric.WithUnit either).
func findWithUnit(opts []ast.Expr, metricAlias string) string {
	if metricAlias == "" {
		return ""
	}
	for _, opt := range opts {
		call, ok := opt.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || x.Name != metricAlias || sel.Sel.Name != "WithUnit" {
			continue
		}
		if unit, ok := stringLit(call.Args[0]); ok {
			return unit
		}
	}
	return ""
}

// stringLit reports the literal string value of e, if e is a plain
// string literal (not a concatenation, constant reference, or any
// other expression form).
func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// expandNames turns one registered (name, unit) pair into every
// Prometheus series name it produces: dots become underscores, the
// exporter's unit suffix (see unitSuffix) is appended, and then a
// counter contributes one "_total"-suffixed name, a histogram
// contributes three ("_bucket", "_count", "_sum" - a Prometheus
// histogram has no bare queryable series under the base name itself),
// and a gauge contributes exactly the base name (not monotonic, so no
// structural suffix at all).
func expandNames(name, unit string, kind registrationKind) ([]string, error) {
	suffix, err := unitSuffix(unit)
	if err != nil {
		return nil, err
	}
	base := strings.ReplaceAll(name, ".", "_") + suffix

	switch kind {
	case kindCounter:
		return []string{base + "_total"}, nil
	case kindHistogram:
		return []string{base + "_bucket", base + "_count", base + "_sum"}, nil
	case kindGauge:
		return []string{base}, nil
	default:
		return nil, fmt.Errorf("unknown registration kind %v", kind)
	}
}

// unitSuffix implements the exporter's documented unit-suffix rules:
// "s" and "ms" are duration units, "By" is a byte-count unit, and a
// curly-brace unit (e.g. "{event}") is a semantic annotation the
// exporter drops rather than turning into a name suffix. An empty unit
// is treated the same as a curly-brace one (a legitimate "no unit"
// declaration, not a mistake). Anything else is not one of the forms
// this repo's registrations use, so it fails loud rather than silently
// picking a suffix (or none) that might be wrong - a genuinely new
// unit form should extend this table deliberately, not slip through
// unnoticed.
func unitSuffix(unit string) (string, error) {
	switch {
	case unit == "":
		return "", nil
	case strings.HasPrefix(unit, "{") && strings.HasSuffix(unit, "}"):
		return "", nil
	case unit == "s":
		return "_seconds", nil
	case unit == "ms":
		return "_milliseconds", nil
	case unit == "By":
		return "_bytes", nil
	default:
		return "", fmt.Errorf("unrecognized unit %q (known forms: {x}, s, ms, By)", unit)
	}
}
