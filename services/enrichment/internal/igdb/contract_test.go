package igdb

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/levonn-dev/vgkeep/libs/go/regionkit"
)

// TestReleaseRegionEnum_MatchesGeneratedTable is the anti-drift guard for
// api/domain.yaml's release_regions rows: oapi-codegen has no way to read
// domain.yaml, so api/enrichment.yaml (and its api/bff.yaml mirror, kept in
// sync by convention - see that file's own schema comment) hand-type the
// same ten names as a wire enum on PlatformRef.release_regions and
// ReleaseDate.region. This test reads api/enrichment.yaml off disk on every
// run - not a hardcoded snapshot of const identifiers, which would only
// ever catch a name removed from domain.yaml, never one added to the
// contract without a matching domain.yaml row - and compares its two enum
// lists against regionkit.ReleaseRegionNames in both directions, so a name
// added or dropped on EITHER side fails here instead of drifting silently.
func TestReleaseRegionEnum_MatchesGeneratedTable(t *testing.T) {
	generated := sortedReleaseRegionNames()
	contract := parseEnrichmentReleaseRegionEnums(t)

	assertSameRegionNames(t, "PlatformRef.release_regions", contract.releaseRegions, generated)
	assertSameRegionNames(t, "ReleaseDate.region", contract.releaseDate, generated)
}

// sortedReleaseRegionNames reads regionkit.ReleaseRegionNames (generated
// from api/domain.yaml's release_regions rows) as a sorted name list, so
// it compares against a contract enum regardless of either side's
// declaration order.
func sortedReleaseRegionNames() []string {
	names := make([]string, 0, len(regionkit.ReleaseRegionNames))
	for _, name := range regionkit.ReleaseRegionNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// releaseRegionEnums holds the two release-region enum lists this test
// reads straight out of api/enrichment.yaml, each sorted.
type releaseRegionEnums struct {
	releaseRegions []string
	releaseDate    []string
}

// enrichmentContractSchema is the minimal shape needed to reach
// components.schemas.PlatformRef.properties.release_regions.items.enum and
// components.schemas.ReleaseDate.properties.region.enum. This is a plain
// (non-strict) decode: every other key in the document - paths, the rest of
// the schemas - is ignored, since this test only cares about reading these
// two enum lists live, not about flagging unrelated contract typos (that is
// domaingen's job for domain.yaml, not this test's job for the OpenAPI doc).
type enrichmentContractSchema struct {
	Components struct {
		Schemas struct {
			PlatformRef struct {
				Properties struct {
					ReleaseRegions struct {
						Items struct {
							Enum []string `yaml:"enum"`
						} `yaml:"items"`
					} `yaml:"release_regions"`
				} `yaml:"properties"`
			} `yaml:"PlatformRef"`
			ReleaseDate struct {
				Properties struct {
					Region struct {
						Enum []string `yaml:"enum"`
					} `yaml:"region"`
				} `yaml:"properties"`
			} `yaml:"ReleaseDate"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

// parseEnrichmentReleaseRegionEnums reads and decodes api/enrichment.yaml
// from disk and returns its two release-region enum lists, sorted.
func parseEnrichmentReleaseRegionEnums(t *testing.T) releaseRegionEnums {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, "api", "enrichment.yaml"))
	if err != nil {
		t.Fatalf("reading api/enrichment.yaml: %v", err)
	}
	var doc enrichmentContractSchema
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing api/enrichment.yaml: %v", err)
	}
	releaseRegions := append([]string(nil), doc.Components.Schemas.PlatformRef.Properties.ReleaseRegions.Items.Enum...)
	releaseDate := append([]string(nil), doc.Components.Schemas.ReleaseDate.Properties.Region.Enum...)
	sort.Strings(releaseRegions)
	sort.Strings(releaseDate)
	return releaseRegionEnums{releaseRegions: releaseRegions, releaseDate: releaseDate}
}

// repoPath resolves a path relative to the repo root, anchored on this test
// file's own compiled-in source location (runtime.Caller) rather than the
// process working directory - correct regardless of the directory `go
// test`/`go vet` happen to be invoked from. This file lives at
// services/enrichment/internal/igdb/, four directories below the repo root.
func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not resolve this test file's own path")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	return filepath.Join(append([]string{root}, parts...)...)
}

// assertSameRegionNames fails with the precise set difference in whichever
// direction(s) it exists: a name the contract enum declares that the
// generated table does not (a domain.yaml release_regions row is missing),
// and a name the generated table has that the contract enum does not (an
// api/enrichment.yaml enum entry is missing), are reported independently
// via t.Errorf so both drift directions are visible in one run instead of
// the first one masking the other.
func assertSameRegionNames(t *testing.T, field string, contract, generated []string) {
	t.Helper()
	if only := setDiff(contract, generated); len(only) > 0 {
		t.Errorf("%s enum declares %v, which regionkit.ReleaseRegionNames does not generate (domain.yaml release_regions row missing)", field, only)
	}
	if only := setDiff(generated, contract); len(only) > 0 {
		t.Errorf("%s enum is missing %v, which regionkit.ReleaseRegionNames generates (api/enrichment.yaml enum entry missing)", field, only)
	}
}

// setDiff returns the sorted values present in a but not in b.
func setDiff(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, v := range b {
		inB[v] = true
	}
	var diff []string
	for _, v := range a {
		if !inB[v] {
			diff = append(diff, v)
		}
	}
	sort.Strings(diff)
	return diff
}
