package expand

import (
	"reflect"
	"testing"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

func TestDisplayName(t *testing.T) {
	cases := map[string]string{"bff": "BFF", "auth": "Auth", "enrichment": "Enrichment", "": ""}
	for in, want := range cases {
		if got := DisplayName(in); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubstitute(t *testing.T) {
	got := Substitute("{service} is down; {Service} disruption; {service}-pg", "bff")
	want := "bff is down; BFF disruption; bff-pg"
	if got != want {
		t.Errorf("Substitute = %q, want %q", got, want)
	}
}

// TestBlocks proves instantiation order (service, block, panel) and the
// per-instance substitution plus anchor pairing Blocks owns.
func TestBlocks(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Blocks: map[string]manifest.Block{
				"availability": {Panels: []string{
					`{"title": "Availability", "gridPos": {"y": 0}, "targets": [{"expr": "up{pod=~\"{service}-.*\"}"}]}`,
				}},
			},
			Services: []manifest.ServiceDash{
				{Service: "alpha", GoldenBlocks: map[string]int{"availability": 128}},
				{Service: "bravo", GoldenBlocks: map[string]int{"availability": 128}},
			},
		},
	}

	got := Blocks(m)
	want := []BlockPanel{
		{
			Service: "alpha", Block: "availability", AnchorY: 128,
			Fragment: `{"title": "Availability", "gridPos": {"y": 0}, "targets": [{"expr": "up{pod=~\"alpha-.*\"}"}]}`,
		},
		{
			Service: "bravo", Block: "availability", AnchorY: 128,
			Fragment: `{"title": "Availability", "gridPos": {"y": 0}, "targets": [{"expr": "up{pod=~\"bravo-.*\"}"}]}`,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Blocks() = %+v, want %+v", got, want)
	}
}

// TestBlocks_SkipsServicesWithNoGoldenBlocks proves a service with an
// empty/nil GoldenBlocks map contributes nothing; other services are unaffected.
func TestBlocks_SkipsServicesWithNoGoldenBlocks(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Blocks: map[string]manifest.Block{
				"availability": {Panels: []string{`{"title": "Availability", "gridPos": {"y": 0}}`}},
			},
			Services: []manifest.ServiceDash{
				{Service: "alpha", GoldenBlocks: map[string]int{"availability": 0}},
				{Service: "bravo"}, // no golden_blocks at all
			},
		},
	}

	got := Blocks(m)
	if len(got) != 1 {
		t.Fatalf("Blocks() returned %d instances, want 1 (bravo skipped): %+v", len(got), got)
	}
	if got[0].Service != "alpha" {
		t.Errorf("Blocks()[0].Service = %q, want %q", got[0].Service, "alpha")
	}
}
