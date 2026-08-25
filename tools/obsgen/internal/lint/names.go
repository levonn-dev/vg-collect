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
// resolves a per-file local alias against; it never hardcodes either
// spelling. Every real caller aliases vgotelImportPath as "vgotel"
// (libs/go/otel's own package name, "otel", collides with the upstream SDK import).
const (
	vgotelImportPath = "github.com/levonn-dev/vgkeep/libs/go/otel"
	metricImportPath = "go.opentelemetry.io/otel/metric"
)

// vgotel.Counter/Histogram's signature is (meter, name, description,
// unit, ...): name at index 1, unit at index 3. A pass-through closure
// drops the meter (captured, not passed), shifting both two positions
// earlier. The Logged variants insert a logger after meter, shifting
// name/unit one position right.
const (
	directNameIndex  = 1
	directUnitIndex  = 3
	loggedNameIndex  = 2
	loggedUnitIndex  = 4
	closureNameIndex = 0
	closureUnitIndex = 2
)

// registrationKind distinguishes Counter (_total suffix), Histogram
// (_bucket/_count/_sum), and Gauge (no structural suffix, not monotonic).
type registrationKind int

const (
	kindCounter registrationKind = iota
	kindHistogram
	kindGauge
)

// Known scans every .go file under repoRoot/services and
// repoRoot/libs/go, plus every .ts/.tsx file under repoRoot/frontend/src
// (see names_ts.go), for metric registrations and returns every
// Prometheus-form name they expand to (see expandNames). It recognizes
// three Go call shapes (direct vgotel call, pass-through closure, direct
// OTel SDK call - see otelMethodKind) plus one TypeScript shape
// (RECEIVER.create<Kind>("name", { unit: "..." })). A registration whose
// name is not a literal at one of these call shapes contributes nothing
// (no error: a dynamically-built name is out of scope by construction),
// except a TypeScript template literal containing interpolation, which
// names_ts.go treats as a scan error rather than a silent skip.
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

// scanTree walks every non-test .go file under root. _test.go is
// skipped deliberately: libs/go/otel/emit_test.go registers throwaway
// counters with unit "u", not a recognized form, which would fail Known on every run.
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
// known. Parsing is syntax-only (go/parser, no go/types), so a fixture
// whose imports don't resolve to a real module still scans correctly.
func scanFile(path string, known map[string]struct{}) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	vgAlias := resolveImportAlias(file, vgotelImportPath)
	metricAlias := resolveImportAlias(file, metricImportPath)
	// an empty vgAlias (file doesn't import libs/go/otel) is safe: directKind
	// never matches "", so no closures are found.
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

// resolveImportAlias returns the local identifier file binds importPath
// to, or "" if the file doesn't import it at all (the common case).
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
// method names to their suffix kind, matched by name alone (syntax-only
// parsing, no receiver-type resolution) since the names are distinctive
// enough that a collision is a remarkable coincidence.
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

// passThroughClosures finds every local closure shaped like a
// counter/histogram helper (x := func(...) {...} calling
// alias.Counter/Histogram with params passed straight through, meter
// skipped). Returns closure-variable-name -> which vgotel function it wraps.
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

// passThroughArgs reports whether callArgs passes params straight
// through starting at index 1 (index 0 is the meter), including a trailing variadic.
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

// recordCall extracts a call's literal name/unit args and folds every
// expanded name into known. A non-literal name/unit contributes nothing;
// an unrecognized unit on a literal registration is a scan error.
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

// recordDirectOTelCall extracts a direct metric.Meter call's literal
// name (its first argument, no meter to skip) and unit (from
// metric.WithUnit, see findWithUnit); same handling as recordCall.
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

// findWithUnit scans opts for a metricAlias.WithUnit call and returns
// its literal argument, or "" if absent (same as an explicit empty
// unit) or metricAlias is "" (unimported).
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

// expandNames turns one (name, unit) pair into every Prometheus series
// name: dots become underscores, unitSuffix is appended, then counter ->
// one _total name, histogram -> three (_bucket/_count/_sum), gauge -> the base name.
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

// unitSuffix implements the exporter's unit-suffix rules: s/ms are
// durations, By is bytes, {x} and empty are no-suffix. Anything else
// fails loud rather than silently guessing a suffix.
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
