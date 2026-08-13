package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates testdata/domain.go.golden and domain.ts.golden
// from the emitters' actual output instead of comparing against them:
// `go test -run TestGenerate_MatchesGoldenFixtures -update`. Every use
// requires re-reading the diff and reviewing it before committing -
// this flag records verified-correct output, it does not decide
// correctness.
var update = flag.Bool("update", false, "write actual emitter output to the golden files instead of comparing")

// readTestdata loads a fixture or golden file from testdata/; go test
// runs with the package directory as its working directory, so the
// relative path is stable regardless of the invoking directory.
func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name) //nolint:gosec // G304: name is always a literal passed by this package's own tests, never external input.
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return data
}

// TestGenerate_MatchesGoldenFixtures is the base case: parse a fixture
// yaml, emit both languages, and compare byte-for-byte against golden
// files checked into testdata/. This is the primary regression guard -
// any change to either emitter's output (intentional or not) shows up
// here as a diff against a reviewed golden.
func TestGenerate_MatchesGoldenFixtures(t *testing.T) {
	dom, err := parseDomain(readTestdata(t, "domain.yaml"))
	if err != nil {
		t.Fatalf("parseDomain: %v", err)
	}

	goSrc, err := generateGo(dom, "regionkit")
	if err != nil {
		t.Fatalf("generateGo: %v", err)
	}
	tsSrc, err := generateTS(dom)
	if err != nil {
		t.Fatalf("generateTS: %v", err)
	}

	if *update {
		if err := os.WriteFile("testdata/domain.go.golden", goSrc, 0o600); err != nil {
			t.Fatalf("writing testdata/domain.go.golden: %v", err)
		}
		if err := os.WriteFile("testdata/domain.ts.golden", tsSrc, 0o600); err != nil {
			t.Fatalf("writing testdata/domain.ts.golden: %v", err)
		}
		t.Skip("golden files updated; re-run without -update to verify")
	}

	if want := readTestdata(t, "domain.go.golden"); string(goSrc) != string(want) {
		t.Errorf("generateGo output does not match testdata/domain.go.golden\n--- got ---\n%s\n--- want ---\n%s", goSrc, want)
	}
	if want := readTestdata(t, "domain.ts.golden"); string(tsSrc) != string(want) {
		t.Errorf("generateTS output does not match testdata/domain.ts.golden\n--- got ---\n%s\n--- want ---\n%s", tsSrc, want)
	}
}

// TestGenerate_RowAdditionPropagatesToBothOutputs proves the single-
// source-of-truth property the whole generator exists for: one added
// yaml row (a korea region, with its own class and localization
// chain) shows up in BOTH language outputs from the same generator
// run, with no per-language hand edit.
func TestGenerate_RowAdditionPropagatesToBothOutputs(t *testing.T) {
	dom, err := parseDomain(readTestdata(t, "domain_extra_row.yaml"))
	if err != nil {
		t.Fatalf("parseDomain: %v", err)
	}

	goSrc, err := generateGo(dom, "regionkit")
	if err != nil {
		t.Fatalf("generateGo: %v", err)
	}
	tsSrc, err := generateTS(dom)
	if err != nil {
		t.Fatalf("generateTS: %v", err)
	}

	for _, want := range []string{"korea", "ko-KR"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generateGo output missing %q from the added region row", want)
		}
		if !strings.Contains(string(tsSrc), want) {
			t.Errorf("generateTS output missing %q from the added region row", want)
		}
	}
}

// TestGenerate_ByteIdempotent locks down determinism: nothing in
// either emitter may depend on map iteration order, wall-clock time,
// or any other source of run-to-run variance. Repeated generation
// from the same parsed domain, and repeated parsing of the same yaml
// bytes, must produce byte-identical output every time - the property
// `task gen` run twice in a row relies on for a clean drift check.
func TestGenerate_ByteIdempotent(t *testing.T) {
	yamlData := readTestdata(t, "domain.yaml")

	dom, err := parseDomain(yamlData)
	if err != nil {
		t.Fatalf("parseDomain: %v", err)
	}

	go1, err := generateGo(dom, "regionkit")
	if err != nil {
		t.Fatalf("generateGo (1st): %v", err)
	}
	go2, err := generateGo(dom, "regionkit")
	if err != nil {
		t.Fatalf("generateGo (2nd): %v", err)
	}
	if string(go1) != string(go2) {
		t.Errorf("generateGo is not byte-idempotent across repeated calls on the same *domain")
	}

	ts1, err := generateTS(dom)
	if err != nil {
		t.Fatalf("generateTS (1st): %v", err)
	}
	ts2, err := generateTS(dom)
	if err != nil {
		t.Fatalf("generateTS (2nd): %v", err)
	}
	if string(ts1) != string(ts2) {
		t.Errorf("generateTS is not byte-idempotent across repeated calls on the same *domain")
	}

	// Close the loop end to end: re-parsing the same source bytes and
	// regenerating must match too, not just repeated calls sharing one
	// already-parsed *domain value.
	dom2, err := parseDomain(yamlData)
	if err != nil {
		t.Fatalf("parseDomain (2nd parse): %v", err)
	}
	go3, err := generateGo(dom2, "regionkit")
	if err != nil {
		t.Fatalf("generateGo (3rd, reparsed): %v", err)
	}
	if string(go1) != string(go3) {
		t.Errorf("generateGo output differs after re-parsing the same yaml bytes")
	}
}

// TestRun_RoundTripWritesBothOutputsMatchingGoldens exercises run()
// (and, through it, writeFile) end to end exactly as `task gen:domain`
// invokes it: a real yaml path in, two real file paths out. The
// output directory is a nested path that does not exist yet, so
// writeFile's os.MkdirAll actually creates something rather than
// no-op'ing against an already-present directory.
func TestRun_RoundTripWritesBothOutputsMatchingGoldens(t *testing.T) {
	dir := t.TempDir()
	goOut := filepath.Join(dir, "nested", "go", "tables_gen.go")
	tsOut := filepath.Join(dir, "nested", "ts", "domain.ts")

	if err := run("testdata/domain.yaml", goOut, "regionkit", tsOut); err != nil {
		t.Fatalf("run: %v", err)
	}

	gotGo, err := os.ReadFile(goOut) //nolint:gosec // G304: goOut is a path this same test built with filepath.Join under t.TempDir(), never external input.
	if err != nil {
		t.Fatalf("reading run's Go output: %v", err)
	}
	if want := readTestdata(t, "domain.go.golden"); string(gotGo) != string(want) {
		t.Errorf("run's Go output does not match testdata/domain.go.golden\n--- got ---\n%s\n--- want ---\n%s", gotGo, want)
	}

	gotTS, err := os.ReadFile(tsOut) //nolint:gosec // G304: tsOut is a path this same test built with filepath.Join under t.TempDir(), never external input.
	if err != nil {
		t.Fatalf("reading run's TS output: %v", err)
	}
	if want := readTestdata(t, "domain.ts.golden"); string(gotTS) != string(want) {
		t.Errorf("run's TS output does not match testdata/domain.ts.golden\n--- got ---\n%s\n--- want ---\n%s", gotTS, want)
	}
}

// TestRun_MissingYamlFileReturnsError covers run's read-error branch
// (the source yaml path does not exist) - the same branch main()
// reports to stderr and turns into a nonzero exit.
func TestRun_MissingYamlFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	err := run(filepath.Join(dir, "does-not-exist.yaml"), filepath.Join(dir, "out.go"), "regionkit", filepath.Join(dir, "out.ts"))
	if err == nil {
		t.Fatal("run: want an error for a missing yaml file, got nil")
	}
}

// TestParseDomain_UnknownKeyInReleaseRegionsRejected pins KnownFields(true)
// for the new release_regions section: a typo'd field name in one of its
// rows must fail the parse, the same guarantee every other section already
// has, rather than silently vanishing (parseDomain's whole reason to set
// KnownFields at all).
func TestParseDomain_UnknownKeyInReleaseRegionsRejected(t *testing.T) {
	if _, err := parseDomain(readTestdata(t, "domain_unknown_key_release_regions.yaml")); err == nil {
		t.Fatal("parseDomain: want an error for an unknown key in a release_regions row, got nil")
	}
}

// TestParseDomain_UnknownKeyInRegionsRejected pins the same KnownFields(true)
// rejection for a pre-existing section (regions), so the strict-parsing
// guarantee is proven generally rather than only for release_regions above.
func TestParseDomain_UnknownKeyInRegionsRejected(t *testing.T) {
	if _, err := parseDomain(readTestdata(t, "domain_unknown_key_regions.yaml")); err == nil {
		t.Fatal("parseDomain: want an error for an unknown key in a regions row, got nil")
	}
}
