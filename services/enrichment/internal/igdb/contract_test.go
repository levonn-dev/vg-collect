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

// The anti-drift guard for api/domain.yaml's release_regions rows:
// oapi-codegen can't read domain.yaml, so api/common.yaml hand-types
// the names once as the ReleaseRegion schema, which two wire sites
// $ref. Parses common.yaml off disk (catches additions too) and fails
// if either site stops $ref-ing and inlines its own enum.
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

// sortedReleaseRegionNames reads regionkit.ReleaseRegionNames as a
// sorted list, so comparison ignores either side's declaration order.
func sortedReleaseRegionNames() []string {
	names := make([]string, 0, len(regionkit.ReleaseRegionNames))
	for _, name := range regionkit.ReleaseRegionNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// releaseRegionContract holds what this test reads from
// api/common.yaml: the sorted ReleaseRegion enum and both $ref targets.
type releaseRegionContract struct {
	enum                 []string
	platformRefItemsRef  string
	releaseDateRegionRef string
}

// releaseRegionContractSchema is the minimal shape needed to reach
// ReleaseRegion.enum and its two wire sites' $ref (a plain, non-strict
// decode; other contract typos are domaingen's job, not this test's).
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

// parseReleaseRegionContract reads api/common.yaml and returns the
// sorted enum plus both $ref targets. An absent schema or empty enum
// is a hard failure, never a silent empty-vs-empty pass.
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

// repoPath resolves a path relative to the repo root, anchored on
// this file's compiled-in source location (runtime.Caller), correct
// regardless of `go test`'s working directory.
func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not resolve this test file's own path")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	return filepath.Join(append([]string{root}, parts...)...)
}

// assertSameRegionNames reports each direction's set difference via
// its own t.Errorf, so a name missing from domain.yaml and one missing
// from api/common.yaml are both visible in one run, not masking each other.
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
