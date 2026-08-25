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

// update rewrites golden fixtures from actual output instead of
// comparing against them; review the diff before committing.
var update = flag.Bool("update", false, "write actual emitter output to the golden files instead of comparing")

// readTestdata loads a fixture or golden file from testdata/ (go test's
// working directory is always the package directory).
func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name) //nolint:gosec // G304: name is a literal from this package's own tests, not external input.
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return data
}

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

// TestGenerate_ByteIdempotent locks down determinism: nothing may depend
// on map order or wall-clock time (`task gen` twice relies on this).
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

	// re-parsing the same bytes must match too, not just repeated calls on one *domain.
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

// TestRun_RoundTripWritesBothOutputsMatchingGoldens exercises run() into a
// nested dir that doesn't exist yet, so MkdirAll actually creates it.
func TestRun_RoundTripWritesBothOutputsMatchingGoldens(t *testing.T) {
	dir := t.TempDir()
	goOut := filepath.Join(dir, "nested", "go", "tables_gen.go")
	tsOut := filepath.Join(dir, "nested", "ts", "domain.ts")

	if err := run("testdata/domain.yaml", goOut, "regionkit", tsOut); err != nil {
		t.Fatalf("run: %v", err)
	}

	gotGo, err := os.ReadFile(goOut) //nolint:gosec // G304: goOut is a path this test built under t.TempDir(), not external input.
	if err != nil {
		t.Fatalf("reading run's Go output: %v", err)
	}
	if want := readTestdata(t, "domain.go.golden"); string(gotGo) != string(want) {
		t.Errorf("run's Go output does not match testdata/domain.go.golden\n--- got ---\n%s\n--- want ---\n%s", gotGo, want)
	}

	gotTS, err := os.ReadFile(tsOut) //nolint:gosec // G304: tsOut is a path this test built under t.TempDir(), not external input.
	if err != nil {
		t.Fatalf("reading run's TS output: %v", err)
	}
	if want := readTestdata(t, "domain.ts.golden"); string(gotTS) != string(want) {
		t.Errorf("run's TS output does not match testdata/domain.ts.golden\n--- got ---\n%s\n--- want ---\n%s", gotTS, want)
	}
}

// TestRun_MissingYamlFileReturnsError covers run's read-error branch,
// the same one main() turns into a nonzero exit.
func TestRun_MissingYamlFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	err := run(filepath.Join(dir, "does-not-exist.yaml"), filepath.Join(dir, "out.go"), "regionkit", filepath.Join(dir, "out.ts"))
	if err == nil {
		t.Fatal("run: want an error for a missing yaml file, got nil")
	}
}

// TestParseDomain_UnknownKeyInReleaseRegionsRejected pins KnownFields(true)
// for release_regions: a typo'd field must fail the parse.
func TestParseDomain_UnknownKeyInReleaseRegionsRejected(t *testing.T) {
	if _, err := parseDomain(readTestdata(t, "domain_unknown_key_release_regions.yaml")); err == nil {
		t.Fatal("parseDomain: want an error for an unknown key in a release_regions row, got nil")
	}
}

// TestParseDomain_UnknownKeyInRegionsRejected mirrors the release_regions
// case above for a pre-existing section (regions).
func TestParseDomain_UnknownKeyInRegionsRejected(t *testing.T) {
	if _, err := parseDomain(readTestdata(t, "domain_unknown_key_regions.yaml")); err == nil {
		t.Fatal("parseDomain: want an error for an unknown key in a regions row, got nil")
	}
}

// --- facets: constraint values mirrored from a bundled OpenAPI document ----
//
// testdata/facets.bundled.yaml is one small fixture, readable in a
// screen, shared by every TestBuildFacets_* test below; each test's own
// name states the fixture entity and behavior it covers.

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
			// nested pure-enum-shaped property stays; only a top-level pure-enum schema is omitted.
			"status": map[string]interface{}{"type": "string"},
		},
	}
	if !reflect.DeepEqual(f.Schemas["Widget"], want) {
		t.Errorf("Schemas[Widget] = %#v, want %#v", f.Schemas["Widget"], want)
	}
}

// TestBuildFacets_PropertyLiterallyNamedEnumKeepsItsNameAndOwnFacets proves
// the strip is keyword-position-aware: a field named "enum" is not the
// enum keyword and survives; only its own schema's real enum keyword goes.
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
	// every OTHER schema still gets a facet; no curation beyond the pure-enum rule.
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

	// kind resolves to Kind (pure enum), so its own facet is empty ({}), not
	// absent; listWidgets keeps its entry because other inline params remain.
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

	// cursor/sort on listWidgets are $ref'd component params, not inline, so
	// neither shows up here (buildParameterFacets covers them separately).
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
// the facets walk and TS serialization must not depend on Go's map order.
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

// TestRunFacets_RoundTripWritesOutputMatchingGolden exercises runFacets
// into a nested dir that doesn't exist yet (forces writeFile's MkdirAll).
func TestRunFacets_RoundTripWritesOutputMatchingGolden(t *testing.T) {
	dir := t.TempDir()
	facetsOut := filepath.Join(dir, "nested", "ts", "facets.ts")

	if err := runFacets("testdata/facets.bundled.yaml", facetsOut); err != nil {
		t.Fatalf("runFacets: %v", err)
	}

	got, err := os.ReadFile(facetsOut) //nolint:gosec // G304: facetsOut is a path this test built under t.TempDir(), not external input.
	if err != nil {
		t.Fatalf("reading runFacets output: %v", err)
	}
	if want := readTestdata(t, "facets.ts.golden"); string(got) != string(want) {
		t.Errorf("runFacets output does not match testdata/facets.ts.golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRunFacets_MissingBundleFileReturnsError covers runFacets' read-error
// branch, the same one main() turns into a nonzero exit.
func TestRunFacets_MissingBundleFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	err := runFacets(filepath.Join(dir, "does-not-exist.yaml"), filepath.Join(dir, "facets.ts"))
	if err == nil {
		t.Fatal("runFacets: want an error for a missing bundle file, got nil")
	}
}
