package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

// --- facets: constraint values mirrored from a bundled OpenAPI document ----
//
// testdata/facets.bundled.yaml is a small fixture standing in for
// api/bundled/bff.yaml, sized so a human reads the whole thing in one
// screen: Widget carries a non-enum property (name), an untouched
// nested $ref (tag), and an inline enum property (status) that must
// lose its enum key but keep the rest of Widget's entry. Tag has no
// enum at all (sanity: unaffected schemas emit byte-for-byte as
// before). Kind is a pure enum vocabulary (only "type" and "enum") -
// schema.ts already exports its values by name, so it earns no
// schemas entry at all. components.parameters.sort pairs an enum with
// a default (mirrors the real entriesSort parameter): the default
// must survive even though the enum next to it drops. cursor has
// neither (sanity: an unaffected case emits its facet unchanged, exactly
// as it always has). listWidgets
// mixes an inline numeric-only param (limit), an inline $ref'd-vocabulary
// param (kind, resolved one level to Kind, which is pure enum - kind's
// facet is empty after the drop), and two $ref'd component parameters
// (cursor, sort) that never show up in operationParams at all.
// createWidget has no parameters, so it is absent from operationParams
// entirely.

func loadFacetsFixture(t *testing.T) map[string]interface{} {
	t.Helper()
	doc, err := parseBundle(readTestdata(t, "facets.bundled.yaml"))
	if err != nil {
		t.Fatalf("parseBundle: %v", err)
	}
	return doc
}

func TestBuildFacets_SchemaDropsEnumButKeepsOtherContent(t *testing.T) {
	f, err := buildFacets(loadFacetsFixture(t))
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}

	want := map[string]interface{}{
		"type":     "object",
		"required": []interface{}{"name"},
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string", "maxLength": 40},
			"tag":  map[string]interface{}{"$ref": "#/components/schemas/Tag"},
			// enum dropped; the bare "type" that remains is a NESTED
			// property, not a top-level schemas entry, so it stays -
			// only a top-level pure-enum schema is omitted entirely.
			"status": map[string]interface{}{"type": "string"},
		},
	}
	if !reflect.DeepEqual(f.Schemas["Widget"], want) {
		t.Errorf("Schemas[Widget] = %#v, want %#v", f.Schemas["Widget"], want)
	}
}

// TestBuildFacets_PropertyLiterallyNamedEnumKeepsItsNameAndOwnFacets proves
// the enum strip is keyword-position-aware, not a blind key-name match: a
// property whose FIELD NAME happens to be "enum" is not itself a JSON-Schema
// enum keyword, so it must survive under its own name inside "properties" -
// only that property's OWN schema (one level further in) has a real enum
// keyword to strip, and only that keyword goes; the property's other facet
// (maxLength) stays right alongside it.
func TestBuildFacets_PropertyLiterallyNamedEnumKeepsItsNameAndOwnFacets(t *testing.T) {
	doc := map[string]interface{}{
		"paths": map[string]interface{}{},
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{
				"Weird": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"enum": map[string]interface{}{
							"type":      "string",
							"enum":      []interface{}{"a", "b"},
							"maxLength": 20,
						},
					},
				},
			},
			"parameters": map[string]interface{}{},
		},
	}

	f, err := buildFacets(doc)
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}

	want := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"enum": map[string]interface{}{"type": "string", "maxLength": 20},
		},
	}
	if !reflect.DeepEqual(f.Schemas["Weird"], want) {
		t.Errorf("Schemas[Weird] = %#v, want %#v", f.Schemas["Weird"], want)
	}
}

func TestBuildFacets_SchemaWithNoEnumIsUnaffected(t *testing.T) {
	f, err := buildFacets(loadFacetsFixture(t))
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}

	want := map[string]interface{}{
		"type": "string", "minLength": 1, "maxLength": 20, "pattern": "^[a-z]+$",
	}
	if !reflect.DeepEqual(f.Schemas["Tag"], want) {
		t.Errorf("Schemas[Tag] = %#v, want %#v", f.Schemas["Tag"], want)
	}
}

func TestBuildFacets_PureEnumVocabularySchemaOmittedEntirely(t *testing.T) {
	f, err := buildFacets(loadFacetsFixture(t))
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}

	if _, present := f.Schemas["Kind"]; present {
		t.Errorf("Schemas[Kind] = %#v, want no entry at all (schema.ts already exports its values)", f.Schemas["Kind"])
	}
	// total emission is unaffected by the omission: every OTHER
	// components.schemas entry still gets a facet, no curation beyond
	// the pure-enum-vocabulary rule.
	wantNames := []string{"Tag", "Widget"}
	gotNames := make([]string, 0, len(f.Schemas))
	for name := range f.Schemas {
		gotNames = append(gotNames, name)
	}
	sort.Strings(gotNames)
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("Schemas keys = %v, want %v", gotNames, wantNames)
	}
}

func TestBuildFacets_ParameterDefaultSurvivesEnumDrop(t *testing.T) {
	f, err := buildFacets(loadFacetsFixture(t))
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}

	want := map[string]interface{}{"default": "created_at"}
	if !reflect.DeepEqual(f.Parameters["sort"], want) {
		t.Errorf("Parameters[sort] = %#v, want %#v", f.Parameters["sort"], want)
	}
}

func TestBuildFacets_ParameterWithNoEnumIsUnaffected(t *testing.T) {
	f, err := buildFacets(loadFacetsFixture(t))
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}

	want := map[string]interface{}{"maxLength": 100}
	if !reflect.DeepEqual(f.Parameters["cursor"], want) {
		t.Errorf("Parameters[cursor] = %#v, want %#v", f.Parameters["cursor"], want)
	}
}

func TestBuildFacets_InlineOperationParamLocalRefResolvesEmptyAfterEnumDrop(t *testing.T) {
	f, err := buildFacets(loadFacetsFixture(t))
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}

	// kind's schema is a bare $ref to Kind, a pure enum vocabulary,
	// resolved one level; Kind carries nothing besides its now-dropped
	// enum, so kind's own facet is empty - not absent, empty: the
	// operation still has other inline params, so listWidgets itself
	// keeps its operationParams entry.
	want := map[string]interface{}{
		"limit": map[string]interface{}{"minimum": 1, "maximum": 500, "default": 200},
		"kind":  map[string]interface{}{},
	}
	if !reflect.DeepEqual(f.OperationParams["listWidgets"], want) {
		t.Errorf("OperationParams[listWidgets] = %#v, want %#v", f.OperationParams["listWidgets"], want)
	}
}

func TestBuildFacets_OperationWithNoInlineParamsAbsentFromOperationParams(t *testing.T) {
	f, err := buildFacets(loadFacetsFixture(t))
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}

	// createWidget has no parameters at all; cursor and sort on
	// listWidgets are $ref'd component parameters, not inline, so
	// neither operation contributes them here (buildParameterFacets
	// covers cursor/sort separately, proven above).
	if _, present := f.OperationParams["createWidget"]; present {
		t.Errorf("OperationParams[createWidget] = %#v, want no entry (no parameters at all)", f.OperationParams["createWidget"])
	}
}

func TestBuildFacets_BareRefSchemaThrows(t *testing.T) {
	badDoc := map[string]interface{}{
		"paths": map[string]interface{}{},
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{
				"Aliased": map[string]interface{}{"$ref": "../common.yaml#/components/schemas/Aliased"},
			},
			"parameters": map[string]interface{}{},
		},
	}
	if _, err := buildFacets(badDoc); err == nil {
		t.Fatal("buildFacets: want an error for a bare $ref components.schemas entry (unbundled input), got nil")
	}
}

func TestBuildFacets_ComponentParameterLocalRefResolves(t *testing.T) {
	doc := map[string]interface{}{
		"paths": map[string]interface{}{},
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{
				"Kind": map[string]interface{}{"type": "string", "enum": []interface{}{"alpha", "beta"}},
			},
			"parameters": map[string]interface{}{
				"kind": map[string]interface{}{
					"name": "kind", "in": "query",
					"schema": map[string]interface{}{"$ref": "#/components/schemas/Kind"},
				},
			},
		},
	}
	f, err := buildFacets(doc)
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}
	want := map[string]interface{}{"kind": map[string]interface{}{}}
	if !reflect.DeepEqual(f.Parameters, want) {
		t.Errorf("Parameters = %#v, want %#v", f.Parameters, want)
	}
}

func TestBuildFacets_InlineOperationParamLocalRefResolves(t *testing.T) {
	doc := map[string]interface{}{
		"paths": map[string]interface{}{
			"/things": map[string]interface{}{
				"get": map[string]interface{}{
					"operationId": "listThings",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "kind", "in": "query",
							"schema": map[string]interface{}{"$ref": "#/components/schemas/Kind"},
						},
					},
				},
			},
		},
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{
				"Kind": map[string]interface{}{"type": "string", "enum": []interface{}{"alpha", "beta"}},
			},
			"parameters": map[string]interface{}{},
		},
	}
	f, err := buildFacets(doc)
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}
	want := map[string]interface{}{"listThings": map[string]interface{}{"kind": map[string]interface{}{}}}
	if !reflect.DeepEqual(f.OperationParams, want) {
		t.Errorf("OperationParams = %#v, want %#v", f.OperationParams, want)
	}
}

func TestBuildFacets_ComponentParameterUnresolvableRefThrows(t *testing.T) {
	badDoc := map[string]interface{}{
		"paths": map[string]interface{}{},
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{},
			"parameters": map[string]interface{}{
				"broken": map[string]interface{}{
					"name": "broken", "in": "query",
					"schema": map[string]interface{}{"$ref": "#/components/schemas/Tag"},
				},
			},
		},
	}
	if _, err := buildFacets(badDoc); err == nil {
		t.Fatal("buildFacets: want an error for a components.parameters schema $ref with no local target, got nil")
	}
}

func TestBuildFacets_InlineOperationParamCrossFileRefThrows(t *testing.T) {
	badDoc := map[string]interface{}{
		"paths": map[string]interface{}{
			"/broken": map[string]interface{}{
				"get": map[string]interface{}{
					"operationId": "getBroken",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "kind", "in": "query",
							"schema": map[string]interface{}{"$ref": "../common.yaml#/components/schemas/Kind"},
						},
					},
				},
			},
		},
		"components": map[string]interface{}{
			"schemas":    map[string]interface{}{"Kind": map[string]interface{}{"type": "string"}},
			"parameters": map[string]interface{}{},
		},
	}
	if _, err := buildFacets(badDoc); err == nil {
		t.Fatal("buildFacets: want an error for an inline operation parameter schema $ref pointing outside the document, got nil")
	}
}

// TestGenerateFacetsTS_MatchesGoldenFixture is the facets emitter's base
// case, mirroring TestGenerate_MatchesGoldenFixtures above: build facets
// from the fixture, emit TypeScript, and compare byte-for-byte against a
// golden file reviewed once and checked into testdata/.
func TestGenerateFacetsTS_MatchesGoldenFixture(t *testing.T) {
	f, err := buildFacets(loadFacetsFixture(t))
	if err != nil {
		t.Fatalf("buildFacets: %v", err)
	}
	src, err := generateFacetsTS(f)
	if err != nil {
		t.Fatalf("generateFacetsTS: %v", err)
	}

	if *update {
		if err := os.WriteFile("testdata/facets.ts.golden", src, 0o600); err != nil {
			t.Fatalf("writing testdata/facets.ts.golden: %v", err)
		}
		t.Skip("golden file updated; re-run without -update to verify")
	}

	if want := readTestdata(t, "facets.ts.golden"); string(src) != string(want) {
		t.Errorf("generateFacetsTS output does not match testdata/facets.ts.golden\n--- got ---\n%s\n--- want ---\n%s", src, want)
	}
}

// TestGenerateFacetsTS_ByteIdempotent mirrors TestGenerate_ByteIdempotent:
// nothing in the facets walk or its TypeScript serialization may depend on
// Go's randomized map iteration order or any other source of run-to-run
// variance - the property `task gen` run twice in a row relies on for a
// clean drift check.
func TestGenerateFacetsTS_ByteIdempotent(t *testing.T) {
	yamlData := readTestdata(t, "facets.bundled.yaml")

	doc1, err := parseBundle(yamlData)
	if err != nil {
		t.Fatalf("parseBundle (1st): %v", err)
	}
	f1, err := buildFacets(doc1)
	if err != nil {
		t.Fatalf("buildFacets (1st): %v", err)
	}
	src1, err := generateFacetsTS(f1)
	if err != nil {
		t.Fatalf("generateFacetsTS (1st): %v", err)
	}

	doc2, err := parseBundle(yamlData)
	if err != nil {
		t.Fatalf("parseBundle (2nd): %v", err)
	}
	f2, err := buildFacets(doc2)
	if err != nil {
		t.Fatalf("buildFacets (2nd): %v", err)
	}
	src2, err := generateFacetsTS(f2)
	if err != nil {
		t.Fatalf("generateFacetsTS (2nd): %v", err)
	}

	if string(src1) != string(src2) {
		t.Errorf("generateFacetsTS is not byte-idempotent across repeated calls on freshly re-parsed input")
	}
}

// TestRunFacets_RoundTripWritesOutputMatchingGolden exercises runFacets end
// to end exactly as `task gen` invokes it: a real bundle path in, a real
// output file path out, into a nested directory that does not exist yet.
func TestRunFacets_RoundTripWritesOutputMatchingGolden(t *testing.T) {
	dir := t.TempDir()
	facetsOut := filepath.Join(dir, "nested", "ts", "facets.ts")

	if err := runFacets("testdata/facets.bundled.yaml", facetsOut); err != nil {
		t.Fatalf("runFacets: %v", err)
	}

	got, err := os.ReadFile(facetsOut) //nolint:gosec // G304: facetsOut is a path this same test built with filepath.Join under t.TempDir(), never external input.
	if err != nil {
		t.Fatalf("reading runFacets output: %v", err)
	}
	if want := readTestdata(t, "facets.ts.golden"); string(got) != string(want) {
		t.Errorf("runFacets output does not match testdata/facets.ts.golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRunFacets_MissingBundleFileReturnsError covers runFacets' read-error
// branch, the same branch main() reports to stderr and turns into a
// nonzero exit.
func TestRunFacets_MissingBundleFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	err := runFacets(filepath.Join(dir, "does-not-exist.yaml"), filepath.Join(dir, "facets.ts"))
	if err == nil {
		t.Fatal("runFacets: want an error for a missing bundle file, got nil")
	}
}
