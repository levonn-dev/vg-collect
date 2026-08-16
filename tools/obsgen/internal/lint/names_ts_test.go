package lint_test

import (
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/lint"
)

// TestKnown_TypeScriptExtractsRepresentativeRegistrations proves
// names.Known's TypeScript pass (dashboard-linter design, section
// 13(f)) against a fixture tree covering every shape real
// frontend/src/telemetryImpl.ts registrations use, plus the one
// boundary case that is not a registration at all: a multi-line
// options object whose unit sits beside a nested advice object (proves
// the bracket-depth tracker survives nested {}/[] without ending the
// call early), a unitless counter, a plain (non-interpolated) backtick
// literal name, a receiver-less call (must not match at all - see
// widget.tsx for the .tsx extension too), and an ObservableGauge (no
// structural suffix). See TestKnown_TypeScriptInterpolatedTemplateErrors
// for the template-interpolation error case, kept in its own fixture
// tree because Known discards its whole return value on any scan
// error.
func TestKnown_TypeScriptExtractsRepresentativeRegistrations(t *testing.T) {
	known, err := lint.Known("testdata/names-ts-valid")
	if err != nil {
		t.Fatalf("Known: unexpected error: %v", err)
	}

	want := []string{
		// frontend/src/telemetry.ts: multi-line options object, "ms" unit
		// nested beside an advice object carrying its own {}/[] - histogram
		// -> three suffixes.
		"vg_widget_latency_milliseconds_bucket",
		"vg_widget_latency_milliseconds_count",
		"vg_widget_latency_milliseconds_sum",
		// frontend/src/telemetry.ts: unitless counter.
		"vg_widget_boot_total",
		// frontend/src/telemetry.ts: backtick literal name, no interpolation.
		"vg_widget_errors_total",
		// frontend/src/telemetry.ts: ObservableGauge, no unit -> no
		// structural suffix at all.
		"vg_widget_pool_connections",
		// frontend/src/widget.tsx: proves .tsx files are scanned too;
		// UpDownCounter -> no structural suffix.
		"vg_widget_active_sessions",
	}
	for _, name := range want {
		if _, ok := known[name]; !ok {
			t.Errorf("Known()[%q] missing; got %v", name, known)
		}
	}

	// telemetry.ts's bare() helper calls createCounter with no receiver
	// identifier at all (must not match, not even attempted); its
	// "dynamic" registration has a receiver that does match but an
	// identifier - not any string literal - as the name argument (must
	// not resolve, the same silent skip a non-literal name gets on the
	// Go side).
	for name := range known {
		if strings.Contains(name, "bare") || strings.Contains(name, "dynamic") {
			t.Errorf("Known() must not resolve a non-literal or receiver-less create call, found %q", name)
		}
	}

	// telemetry.ts's concatQuote/concatBacktick registrations both
	// concatenate a leading literal ('vg.widget.' and `vg.widget.`,
	// quote and backtick form) with a "kind" variable via "+": the
	// literal is only the left operand of a larger expression, not the
	// call's whole first argument, so - like bare() and dynamic above -
	// neither must resolve to anything. In particular neither may
	// produce the phantom name "vg_widget__total" a scanner that read
	// only the leading literal and never checked what followed it would
	// otherwise record (both forms concatenate the identical literal
	// text, so a scanner with that bug would collapse them into this
	// one shared entry).
	if _, ok := known["vg_widget__total"]; ok {
		t.Errorf("Known() resolved a concatenated first argument to phantom name %q; a concatenation must contribute nothing, silently", "vg_widget__total")
	}

	// Exact size: proves no extra names snuck in beyond the ones wanted
	// above.
	if len(known) != len(want) {
		t.Errorf("len(Known()) = %d, want %d; got %v", len(known), len(want), known)
	}
}

// TestKnown_TypeScriptMissingFrontendDirIsNotAnError proves a repoRoot
// with a real services/libs/go tree but no frontend/src directory at
// all scans cleanly - contributes zero TypeScript-derived names, not
// an error - unlike a missing services/ or libs/go/ root (see
// TestKnown_MissingTree), which does fail loud. frontend/src does not
// get that same strictness: a repoRoot pointing at the wrong place
// entirely is already caught by the services/libs/go checks (a real
// vgkeep checkout always has both alongside frontend/src), so treating
// frontend/src's own absence as fatal would only ever fire for a
// fixture tree with no frontend content to model - exactly
// testdata/names-valid's own shape (no frontend/src directory at all),
// reused here as the proof.
func TestKnown_TypeScriptMissingFrontendDirIsNotAnError(t *testing.T) {
	known, err := lint.Known("testdata/names-valid")
	if err != nil {
		t.Fatalf("Known: unexpected error: %v", err)
	}
	for name := range known {
		if strings.HasPrefix(name, "vg_frontend_") {
			t.Errorf("Known() found a TypeScript-derived name %q from a tree with no frontend/src", name)
		}
	}
}

// TestKnown_TypeScriptInterpolatedTemplateErrors proves a template
// literal name containing "${" fails loud (a scan error) rather than
// silently contributing nothing, unlike an ordinary non-literal name
// (an identifier, a concatenation) - see recordTSCall's doc comment
// for why the two cases are treated differently.
func TestKnown_TypeScriptInterpolatedTemplateErrors(t *testing.T) {
	_, err := lint.Known("testdata/names-ts-interpolated")
	if err == nil {
		t.Fatal("Known: want an error for a template literal with interpolation, got nil")
	}
	if !strings.Contains(err.Error(), "interpolation") {
		t.Errorf("Known error = %q, want it to mention interpolation", err.Error())
	}
	if !strings.Contains(err.Error(), "vg.widget.") {
		t.Errorf("Known error = %q, want it to quote the offending template text", err.Error())
	}
}

// TestKnown_TypeScriptRealTree proves the TypeScript pass resolves the
// real frontend/src/telemetryImpl.ts registrations against the actual
// repository tree, not a fixture - a frontend metric rename becomes an
// immediate failure here, not just a lint finding discovered later.
// All nine names are cross-checked against
// deploy/charts/platform/files/dashboards/frontend.json's own queries
// (the ground truth the dashboard lint must satisfy): every distinct
// vg_frontend_* token its exprs reference, extracted independently of
// this package (not read back from telemetryImpl.ts or from Known's
// own output) via
//
//	grep -o 'vg_frontend_[a-z_]*' deploy/charts/platform/files/dashboards/frontend.json | sort -u
//
// which returns exactly these nine: six unitless counters' _total
// names, plus the _bucket name for each of the three "ms"/unitless
// web-vitals histograms (frontend.json never queries the matching
// _count/_sum series, so only _bucket is ground truth here - unlike
// TestKnown_TypeScriptExtractsRepresentativeRegistrations's fixture
// assertion, which does check _count/_sum too, since that fixture's
// "want" list is authored directly from the registration, not
// cross-checked against a dashboard).
func TestKnown_TypeScriptRealTree(t *testing.T) {
	known, err := lint.Known("../../../..")
	if err != nil {
		t.Fatalf("Known: unexpected error scanning the real repo tree: %v", err)
	}
	for _, name := range []string{
		"vg_frontend_api_failures_total",
		"vg_frontend_errors_total",
		"vg_frontend_locale_boot_total",
		"vg_frontend_locale_catalog_failures_total",
		"vg_frontend_locale_switches_total",
		"vg_frontend_prose_fallback_served_total",
		"vg_frontend_web_vitals_cls_bucket",
		"vg_frontend_web_vitals_inp_milliseconds_bucket",
		"vg_frontend_web_vitals_lcp_milliseconds_bucket",
	} {
		if _, ok := known[name]; !ok {
			t.Errorf("Known(real repo tree)[%q] missing; want the TypeScript pass to find it in frontend/src/telemetryImpl.ts", name)
		}
	}
}
