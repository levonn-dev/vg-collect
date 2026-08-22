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

// releaseRegionSchemaRef is where both release-region wire sites point:
// the named vocabulary schema that is the one authored home of the enum.
const releaseRegionSchemaRef = "#/components/schemas/ReleaseRegion"

// TestReleaseRegionEnum_MatchesGeneratedTable is the anti-drift guard for
// api/domain.yaml's release_regions rows: oapi-codegen has no way to read
// domain.yaml, so api/common.yaml (the shared components file every service
// contract, enrichment's included, references by $ref) hand-types the same
// ten names once, as the ReleaseRegion vocabulary schema, and the two wire
// sites (PlatformRef.release_regions items, ReleaseDate.region) $ref it.
// This test reads api/common.yaml off disk on every run - not a hardcoded
// snapshot of const identifiers, which would only ever catch a name removed
// from domain.yaml, never one added to the contract without a matching
// domain.yaml row - and compares ReleaseRegion's enum against
// regionkit.ReleaseRegionNames in both directions, so a name added or
// dropped on EITHER side fails here instead of drifting silently. It also
// pins that both wire sites still $ref the vocabulary schema: a site
// quietly reverting to its own inline enum would otherwise fall outside
// this guard's watch.
func TestReleaseRegionEnum_MatchesGeneratedTable(t *testing.T) {
	generated := sortedReleaseRegionNames()
	contract := parseReleaseRegionContract(t)

	assertSameRegionNames(t, "ReleaseRegion", contract.enum, generated)

	if contract.platformRefItemsRef != releaseRegionSchemaRef {
		t.Errorf("PlatformRef.release_regions items $ref = %q, want %q (the site must reference the vocabulary schema this guard watches)",
			contract.platformRefItemsRef, releaseRegionSchemaRef)
	}
	if contract.releaseDateRegionRef != releaseRegionSchemaRef {
		t.Errorf("ReleaseDate.region $ref = %q, want %q (the site must reference the vocabulary schema this guard watches)",
			contract.releaseDateRegionRef, releaseRegionSchemaRef)
	}
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

// releaseRegionContract holds what this test reads straight out of
// api/common.yaml: the ReleaseRegion vocabulary enum (sorted) and the two
// wire sites' $ref targets.
type releaseRegionContract struct {
	enum                 []string
	platformRefItemsRef  string
	releaseDateRegionRef string
}

// releaseRegionContractSchema is the minimal shape needed to reach
// components.schemas.ReleaseRegion.enum and the $ref at each of its two
// wire sites. This is a plain (non-strict) decode: every other key in the
// document - paths, the rest of the schemas - is ignored, since this test
// only cares about reading these fields live, not about flagging unrelated
// contract typos (that is domaingen's job for domain.yaml, not this test's
// job for the OpenAPI doc).
type releaseRegionContractSchema struct {
	Components struct {
		Schemas struct {
			ReleaseRegion struct {
				Enum []string `yaml:"enum"`
			} `yaml:"ReleaseRegion"`
			PlatformRef struct {
				Properties struct {
					ReleaseRegions struct {
						Items struct {
							Ref string `yaml:"$ref"`
						} `yaml:"items"`
					} `yaml:"release_regions"`
				} `yaml:"properties"`
			} `yaml:"PlatformRef"`
			ReleaseDate struct {
				Properties struct {
					Region struct {
						Ref string `yaml:"$ref"`
					} `yaml:"region"`
				} `yaml:"properties"`
			} `yaml:"ReleaseDate"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

// parseReleaseRegionContract reads and decodes api/common.yaml from disk
// and returns the ReleaseRegion vocabulary enum (sorted) plus the two wire
// sites' $ref targets. An absent ReleaseRegion schema or an absent/empty
// enum key is a hard failure, never an empty comparison: the guard must
// fail loudly if its authored home moves rather than silently pass two
// empty lists against each other.
func parseReleaseRegionContract(t *testing.T) releaseRegionContract {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, "api", "common.yaml"))
	if err != nil {
		t.Fatalf("reading api/common.yaml: %v", err)
	}
	var doc releaseRegionContractSchema
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing api/common.yaml: %v", err)
	}
	enum := append([]string(nil), doc.Components.Schemas.ReleaseRegion.Enum...)
	if len(enum) == 0 {
		t.Fatalf("api/common.yaml components.schemas.ReleaseRegion is absent or carries no enum key; this guard watches that schema as the release-region enum's one authored home - if the vocabulary moved, re-ground this test")
	}
	sort.Strings(enum)
	return releaseRegionContract{
		enum:                 enum,
		platformRefItemsRef:  doc.Components.Schemas.PlatformRef.Properties.ReleaseRegions.Items.Ref,
		releaseDateRegionRef: doc.Components.Schemas.ReleaseDate.Properties.Region.Ref,
	}
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
// api/common.yaml enum entry is missing), are reported independently
// via t.Errorf so both drift directions are visible in one run instead of
// the first one masking the other.
func assertSameRegionNames(t *testing.T, field string, contract, generated []string) {
	t.Helper()
	if only := setDiff(contract, generated); len(only) > 0 {
		t.Errorf("%s enum declares %v, which regionkit.ReleaseRegionNames does not generate (domain.yaml release_regions row missing)", field, only)
	}
	if only := setDiff(generated, contract); len(only) > 0 {
		t.Errorf("%s enum is missing %v, which regionkit.ReleaseRegionNames generates (api/common.yaml enum entry missing)", field, only)
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
