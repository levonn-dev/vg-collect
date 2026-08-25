package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunGen_Success proves the load-assemble-emit path with a small
// internally-consistent manifest tree (one service, panel_ref resolves).
func TestRunGen_Success(t *testing.T) {
	if err := runGen("testdata/gen-success", t.TempDir()); err != nil {
		t.Fatalf("runGen: unexpected error: %v", err)
	}
}

// TestRunGen_WritesOutputs proves the outputs carry their generated-by
// headers, land at canonical paths, and a second run is byte-identical.
func TestRunGen_WritesOutputs(t *testing.T) {
	outRoot := t.TempDir()
	if err := runGen("testdata/gen-success", outRoot); err != nil {
		t.Fatalf("runGen: unexpected error: %v", err)
	}

	rulesPath := filepath.Join(outRoot, "deploy/charts/platform/files/alerting/vg-rules.yaml")
	rules, err := os.ReadFile(rulesPath) //nolint:gosec // G304: rulesPath is built from this test's own t.TempDir(), not external input.
	if err != nil {
		t.Fatalf("reading %s: %v", rulesPath, err)
	}
	wantHeader := "# Generated from deploy/observability/. Do not edit by hand.\n"
	if !strings.HasPrefix(string(rules), wantHeader) {
		t.Errorf("vg-rules.yaml does not start with the generated-by comment; got:\n%s", firstLines(rules, 2))
	}
	if !strings.Contains(string(rules), "uid: vg-alpha-down") {
		t.Errorf("vg-rules.yaml missing the expected rule; got:\n%s", rules)
	}

	dashPath := filepath.Join(outRoot, "deploy/charts/platform/files/dashboards/alpha.json")
	dash, err := os.ReadFile(dashPath) //nolint:gosec // G304: dashPath is built from this test's own t.TempDir(), not external input.
	if err != nil {
		t.Fatalf("reading %s: %v", dashPath, err)
	}
	if !strings.Contains(string(dash), `"description": "Generated from deploy/observability/. Do not edit by hand."`) {
		t.Errorf("alpha.json missing the generated-by description field; got:\n%s", dash)
	}

	// running gen again over the same tree must not change either output.
	if err := runGen("testdata/gen-success", outRoot); err != nil {
		t.Fatalf("runGen (second run): unexpected error: %v", err)
	}
	rules2, err := os.ReadFile(rulesPath) //nolint:gosec // G304: same fixed test-owned path as above.
	if err != nil {
		t.Fatalf("reading %s (second run): %v", rulesPath, err)
	}
	if string(rules) != string(rules2) {
		t.Error("vg-rules.yaml is not byte-idempotent across repeated gen runs")
	}
	dash2, err := os.ReadFile(dashPath) //nolint:gosec // G304: same fixed test-owned path as above.
	if err != nil {
		t.Fatalf("reading %s (second run): %v", dashPath, err)
	}
	if string(dash) != string(dash2) {
		t.Error("alpha.json is not byte-idempotent across repeated gen runs")
	}
}

func firstLines(b []byte, n int) string {
	lines := strings.SplitN(string(b), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// TestRunGen_Errors proves every failure path is distinguishable by its message prefix.
func TestRunGen_Errors(t *testing.T) {
	cases := []struct {
		name    string
		dir     string
		outRoot func(t *testing.T) string
		want    string
	}{
		{
			name:    "manifest tree does not exist",
			dir:     "testdata/does-not-exist",
			outRoot: func(t *testing.T) string { return t.TempDir() },
			want:    "loading manifests:",
		},
		{
			name:    "manifest loads but an alert panel_ref does not resolve",
			dir:     "internal/manifest/testdata/valid",
			outRoot: func(t *testing.T) string { return t.TempDir() },
			want:    "assembling dashboards:",
		},
		{
			name: "outRoot's target directory cannot be created (MkdirAll fails)",
			dir:  "testdata/gen-success",
			outRoot: func(t *testing.T) string {
				// a file where MkdirAll needs a directory forces failure portably,
				// without unwritable permissions (root bypasses those in CI).
				root := t.TempDir()
				blocker := filepath.Join(root, "deploy")
				if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
					t.Fatalf("writing blocker file: %v", err)
				}
				return root
			},
			want: "writing outputs:",
		},
		{
			name: "vg-rules.yaml's own path is blocked (WriteFile fails)",
			dir:  "testdata/gen-success",
			outRoot: func(t *testing.T) string {
				// the file path itself is pre-occupied by a directory, so
				// WriteFile (not MkdirAll) fails here, unlike the case above.
				root := t.TempDir()
				rulesPath := filepath.Join(root, "deploy/charts/platform/files/alerting/vg-rules.yaml")
				if err := os.MkdirAll(rulesPath, 0o750); err != nil {
					t.Fatalf("pre-creating a directory at the rules path: %v", err)
				}
				return root
			},
			want: "writing outputs:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runGen(tc.dir, tc.outRoot(t))
			if err == nil {
				t.Fatal("runGen: want an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("runGen error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestRunLint_Clean proves a tree with no lint problems (anchors and
// panel_ref resolve, every expr names a known metric) exits clean.
func TestRunLint_Clean(t *testing.T) {
	dir := "testdata/lint-clean/deploy/observability"
	if err := runLint(dir, "testdata/lint-clean"); err != nil {
		t.Fatalf("runLint: unexpected error: %v", err)
	}
}

// TestRunLint_Findings proves a tree with real problems (unknown metric,
// missing runbook anchor) fails, naming how many findings fired.
func TestRunLint_Findings(t *testing.T) {
	dir := "testdata/lint-findings/deploy/observability"
	err := runLint(dir, "testdata/lint-findings")
	if err == nil {
		t.Fatal("runLint: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "lint finding(s)") {
		t.Errorf("runLint error = %q, want it to mention lint finding(s)", err.Error())
	}
}

// TestRunLint_LoadError proves a load failure short-circuits before
// lint.Run runs, with the same "loading manifests:" prefix runGen uses.
func TestRunLint_LoadError(t *testing.T) {
	err := runLint("testdata/does-not-exist", "testdata/lint-clean")
	if err == nil {
		t.Fatal("runLint: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "loading manifests:") {
		t.Errorf("runLint error = %q, want it to contain %q", err.Error(), "loading manifests:")
	}
}
