package dashboards_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/dashboards"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// update rewrites the golden fixtures from actual output instead of
// comparing against them; review the diff before committing.
var update = flag.Bool("update", false, "write actual Assemble output to the golden files instead of comparing")

// thresholdStep mirrors one thresholds.steps entry; Value is a pointer
// since Grafana's base step uses a null value a bare float64 can't represent.
type thresholdStep struct {
	Color string   `json:"color"`
	Value *float64 `json:"value"`
}

func f64(v float64) *float64 { return &v }

// panelProbe decodes just enough of one panel to check assembly
// structurally, instead of matching substrings against indented JSON.
type panelProbe struct {
	ID          int               `json:"id"`
	Type        string            `json:"type"`
	Title       string            `json:"title"`
	Collapsed   bool              `json:"collapsed"`
	Panels      []json.RawMessage `json:"panels"`
	GridPos     manifest.GridPos  `json:"gridPos"`
	FieldConfig struct {
		Defaults struct {
			Thresholds *struct {
				Mode  string          `json:"mode"`
				Steps []thresholdStep `json:"steps"`
			} `json:"thresholds"`
			Custom *struct {
				ThresholdsStyle struct {
					Mode string `json:"mode"`
				} `json:"thresholdsStyle"`
			} `json:"custom"`
		} `json:"defaults"`
	} `json:"fieldConfig"`
	Targets []struct {
		Expr string `json:"expr"`
	} `json:"targets"`
}

type dashboardProbe struct {
	UID    string       `json:"uid"`
	Title  string       `json:"title"`
	Panels []panelProbe `json:"panels"`
}

func parseDashboard(t *testing.T, data []byte) dashboardProbe {
	t.Helper()
	var d dashboardProbe
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("parsing dashboard json: %v\n%s", err, data)
	}
	return d
}

func findPanel(t *testing.T, d dashboardProbe, title string) panelProbe {
	t.Helper()
	for _, p := range d.Panels {
		if p.Title == title {
			return p
		}
	}
	titles := make([]string, len(d.Panels))
	for i, p := range d.Panels {
		titles[i] = p.Title
	}
	t.Fatalf("no panel titled %q; have %v", title, titles)
	return panelProbe{}
}

// findRawPanel returns raw JSON bytes for the panel titled title, the
// only way to check key order (unmarshaling into a struct loses it).
func findRawPanel(t *testing.T, data []byte, title string) json.RawMessage {
	t.Helper()
	var doc struct {
		Panels []json.RawMessage `json:"panels"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing dashboard json: %v\n%s", err, data)
	}
	for _, raw := range doc.Panels {
		var probe struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("parsing panel json: %v\n%s", err, raw)
		}
		if probe.Title == title {
			return raw
		}
	}
	t.Fatalf("no panel titled %q in dashboard json %s", title, data)
	return nil
}

// jsonObjectKeyOrder walks raw's top-level JSON object token stream and
// returns its keys in emitted order.
func jsonObjectKeyOrder(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("jsonObjectKeyOrder: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		t.Fatalf("jsonObjectKeyOrder: expected a json object, got %v", tok)
	}
	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			t.Fatalf("jsonObjectKeyOrder: %v", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			t.Fatalf("jsonObjectKeyOrder: expected a string object key, got %v", keyTok)
		}
		keys = append(keys, key)
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			t.Fatalf("jsonObjectKeyOrder: %v", err)
		}
	}
	return keys
}

// fixtureModel builds a small two-service (alpha, bravo) manifest
// directly (not via manifest.Load, to avoid re-testing the loader). The
// "shared" golden block has 3 panels at anchor 0 for both services; see
// TestAssemble_TwoServiceFixture's own assertions for what each panel
// and alert proves.
func fixtureModel() *manifest.Model {
	blocks := map[string]manifest.Block{
		"shared": {Panels: []string{
			`{"title": "Availability", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "short"}, "overrides": []}, "targets": [{"refId": "A", "expr": "up{namespace=\"vgkeep\", pod=~\"{service}-.*\"}"}]}`,
			`{"title": "{Service} request rate", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "reqps"}, "overrides": []}, "targets": [{"refId": "A", "expr": "sum(rate(vg_{service}_requests_total[5m]))"}]}`,
			`{"title": "Goroutines", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "short"}, "overrides": []}, "targets": [{"refId": "A", "expr": "go_goroutine_count"}]}`,
		}},
	}

	dashServices := []manifest.ServiceDash{
		{
			Service:      "alpha",
			UID:          "vg-alpha",
			Title:        "Alpha",
			GoldenBlocks: map[string]int{"shared": 0},
			CustomPanels: []json.RawMessage{
				json.RawMessage(`{"title": "Alpha queue depth", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 32}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "short", "thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": null}]}}, "overrides": []}, "targets": [{"refId": "A", "expr": "vg_alpha_queue_pending"}]}`),
			},
		},
		{
			Service:      "bravo",
			UID:          "vg-bravo",
			Title:        "Bravo",
			GoldenBlocks: map[string]int{"shared": 0},
			CustomPanels: []json.RawMessage{
				json.RawMessage(`{"title": "Bravo refresh duration", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 32}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "s"}, "overrides": []}, "targets": [{"refId": "A", "expr": "vg_bravo_refresh_seconds"}]}`),
				json.RawMessage(`{"title": "Bravo error rate", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 32}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "percentunit"}, "overrides": []}, "targets": [{"refId": "A", "expr": "vg_bravo_errors_ratio"}]}`),
			},
		},
	}

	templates := map[string]manifest.Template{
		"availability": {
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
		},
	}

	alertServices := []manifest.ServiceAlerts{
		{
			Service: "alpha",
			Golden:  map[string]manifest.Overrides{"availability": {}},
			Alerts: []manifest.Rule{
				{
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
					PanelRef:     "alpha/Alpha queue depth",
				},
				{
					UID:          "vg-alpha-availability-extra",
					Title:        "Alpha availability secondary check",
					Expr:         `up{namespace="vgkeep", pod=~"alpha-.*"}`,
					Condition:    "lt 2",
					Instant:      true,
					For:          "5m",
					NoDataState:  "Alerting",
					ExecErrState: "Error",
					Severity:     "warn",
					Summary:      "secondary alpha availability check",
					Runbook:      "alpha.md#availability-extra",
					PanelRef:     "alpha/Availability",
				},
			},
		},
		{
			Service: "bravo",
			Golden:  map[string]manifest.Overrides{"availability": {Severity: "warn"}},
			Alerts: []manifest.Rule{
				{
					UID:          "vg-bravo-request-rate-page",
					Title:        "Bravo request rate paging check",
					Expr:         "sum(rate(vg_bravo_requests_total[5m]))",
					Condition:    "gt 100",
					Instant:      true,
					For:          "5m",
					NoDataState:  "Alerting",
					ExecErrState: "Error",
					Severity:     "page",
					Summary:      "bravo request rate is paging on-call",
					Runbook:      "bravo.md#request-rate-page",
					PanelRef:     "bravo/Bravo request rate",
				},
			},
		},
	}

	return &manifest.Model{
		Alerts: manifest.AlertTree{
			Group:      manifest.AlertGroup{Name: "vgkeep", Folder: "vgkeep", Interval: "1m"},
			Datasource: "prometheus",
			Templates:  templates,
			Services:   alertServices,
		},
		Dashboards: manifest.DashTree{
			Blocks:   blocks,
			Services: dashServices,
		},
	}
}

// TestAssemble_TwoServiceFixture is the base case: checks id assignment,
// substitution, threshold projection, and determinism before comparing
// against golden files.
func TestAssemble_TwoServiceFixture(t *testing.T) {
	m := fixtureModel()

	files1, idx1, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error: %v", err)
	}
	if len(files1) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files1))
	}

	files2, idx2, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble (second run): unexpected error: %v", err)
	}
	for name, b1 := range files1 {
		b2, ok := files2[name]
		if !ok || string(b1) != string(b2) {
			t.Errorf("Assemble is not byte-idempotent for %q across repeated calls on the same model", name)
		}
	}
	if !reflect.DeepEqual(idx1, idx2) {
		t.Errorf("PanelIndex is not idempotent across repeated calls: run1=%+v run2=%+v", idx1, idx2)
	}

	wantIdx := dashboards.PanelIndex{
		"alpha": {"Availability": 1, "Alpha request rate": 2, "Goroutines": 3, "Alpha queue depth": 4},
		"bravo": {"Availability": 1, "Bravo request rate": 2, "Goroutines": 3, "Bravo refresh duration": 4, "Bravo error rate": 5},
	}
	if !reflect.DeepEqual(idx1, wantIdx) {
		t.Errorf("PanelIndex = %+v, want %+v", idx1, wantIdx)
	}

	alpha := parseDashboard(t, files1["alpha.json"])
	bravo := parseDashboard(t, files1["bravo.json"])

	if alpha.UID != "vg-alpha" || alpha.Title != "Alpha" {
		t.Errorf("alpha dashboard uid/title = %q/%q, want vg-alpha/Alpha", alpha.UID, alpha.Title)
	}
	if bravo.UID != "vg-bravo" || bravo.Title != "Bravo" {
		t.Errorf("bravo dashboard uid/title = %q/%q, want vg-bravo/Bravo", bravo.UID, bravo.Title)
	}
	if len(alpha.Panels) != 4 || len(bravo.Panels) != 5 {
		t.Fatalf("len(panels) = %d/%d, want 4/5", len(alpha.Panels), len(bravo.Panels))
	}

	// deterministic ids: golden block ids fixed by position; custom ids
	// continue in manifest order, not alphabetical ("Bravo error rate" still lands on id 5, not 4).
	for _, tc := range []struct {
		d     dashboardProbe
		title string
		id    int
	}{
		{alpha, "Availability", 1}, {alpha, "Alpha request rate", 2}, {alpha, "Goroutines", 3}, {alpha, "Alpha queue depth", 4},
		{bravo, "Availability", 1}, {bravo, "Bravo request rate", 2}, {bravo, "Goroutines", 3}, {bravo, "Bravo refresh duration", 4}, {bravo, "Bravo error rate", 5},
	} {
		if got := findPanel(t, tc.d, tc.title).ID; got != tc.id {
			t.Errorf("panel %q id = %d, want %d", tc.title, got, tc.id)
		}
	}

	// {service}/{Service} substitution: golden panel title and an expr selector.
	if got := findPanel(t, alpha, "Alpha request rate").Title; got != "Alpha request rate" {
		t.Errorf("golden panel title substitution failed for alpha: %q", got)
	}
	if got := findPanel(t, bravo, "Bravo request rate").Title; got != "Bravo request rate" {
		t.Errorf("golden panel title substitution failed for bravo: %q", got)
	}
	if got := findPanel(t, alpha, "Availability").Targets[0].Expr; got != `up{namespace="vgkeep", pod=~"alpha-.*"}` {
		t.Errorf("alpha Availability expr = %q, want the pod selector substituted for alpha", got)
	}
	if got := findPanel(t, bravo, "Availability").Targets[0].Expr; got != `up{namespace="vgkeep", pod=~"bravo-.*"}` {
		t.Errorf("bravo Availability expr = %q, want the pod selector substituted for bravo", got)
	}

	// alpha's Availability panel gets two projected steps (golden before
	// custom) atop the base step; bravo's override (crit -> warn) proves
	// the override, not the template default, drives step color.
	alphaAvail := findPanel(t, alpha, "Availability")
	if alphaAvail.FieldConfig.Defaults.Thresholds == nil {
		t.Fatal("alpha Availability: thresholds not injected")
	}
	if alphaAvail.FieldConfig.Defaults.Thresholds.Mode != "absolute" {
		t.Errorf("alpha Availability thresholds.mode = %q, want \"absolute\"", alphaAvail.FieldConfig.Defaults.Thresholds.Mode)
	}
	if want := []thresholdStep{{Color: "green", Value: nil}, {Color: "red", Value: f64(1)}, {Color: "orange", Value: f64(2)}}; !reflect.DeepEqual(alphaAvail.FieldConfig.Defaults.Thresholds.Steps, want) {
		t.Errorf("alpha Availability thresholds.steps = %+v, want %+v (base step first, golden step next, second alert's step appended after)", alphaAvail.FieldConfig.Defaults.Thresholds.Steps, want)
	}
	if alphaAvail.FieldConfig.Defaults.Custom == nil || alphaAvail.FieldConfig.Defaults.Custom.ThresholdsStyle.Mode != "line" {
		t.Errorf("alpha Availability fieldConfig.defaults.custom.thresholdsStyle.mode = %+v, want \"line\"", alphaAvail.FieldConfig.Defaults.Custom)
	}

	bravoAvail := findPanel(t, bravo, "Availability")
	if bravoAvail.FieldConfig.Defaults.Thresholds == nil {
		t.Fatal("bravo Availability: thresholds not injected")
	}
	if want := []thresholdStep{{Color: "green", Value: nil}, {Color: "orange", Value: f64(1)}}; !reflect.DeepEqual(bravoAvail.FieldConfig.Defaults.Thresholds.Steps, want) {
		t.Errorf("bravo Availability thresholds.steps = %+v, want %+v (base step first, then severity override crit->warn)", bravoAvail.FieldConfig.Defaults.Thresholds.Steps, want)
	}

	// page severity maps to red too (same as crit), proving
	// thresholdColor's page->red mapping is wired, not just crit's.
	bravoRate := findPanel(t, bravo, "Bravo request rate")
	if bravoRate.FieldConfig.Defaults.Thresholds == nil {
		t.Fatal("bravo request rate: thresholds not injected")
	}
	if want := []thresholdStep{{Color: "green", Value: nil}, {Color: "red", Value: f64(100)}}; !reflect.DeepEqual(bravoRate.FieldConfig.Defaults.Thresholds.Steps, want) {
		t.Errorf("bravo request rate thresholds.steps = %+v, want %+v (base step first, then severity page -> red)", bravoRate.FieldConfig.Defaults.Thresholds.Steps, want)
	}

	// append case: alpha's queue-depth panel already has a hand-authored
	// green/null step; injection appends onto it rather than adding a second base step.
	alphaQueue := findPanel(t, alpha, "Alpha queue depth")
	if alphaQueue.FieldConfig.Defaults.Thresholds == nil {
		t.Fatal("alpha queue depth: thresholds missing entirely")
	}
	wantQueueSteps := []thresholdStep{{Color: "green", Value: nil}, {Color: "orange", Value: f64(25)}}
	if !reflect.DeepEqual(alphaQueue.FieldConfig.Defaults.Thresholds.Steps, wantQueueSteps) {
		t.Errorf("alpha queue depth thresholds.steps = %+v, want %+v (pre-existing step kept, new step appended)", alphaQueue.FieldConfig.Defaults.Thresholds.Steps, wantQueueSteps)
	}

	// untouched control: bravo's panel with no alert must gain no
	// thresholds/custom field, proving injection only touches panel_ref'd panels.
	bravoRefresh := findPanel(t, bravo, "Bravo refresh duration")
	if bravoRefresh.FieldConfig.Defaults.Thresholds != nil || bravoRefresh.FieldConfig.Defaults.Custom != nil {
		t.Errorf("bravo refresh duration panel gained thresholds/custom with no alert pointing at it: %+v", bravoRefresh.FieldConfig.Defaults)
	}

	if t.Failed() {
		t.Fatal("earlier assertions failed; not comparing or writing golden files against a known-bad run")
	}

	for _, svc := range []string{"alpha", "bravo"} {
		name := svc + ".json.golden"
		got := files1[svc+".json"]
		if *update {
			if err := os.WriteFile("testdata/"+name, got, 0o600); err != nil {
				t.Fatalf("writing testdata/%s: %v", name, err)
			}
			continue
		}
		want, err := os.ReadFile("testdata/" + name) //nolint:gosec // G304: name is built from a literal []string{"alpha","bravo"} in this same loop, not external input.
		if err != nil {
			t.Fatalf("reading testdata/%s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("Assemble(%s) output does not match testdata/%s\n--- got ---\n%s\n--- want ---\n%s", svc, name, got, want)
		}
	}
	if *update {
		t.Skip("golden files updated; re-run without -update to verify")
	}
}

// TestAssemble_AlertErrors tables every way an alert's panel_ref or
// threshold fields can fail, each isolated to one change from fixtureModel.
func TestAssemble_AlertErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*manifest.Model)
		want   []string
	}{
		{
			name: "panel_ref names a title missing from that service's dashboard",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].PanelRef = "alpha/Does not exist"
			},
			want: []string{"alpha", "Does not exist"},
		},
		{
			name: "panel_ref has no slash separating service from title",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].PanelRef = "alpha-no-slash"
			},
			want: []string{"vg-alpha-queue-backlog", "malformed panel_ref", "alpha-no-slash"},
		},
		{
			name: "panel_ref names a service with no dashboard at all",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].PanelRef = "charlie/Availability"
			},
			want: []string{"vg-alpha-queue-backlog", "charlie", "has no dashboard"},
		},
		{
			name: "condition is not two whitespace-separated tokens",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].Condition = "not-a-number"
			},
			want: []string{"vg-alpha-queue-backlog"},
		},
		{
			name: "condition's second token does not parse as a number",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].Condition = "gt abc"
			},
			// "abc" confirms this hits ParseFloat's failure branch, not the wrong-token-count guard above.
			want: []string{"vg-alpha-queue-backlog", "abc"},
		},
		{
			name: "severity is none of warn, crit, or page",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].Severity = "info"
			},
			want: []string{"vg-alpha-queue-backlog"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureModel()
			tc.mutate(m)

			_, _, err := dashboards.Assemble(m)
			if err == nil {
				t.Fatal("Assemble: want an error, got nil")
			}
			msg := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("Assemble error = %q, want it to contain %q", msg, want)
				}
			}
		})
	}
}

// TestAssemble_PanelBuildErrors covers the panel-build phase: custom
// panels are never json-validated at load time (unlike golden
// fragments), so a syntax error there is real input Assemble must handle.
func TestAssemble_PanelBuildErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*manifest.Model)
		want   string
	}{
		{
			name: "custom panel fragment is not valid json",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{not valid json`)
			},
			want: "alpha",
		},
		{
			name: "golden panel fragment is not valid json",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Blocks["shared"].Panels[0] = `{not valid json`
			},
			want: `golden block "shared" panel 0`,
		},
		{
			name: "panel fragment has no title field",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Blocks["shared"].Panels[0] = `{"gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}}`
			},
			want: "no title field",
		},
		{
			name: "a custom panel's title collides with a golden panel's title",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{"title": "Goroutines", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 32}, "targets": [{"expr": "vg_alpha_queue_pending"}]}`)
			},
			want: `duplicate panel title "Goroutines"`,
		},
		{
			name: "two golden panels share a title",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Blocks["shared"].Panels[1] = `{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0}, "targets": [{"expr": "up"}]}`
			},
			want: `duplicate panel title "Availability"`,
		},
		{
			name: "two custom panels in the same service share a title",
			mutate: func(m *manifest.Model) {
				// bravo's second custom panel ("Bravo error rate") collides with its first.
				m.Dashboards.Services[1].CustomPanels[1] = json.RawMessage(`{"title": "Bravo refresh duration", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 32}, "targets": [{"expr": "vg_bravo_errors_ratio"}]}`)
			},
			want: `duplicate panel title "Bravo refresh duration"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureModel()
			tc.mutate(m)

			_, _, err := dashboards.Assemble(m)
			if err == nil {
				t.Fatal("Assemble: want an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Assemble error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestAssemble_EmptyCustomPanels proves a service with no custom panels
// comes out as exactly the golden block; fixtureModel's services don't
// exercise this directly.
func TestAssemble_EmptyCustomPanels(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Blocks: map[string]manifest.Block{
				"shared": {Panels: []string{
					`{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "up{namespace=\"vgkeep\", pod=~\"{service}-.*\"}"}]}`,
					`{"title": "{Service} request rate", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0}, "targets": [{"expr": "sum(rate(vg_{service}_requests_total[5m]))"}]}`,
				}},
			},
			Services: []manifest.ServiceDash{
				{
					Service:      "charlie",
					UID:          "vg-charlie",
					Title:        "Charlie",
					GoldenBlocks: map[string]int{"shared": 0},
					// CustomPanels left nil, the zero value a missing custom_panels: decodes to.
				},
			},
		},
	}

	files, idx, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error: %v", err)
	}

	d := parseDashboard(t, files["charlie.json"])
	if len(d.Panels) != 2 {
		t.Fatalf("len(panels) = %d, want 2 (the golden block only, no custom panels)", len(d.Panels))
	}

	wantIdx := dashboards.PanelIndex{
		"charlie": {"Availability": 1, "Charlie request rate": 2},
	}
	if !reflect.DeepEqual(idx, wantIdx) {
		t.Errorf("PanelIndex = %+v, want %+v", idx, wantIdx)
	}
}

// TestAssemble_ServiceDisplayNameAcronym pins the acronym exception:
// "bff" renders "BFF" in {Service} form, not "Bff"; "auth" is the
// general-case control ("Auth"). Also checks {service} lowercase substitution is unaffected.
func TestAssemble_ServiceDisplayNameAcronym(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Blocks: map[string]manifest.Block{
				"shared": {Panels: []string{
					`{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "up{namespace=\"vgkeep\", pod=~\"{service}-.*\"}"}]}`,
					`{"title": "{Service} request rate", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0}, "targets": [{"expr": "sum(rate(vg_{service}_requests_total[5m]))"}]}`,
				}},
			},
			Services: []manifest.ServiceDash{
				{Service: "auth", UID: "vg-auth", Title: "Auth", GoldenBlocks: map[string]int{"shared": 0}},
				{Service: "bff", UID: "vg-bff", Title: "BFF", GoldenBlocks: map[string]int{"shared": 0}},
			},
		},
	}

	files, _, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error: %v", err)
	}

	auth := parseDashboard(t, files["auth.json"])
	bff := parseDashboard(t, files["bff.json"])

	if got := findPanel(t, auth, "Auth request rate").Title; got != "Auth request rate" {
		t.Errorf("golden panel title substitution failed for auth: %q", got)
	}
	if got := findPanel(t, bff, "BFF request rate").Title; got != "BFF request rate" {
		t.Errorf("golden panel title substitution failed for bff: got %q, want \"BFF request rate\" (not \"Bff request rate\")", got)
	}
	if got := findPanel(t, auth, "Availability").Targets[0].Expr; got != `up{namespace="vgkeep", pod=~"auth-.*"}` {
		t.Errorf("auth Availability expr = %q, want the pod selector substituted for auth", got)
	}
	if got := findPanel(t, bff, "Availability").Targets[0].Expr; got != `up{namespace="vgkeep", pod=~"bff-.*"}` {
		t.Errorf("bff Availability expr = %q, want the pod selector substituted for lowercase bff, not Bff/BFF", got)
	}
}

// TestAssemble_BlockPanelAnchoredAbsoluteY proves gridPos.y is the
// block-relative offset (3) plus the block's anchor (50) = 53; x/w/h pass through untouched.
func TestAssemble_BlockPanelAnchoredAbsoluteY(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Blocks: map[string]manifest.Block{
				"availability": {Panels: []string{
					`{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 3}, "targets": [{"expr": "up"}]}`,
				}},
			},
			Services: []manifest.ServiceDash{
				{
					Service:      "charlie",
					UID:          "vg-charlie",
					Title:        "Charlie",
					GoldenBlocks: map[string]int{"availability": 50},
				},
			},
		},
	}

	files, _, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error: %v", err)
	}

	p := findPanel(t, parseDashboard(t, files["charlie.json"]), "Availability")
	wantGridPos := manifest.GridPos{H: 8, W: 12, X: 0, Y: 53}
	if p.GridPos != wantGridPos {
		t.Errorf("Availability gridPos = %+v, want %+v (anchor 50 + block-relative offset 3; x/w/h untouched)", p.GridPos, wantGridPos)
	}
}

// TestAssemble_CombinedOrderingByYThenX proves panels sort by
// (gridPos.y, gridPos.x), not manifest order: "Custom low" (declared
// first) sorts after "Custom top" by y, and ties with the block panel's y, broken by x.
func TestAssemble_CombinedOrderingByYThenX(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Blocks: map[string]manifest.Block{
				"availability": {Panels: []string{
					`{"title": "Availability", "gridPos": {"h": 4, "w": 12, "x": 12, "y": 0}, "targets": [{"expr": "up"}]}`,
				}},
			},
			Services: []manifest.ServiceDash{
				{
					Service:      "charlie",
					UID:          "vg-charlie",
					Title:        "Charlie",
					GoldenBlocks: map[string]int{"availability": 20},
					CustomPanels: []json.RawMessage{
						json.RawMessage(`{"title": "Custom low", "gridPos": {"h": 4, "w": 12, "x": 0, "y": 20}, "targets": [{"expr": "vg_low"}]}`),
						json.RawMessage(`{"title": "Custom top", "gridPos": {"h": 4, "w": 12, "x": 0, "y": 4}, "targets": [{"expr": "vg_top"}]}`),
					},
				},
			},
		},
	}

	files, idx, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error: %v", err)
	}

	d := parseDashboard(t, files["charlie.json"])
	if len(d.Panels) != 3 {
		t.Fatalf("len(panels) = %d, want 3", len(d.Panels))
	}
	wantOrder := []string{"Custom top", "Custom low", "Availability"}
	for i, want := range wantOrder {
		if got := d.Panels[i].Title; got != want {
			t.Errorf("panels[%d].Title = %q, want %q (want order %v, got panels %+v)", i, got, want, wantOrder, d.Panels)
		}
	}

	wantIdx := dashboards.PanelIndex{"charlie": {"Custom top": 1, "Custom low": 2, "Availability": 3}}
	if !reflect.DeepEqual(idx, wantIdx) {
		t.Errorf("PanelIndex = %+v, want %+v", idx, wantIdx)
	}
}

// TestAssemble_OverlapErrors proves overlapping panels fail Assemble
// with both titles named in the error (internal/grid.Check's wiring).
func TestAssemble_OverlapErrors(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Services: []manifest.ServiceDash{
				{
					Service: "charlie",
					UID:     "vg-charlie",
					Title:   "Charlie",
					CustomPanels: []json.RawMessage{
						json.RawMessage(`{"title": "Panel A", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "vg_a"}]}`),
						json.RawMessage(`{"title": "Panel B", "gridPos": {"h": 8, "w": 12, "x": 6, "y": 4}, "targets": [{"expr": "vg_b"}]}`),
					},
				},
			},
		},
	}

	_, _, err := dashboards.Assemble(m)
	if err == nil {
		t.Fatal("Assemble: want an error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"charlie", "overlap", "Panel A", "Panel B"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Assemble error = %q, want it to contain %q", msg, want)
		}
	}
}

// TestAssemble_SectionRowEmittedAtAnchor proves a section emits a row
// panel at its literal anchor, with Grafana's alphabetical row key
// order (collapsed, gridPos, id, panels, title, type).
func TestAssemble_SectionRowEmittedAtAnchor(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Services: []manifest.ServiceDash{
				{
					Service: "charlie",
					UID:     "vg-charlie",
					Title:   "Charlie",
					// the section sits directly under the custom panel (ends at
					// y8) so the row itself is stable - a floating row would fail the stability check too.
					Sections: map[string]int{"Ops": 8},
					CustomPanels: []json.RawMessage{
						json.RawMessage(`{"title": "Custom", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "vg_custom"}]}`),
					},
				},
			},
		},
	}

	files, _, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error: %v", err)
	}

	d := parseDashboard(t, files["charlie.json"])
	row := findPanel(t, d, "Ops")
	if row.Type != "row" {
		t.Errorf("row Type = %q, want \"row\"", row.Type)
	}
	if row.Collapsed {
		t.Error("row Collapsed = true, want false")
	}
	if len(row.Panels) != 0 {
		t.Errorf("row Panels = %v, want empty", row.Panels)
	}
	wantGridPos := manifest.GridPos{H: 1, W: 24, X: 0, Y: 8}
	if row.GridPos != wantGridPos {
		t.Errorf("row GridPos = %+v, want %+v", row.GridPos, wantGridPos)
	}

	// this key-order check plus row.Panels together prove "panels" is a
	// literal empty array present in the JSON, not merely absent.
	raw := findRawPanel(t, files["charlie.json"], "Ops")
	wantKeys := []string{"collapsed", "gridPos", "id", "panels", "title", "type"}
	if got := jsonObjectKeyOrder(t, raw); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("row json key order = %v, want %v", got, wantKeys)
	}
}

// TestAssemble_SectionRowIDInSequence proves a row's id follows the
// same sequence as every other panel, and its title never enters the panel index.
func TestAssemble_SectionRowIDInSequence(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Services: []manifest.ServiceDash{
				{
					Service:  "charlie",
					UID:      "vg-charlie",
					Title:    "Charlie",
					Sections: map[string]int{"Ops": 8},
					CustomPanels: []json.RawMessage{
						json.RawMessage(`{"title": "Above", "gridPos": {"h": 8, "w": 24, "x": 0, "y": 0}, "targets": [{"expr": "vg_above"}]}`),
						json.RawMessage(`{"title": "Below", "gridPos": {"h": 8, "w": 24, "x": 0, "y": 9}, "targets": [{"expr": "vg_below"}]}`),
					},
				},
			},
		},
	}

	files, idx, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error: %v", err)
	}

	d := parseDashboard(t, files["charlie.json"])
	for _, tc := range []struct {
		title string
		id    int
	}{
		{"Above", 1}, {"Ops", 2}, {"Below", 3},
	} {
		if got := findPanel(t, d, tc.title).ID; got != tc.id {
			t.Errorf("panel %q id = %d, want %d", tc.title, got, tc.id)
		}
	}

	wantIdx := dashboards.PanelIndex{"charlie": {"Above": 1, "Below": 3}}
	if !reflect.DeepEqual(idx, wantIdx) {
		t.Errorf("PanelIndex = %+v, want %+v (the row's title must not appear)", idx, wantIdx)
	}
}

// TestAssemble_SectionStabilityViolationFailsAssemble proves a sections-
// opted-in service is checked for compaction stability: a floatable panel fails, named in the error.
func TestAssemble_SectionStabilityViolationFailsAssemble(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Services: []manifest.ServiceDash{
				{
					Service:  "charlie",
					UID:      "vg-charlie",
					Title:    "Charlie",
					Sections: map[string]int{"Ops": 0},
					CustomPanels: []json.RawMessage{
						json.RawMessage(`{"title": "Left", "gridPos": {"h": 8, "w": 8, "x": 0, "y": 1}, "targets": [{"expr": "vg_left"}]}`),
						json.RawMessage(`{"title": "Right", "gridPos": {"h": 8, "w": 8, "x": 16, "y": 1}, "targets": [{"expr": "vg_right"}]}`),
						json.RawMessage(`{"title": "Floater", "gridPos": {"h": 8, "w": 8, "x": 8, "y": 9}, "targets": [{"expr": "vg_floater"}]}`),
					},
				},
			},
		},
	}

	_, _, err := dashboards.Assemble(m)
	if err == nil {
		t.Fatal("Assemble: want an error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"charlie", "float", "Floater"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Assemble error = %q, want it to contain %q", msg, want)
		}
	}
}

// TestAssemble_NoSectionsKeepsLenientOverlapOnlyGate proves a no-sections
// service skips the stability check, the transition behavior pre-migration dashboards depend on.
func TestAssemble_NoSectionsKeepsLenientOverlapOnlyGate(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Services: []manifest.ServiceDash{
				{
					Service: "charlie",
					UID:     "vg-charlie",
					Title:   "Charlie",
					// No Sections entry: the pre-sections dashboards this gate must not break.
					CustomPanels: []json.RawMessage{
						json.RawMessage(`{"title": "Left", "gridPos": {"h": 8, "w": 8, "x": 0, "y": 0}, "targets": [{"expr": "vg_left"}]}`),
						json.RawMessage(`{"title": "Right", "gridPos": {"h": 8, "w": 8, "x": 16, "y": 0}, "targets": [{"expr": "vg_right"}]}`),
						json.RawMessage(`{"title": "Floater", "gridPos": {"h": 8, "w": 8, "x": 8, "y": 8}, "targets": [{"expr": "vg_floater"}]}`),
					},
				},
			},
		},
	}

	_, _, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error (no sections means overlap-only): %v", err)
	}
}

// TestAssemble_PanelRefNeverResolvesToARow proves an alert pointed at a
// row's own title fails to resolve, rather than injecting a threshold onto it.
func TestAssemble_PanelRefNeverResolvesToARow(t *testing.T) {
	m := &manifest.Model{
		Alerts: manifest.AlertTree{
			Services: []manifest.ServiceAlerts{
				{
					Service: "charlie",
					Alerts: []manifest.Rule{
						{
							UID:       "vg-charlie-ops-check",
							Expr:      "vg_charlie_ops",
							Condition: "gt 1",
							Severity:  "warn",
							PanelRef:  "charlie/Ops",
						},
					},
				},
			},
		},
		Dashboards: manifest.DashTree{
			Services: []manifest.ServiceDash{
				{
					Service:  "charlie",
					UID:      "vg-charlie",
					Title:    "Charlie",
					Sections: map[string]int{"Ops": 0},
				},
			},
		},
	}

	_, _, err := dashboards.Assemble(m)
	if err == nil {
		t.Fatal("Assemble: want an error, got nil (panel_ref must not resolve to a row)")
	}
	msg := err.Error()
	for _, want := range []string{"charlie", "Ops", "no panel titled"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Assemble error = %q, want it to contain %q", msg, want)
		}
	}
}

// TestAssemble_SectionsBlocksAndCustomsCompose proves golden blocks,
// custom panels, and section rows combine into one correctly sorted, stable layout.
func TestAssemble_SectionsBlocksAndCustomsCompose(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Blocks: map[string]manifest.Block{
				"stat": {Panels: []string{
					`{"title": "{Service} request rate", "gridPos": {"h": 4, "w": 24, "x": 0, "y": 0}, "targets": [{"expr": "sum(rate(vg_{service}_requests_total[5m]))"}]}`,
				}},
			},
			Services: []manifest.ServiceDash{
				{
					Service:      "charlie",
					UID:          "vg-charlie",
					Title:        "Charlie",
					GoldenBlocks: map[string]int{"stat": 0},
					Sections:     map[string]int{"Custom region": 4},
					CustomPanels: []json.RawMessage{
						json.RawMessage(`{"title": "Custom left", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 5}, "targets": [{"expr": "vg_left"}]}`),
						json.RawMessage(`{"title": "Custom right", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 5}, "targets": [{"expr": "vg_right"}]}`),
					},
				},
			},
		},
	}

	files, idx, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error: %v", err)
	}

	d := parseDashboard(t, files["charlie.json"])
	if len(d.Panels) != 4 {
		t.Fatalf("len(panels) = %d, want 4 (block panel, row, two custom panels)", len(d.Panels))
	}

	for _, tc := range []struct {
		title string
		id    int
	}{
		{"Charlie request rate", 1}, {"Custom region", 2}, {"Custom left", 3}, {"Custom right", 4},
	} {
		if got := findPanel(t, d, tc.title).ID; got != tc.id {
			t.Errorf("panel %q id = %d, want %d", tc.title, got, tc.id)
		}
	}

	row := findPanel(t, d, "Custom region")
	if row.Type != "row" {
		t.Errorf("row Type = %q, want \"row\"", row.Type)
	}

	wantIdx := dashboards.PanelIndex{"charlie": {"Charlie request rate": 1, "Custom left": 3, "Custom right": 4}}
	if !reflect.DeepEqual(idx, wantIdx) {
		t.Errorf("PanelIndex = %+v, want %+v (the row must not appear)", idx, wantIdx)
	}
}
