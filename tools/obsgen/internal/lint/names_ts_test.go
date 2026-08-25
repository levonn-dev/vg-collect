package lint_test

import (
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/lint"
)

// TestKnown_TypeScriptExtractsRepresentativeRegistrations proves
// names.Known's TypeScript pass against a fixture covering every real
// registration shape (nested options object, unitless counter, backtick
// literal, receiver-less call that must not match, ObservableGauge).
// See TestKnown_TypeScriptInterpolatedTemplateErrors for the
// interpolation error case, kept separate since Known discards its whole return on any scan error.
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

	// bare() calls createCounter with no receiver (must not match); "dynamic"
	// has a receiver but an identifier, not a literal, name (must not resolve).
	for name := range known {
		if strings.Contains(name, "bare") || strings.Contains(name, "dynamic") {
			t.Errorf("Known() must not resolve a non-literal or receiver-less create call, found %q", name)
		}
	}

	// concatQuote/concatBacktick concatenate a leading literal with a
	// variable via "+"; the literal isn't the whole argument, so neither
	// must resolve, and a scanner bug reading only the leading literal
	// would collapse both into the phantom name "vg_widget__total".
	if _, ok := known["vg_widget__total"]; ok {
		t.Errorf("Known() resolved a concatenated first argument to phantom name %q; a concatenation must contribute nothing, silently", "vg_widget__total")
	}

	// exact size proves no extra names snuck in beyond want.
	if len(known) != len(want) {
		t.Errorf("len(Known()) = %d, want %d; got %v", len(known), len(want), known)
	}
}

// TestKnown_TypeScriptMissingFrontendDirIsNotAnError proves a missing
// frontend/src scans cleanly (zero names, not an error), unlike a
// missing services/ or libs/go/ root (see TestKnown_MissingTree).
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

// TestKnown_TypeScriptInterpolatedTemplateErrors proves a "${"
// template literal fails loud (scan error), unlike an ordinary non-literal name.
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

// TestKnown_TypeScriptRealTree proves the TypeScript pass resolves
// real frontend/src/telemetryImpl.ts registrations against the actual
// repo tree, not a fixture. The nine names are cross-checked against
// frontend.json's own queries, extracted independently via
//
//	grep -o 'vg_frontend_[a-z_]*' deploy/charts/platform/files/dashboards/frontend.json | sort -u
//
// Only _bucket is checked for the three histograms: frontend.json
// never queries their _count/_sum series.
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
