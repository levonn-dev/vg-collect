package manifest_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// TestLoad_ValidTreeLoadsExactFields is the base case: every field the
// loader is responsible for assembling, checked against the literal values
// testdata/valid's files declare. testdata/valid uses a two-service stand-in
// roster (alpha, bravo) rather than the real service list - the loader does
// not know or care what the real services are called.
func TestLoad_ValidTreeLoadsExactFields(t *testing.T) {
	got, err := manifest.Load("testdata/valid")
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("Load: got a nil Model alongside a nil error")
	}

	wantGroup := manifest.AlertGroup{Name: "vgkeep", Folder: "vgkeep", Interval: "1m"}
	if got.Alerts.Group != wantGroup {
		t.Errorf("Alerts.Group = %+v, want %+v", got.Alerts.Group, wantGroup)
	}

	if got.Alerts.Datasource != "prometheus" {
		t.Errorf("Alerts.Datasource = %q, want %q", got.Alerts.Datasource, "prometheus")
	}

	wantPrefixes := []string{"kube_", "node_", "up"}
	if !reflect.DeepEqual(got.Alerts.ExternalMetricPrefixes, wantPrefixes) {
		t.Errorf("Alerts.ExternalMetricPrefixes = %v, want %v", got.Alerts.ExternalMetricPrefixes, wantPrefixes)
	}

	if len(got.Alerts.Templates) != 2 {
		t.Fatalf("len(Alerts.Templates) = %d, want 2", len(got.Alerts.Templates))
	}
	wantAvailability := manifest.Template{
		UID:          "vg-{service}-down",
		Title:        "{Service} service down",
		Expr:         `up{namespace="vgkeep", pod=~"{service}-.*"}`,
		Condition:    "lt 1",
		Instant:      true,
		For:          "5m",
		NoDataState:  "Alerting",
		ExecErrState: "Error",
		Severity:     "crit",
		Summary:      "{service} has no ready pods answering scrapes",
		Runbook:      "stack.md#service-down",
		PanelRef:     "{service}/Availability",
	}
	if got.Alerts.Templates["availability"] != wantAvailability {
		t.Errorf("Alerts.Templates[availability] = %+v, want %+v", got.Alerts.Templates["availability"], wantAvailability)
	}
	wantPDB := manifest.Template{
		UID:          "vg-{service}-pdb-exhausted",
		Title:        "{Service} disruption budget exhausted",
		Expr:         `min(kube_poddisruptionbudget_status_pod_disruptions_allowed{namespace="vgkeep", poddisruptionbudget=~"{service}.*"})`,
		Condition:    "lt 1",
		Instant:      true,
		For:          "1h",
		NoDataState:  "OK",
		ExecErrState: "Error",
		Severity:     "warn",
		Summary:      "{service} (or one of its datastores) cannot tolerate any pod disruption",
		Runbook:      "stack.md#pdb-exhausted",
		// PanelRef is deliberately unset here: golden.yaml's pdb_budget
		// template has no panel_ref key, proving an omitted optional
		// field decodes to its zero value rather than an error.
	}
	if got.Alerts.Templates["pdb_budget"] != wantPDB {
		t.Errorf("Alerts.Templates[pdb_budget] = %+v, want %+v", got.Alerts.Templates["pdb_budget"], wantPDB)
	}

	if len(got.Alerts.Cluster) != 1 {
		t.Fatalf("len(Alerts.Cluster) = %d, want 1", len(got.Alerts.Cluster))
	}
	wantClusterRule := manifest.Rule{
		UID:          "vg-pod-churn",
		Title:        "Pod restart churn or OOM kill",
		Expr:         `sum by (namespace, pod) (increase(kube_pod_container_status_restarts_total{namespace=~"vgkeep"}[15m])) > 3`,
		Condition:    "gt 0",
		Instant:      true,
		For:          "5m",
		NoDataState:  "OK",
		ExecErrState: "Error",
		Severity:     "warn",
		Summary:      "a pod is restart-churning or was OOM-killed",
		Runbook:      "stack.md#pod-restart-churn-or-oom-kill",
	}
	if got.Alerts.Cluster[0] != wantClusterRule {
		t.Errorf("Alerts.Cluster[0] = %+v, want %+v", got.Alerts.Cluster[0], wantClusterRule)
	}

	if len(got.Alerts.Services) != 2 {
		t.Fatalf("len(Alerts.Services) = %d, want 2", len(got.Alerts.Services))
	}

	alpha := got.Alerts.Services[0]
	if alpha.Service != "alpha" {
		t.Errorf("Alerts.Services[0].Service = %q, want %q", alpha.Service, "alpha")
	}
	wantAlphaGolden := map[string]manifest.Overrides{
		"availability": {},
		"pdb_budget":   {},
	}
	if !reflect.DeepEqual(alpha.Golden, wantAlphaGolden) {
		t.Errorf("Alerts.Services[alpha].Golden = %+v, want %+v", alpha.Golden, wantAlphaGolden)
	}
	if len(alpha.Alerts) != 1 {
		t.Fatalf("len(Alerts.Services[alpha].Alerts) = %d, want 1", len(alpha.Alerts))
	}
	wantAlphaRule := manifest.Rule{
		UID:          "vg-alpha-queue-backlog",
		Title:        "Alpha queue not draining",
		Expr:         "max(vg_alpha_queue_pending)",
		Condition:    "gt 25",
		Instant:      true,
		For:          "10m",
		NoDataState:  "OK",
		ExecErrState: "Error",
		Severity:     "warn",
		Summary:      "the alpha queue has stayed above 25 pending",
		Runbook:      "alpha.md#queue-backlog",
	}
	if alpha.Alerts[0] != wantAlphaRule {
		t.Errorf("Alerts.Services[alpha].Alerts[0] = %+v, want %+v", alpha.Alerts[0], wantAlphaRule)
	}

	bravo := got.Alerts.Services[1]
	if bravo.Service != "bravo" {
		t.Errorf("Alerts.Services[1].Service = %q, want %q", bravo.Service, "bravo")
	}
	wantBravoGolden := map[string]manifest.Overrides{
		"availability": {Severity: "warn"},
		"pdb_budget":   {},
	}
	if !reflect.DeepEqual(bravo.Golden, wantBravoGolden) {
		t.Errorf("Alerts.Services[bravo].Golden = %+v, want %+v", bravo.Golden, wantBravoGolden)
	}
	if len(bravo.Alerts) != 1 {
		t.Fatalf("len(Alerts.Services[bravo].Alerts) = %d, want 1", len(bravo.Alerts))
	}
	wantBravoRule := manifest.Rule{
		UID:          "vg-bravo-refresh-stalled",
		Title:        "Bravo nightly refresh has not completed",
		Expr:         "sum(increase(vg_bravo_refresh_seconds_count[26h]))",
		Condition:    "lt 1",
		Instant:      true,
		Range:        "26h",
		For:          "1h",
		NoDataState:  "Alerting",
		ExecErrState: "Error",
		Severity:     "warn",
		Summary:      "bravo has not finished a refresh in more than 26 hours",
		Runbook:      "bravo.md#refresh-missing",
		PanelRef:     "bravo/Refresh duration",
	}
	if bravo.Alerts[0] != wantBravoRule {
		t.Errorf("Alerts.Services[bravo].Alerts[0] = %+v, want %+v", bravo.Alerts[0], wantBravoRule)
	}

	wantRetired := []manifest.RetiredUID{
		{UID: "vg-bravo-old-thing", Date: "2026-01-15", Reason: "superseded by vg-bravo-refresh-stalled"},
	}
	if !reflect.DeepEqual(got.Alerts.Retired, wantRetired) {
		t.Errorf("Alerts.Retired = %+v, want %+v", got.Alerts.Retired, wantRetired)
	}

	if len(got.Dashboards.Golden) != 1 {
		t.Fatalf("len(Dashboards.Golden) = %d, want 1", len(got.Dashboards.Golden))
	}
	wantGridPos := manifest.GridPos{H: 8, W: 12, X: 0, Y: 0}
	if got.Dashboards.Golden[0].GridPos != wantGridPos {
		t.Errorf("Dashboards.Golden[0].GridPos = %+v, want %+v", got.Dashboards.Golden[0].GridPos, wantGridPos)
	}
	gotFragment := got.Dashboards.Golden[0].Fragment
	if !json.Valid(gotFragment) {
		t.Fatalf("Dashboards.Golden[0].Fragment is not valid json: %s", gotFragment)
	}
	if !strings.Contains(string(gotFragment), `"title": "{Service} Availability"`) {
		t.Errorf("Dashboards.Golden[0].Fragment = %s, want it to contain the availability panel title", gotFragment)
	}

	if len(got.Dashboards.Services) != 2 {
		t.Fatalf("len(Dashboards.Services) = %d, want 2", len(got.Dashboards.Services))
	}
	alphaDash := got.Dashboards.Services[0]
	if alphaDash.Service != "alpha" || alphaDash.UID != "vg-alpha" || alphaDash.Title != "Alpha" {
		t.Errorf("Dashboards.Services[0] = %+v, want Service=alpha UID=vg-alpha Title=Alpha", alphaDash)
	}
	if len(alphaDash.CustomPanels) != 1 || !json.Valid(alphaDash.CustomPanels[0]) {
		t.Fatalf("Dashboards.Services[0].CustomPanels = %s, want exactly one valid json fragment", alphaDash.CustomPanels)
	}
	if !strings.Contains(string(alphaDash.CustomPanels[0]), `"title": "Alpha queue depth"`) {
		t.Errorf("Dashboards.Services[0].CustomPanels[0] = %s, want it to contain the queue depth panel title", alphaDash.CustomPanels[0])
	}

	bravoDash := got.Dashboards.Services[1]
	if bravoDash.Service != "bravo" || bravoDash.UID != "vg-bravo" || bravoDash.Title != "Bravo" {
		t.Errorf("Dashboards.Services[1] = %+v, want Service=bravo UID=vg-bravo Title=Bravo", bravoDash)
	}
	if len(bravoDash.CustomPanels) != 1 || !json.Valid(bravoDash.CustomPanels[0]) {
		t.Fatalf("Dashboards.Services[1].CustomPanels = %s, want exactly one valid json fragment", bravoDash.CustomPanels)
	}
	if !strings.Contains(string(bravoDash.CustomPanels[0]), `"title": "Bravo refresh duration"`) {
		t.Errorf("Dashboards.Services[1].CustomPanels[0] = %s, want it to contain the refresh duration panel title", bravoDash.CustomPanels[0])
	}
}

// TestLoad_InvalidTreesFailWithOffendingPath tables every problem the
// loader must catch: a strict-decode rejection for each distinct manifest
// file kind (proving KnownFields(true) is wired for all of them, not just
// some), the two cross-file uid rules, and the roster/file-presence check.
// Each fixture tree is otherwise a complete, valid tree (every sibling file
// a full load would touch is present and unremarkable) with exactly one
// deliberate defect, so every case is checked two ways: the joined error
// contains the specific file path and detail a fix needs (not just that an
// error occurred), and joinedCount confirms Load found exactly that one
// problem rather than also tripping over an unrelated missing file left
// over from a sparser fixture.
func TestLoad_InvalidTreesFailWithOffendingPath(t *testing.T) {
	cases := []struct {
		name  string
		dir   string
		want  []string // every substring that must appear in the joined error
		avoid []string // substrings that must NOT appear (wrong-answer guards)
	}{
		{
			name: "unknown key in a golden.yaml template",
			dir:  "testdata/unknown-key-alerts-golden",
			want: []string{"alerts/golden.yaml", "field bogus_field not found"},
		},
		{
			name: "unknown key in cluster.yaml",
			dir:  "testdata/unknown-key-cluster",
			want: []string{"alerts/cluster.yaml", "field bogus_field not found"},
		},
		{
			name: "unknown key in a service alerts file",
			dir:  "testdata/unknown-key-service-alert",
			want: []string{"alerts/alpha.yaml", "field bogus_field not found"},
		},
		{
			name: "unknown key in retired.yaml",
			dir:  "testdata/unknown-key-retired",
			want: []string{"alerts/retired.yaml", "field bogus_field not found"},
		},
		{
			name: "unknown key in dashboards golden.yaml",
			dir:  "testdata/unknown-key-dashboards-golden",
			want: []string{"dashboards/golden.yaml", "field bogus_field not found"},
		},
		{
			name: "unknown key in a service dashboard file",
			dir:  "testdata/unknown-key-service-dashboard",
			want: []string{"dashboards/alpha.yaml", "field bogus_field not found"},
		},
		{
			name: "duplicate uid across two services' live rules",
			dir:  "testdata/duplicate-uid",
			want: []string{"vg-dup-thing", "alerts/alpha.yaml", "alerts/bravo.yaml"},
		},
		{
			name: "uid collision error cites the file Load actually read, not the decoded service field",
			dir:  "testdata/duplicate-uid-mismatched-service-field",
			want: []string{"vg-dup-thing", "alerts/alpha.yaml", "alerts/bravo.yaml"},
			// alpha.yaml's own service: field says "not-actually-alpha"; a
			// path rebuilt from that decoded field (the bug this fixture
			// pins) would cite a file that was never read.
			avoid: []string{"not-actually-alpha"},
		},
		{
			name: "retired uid collides with a live rule",
			dir:  "testdata/retired-collides-live",
			want: []string{"vg-alpha-thing", "alerts/retired.yaml", "alerts/alpha.yaml"},
		},
		{
			name: "golden override sets a forbidden field",
			dir:  "testdata/golden-override-forbidden-field",
			want: []string{"alerts/alpha.yaml", "field uid not found"},
		},
		{
			name: "service in the golden roster has no alerts file",
			dir:  "testdata/missing-alerts-file",
			want: []string{"alerts/bravo.yaml"},
		},
		{
			name: "service in the golden roster has no dashboard file",
			dir:  "testdata/missing-dashboard-file",
			want: []string{"dashboards/alpha.yaml"},
		},
		{
			name: "golden panel fragment is not valid json",
			dir:  "testdata/malformed-golden-panel-json",
			want: []string{"dashboards/golden.yaml"},
		},
		{
			name: "services roster in golden.yaml is not alphabetically ordered",
			dir:  "testdata/services-not-alphabetical",
			want: []string{"alerts/golden.yaml", `"alpha" comes after "bravo"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := manifest.Load(tc.dir)
			if err == nil {
				t.Fatalf("Load(%s): want an error, got nil (model: %+v)", tc.dir, got)
			}
			if got != nil {
				t.Errorf("Load(%s): want a nil Model alongside the error, got %+v", tc.dir, got)
			}

			msg := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("Load(%s) error = %q, want it to contain %q", tc.dir, msg, want)
				}
			}
			for _, avoid := range tc.avoid {
				if strings.Contains(msg, avoid) {
					t.Errorf("Load(%s) error = %q, want it to NOT contain %q", tc.dir, msg, avoid)
				}
			}

			if n := joinedCount(t, err); n != 1 {
				t.Errorf("Load(%s) joined %d problems, want exactly 1 (every fixture tree is otherwise complete and valid): %v", tc.dir, n, err)
			}
		})
	}
}

// joinedCount reports how many errors an errors.Join result actually
// combines, using the standard multi-error Unwrap() []error interface
// rather than counting newlines in the message (a single joined yaml.v3
// decode error can itself span multiple lines, so line-counting would
// overcount). It fails the test outright if err was not built by
// errors.Join - every error Load returns must be, per its own contract.
func joinedCount(t *testing.T, err error) int {
	t.Helper()
	u, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("error %v (%T) does not implement Unwrap() []error; Load must always return an errors.Join result", err, err)
	}
	return len(u.Unwrap())
}
