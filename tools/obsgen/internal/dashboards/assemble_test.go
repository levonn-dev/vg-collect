package dashboards_test

import (
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/dashboards"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// update regenerates testdata/*.json.golden from Assemble's actual output
// instead of comparing against them: `go test -run TestAssemble_TwoServiceFixture -update`.
// Every use requires re-reading the diff and reviewing it before
// committing - this flag records verified-correct output, it does not
// decide correctness.
var update = flag.Bool("update", false, "write actual Assemble output to the golden files instead of comparing")

// thresholdStep mirrors one entry of a panel's
// fieldConfig.defaults.thresholds.steps. Value is a pointer because
// Grafana's own convention (already visible in any hand-authored
// dashboard's stat-panel thresholds) uses a null-value base step; a bare
// float64 could not represent that.
type thresholdStep struct {
	Color string   `json:"color"`
	Value *float64 `json:"value"`
}

func f64(v float64) *float64 { return &v }

// panelProbe decodes just enough of one emitted panel to check
// assembly's behavior structurally. Assertions below use this instead of
// matching substrings against the final indented JSON, whose exact
// spacing is a detail of the last marshal step, not of assembly itself.
type panelProbe struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
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

// fixtureModel builds a small two-service (alpha, bravo) manifest model
// directly - Assemble takes an in-memory *manifest.Model, so its tests
// construct one straight from the public model types rather than
// round-tripping through manifest.Load and a yaml fixture tree, which
// would just be re-testing the loader. The golden block has 3 panels
// (proving id sequencing past a single entry), with substitution
// exercised two ways: "{Service} request rate" (title placeholder) and
// the Availability panel's pod selector (expr placeholder; its title is
// left constant on purpose, so panel_ref resolution needs no {Service}
// substitution of its own). alpha's custom panel starts with a
// pre-existing threshold step, to prove injection appends rather than
// replacing. bravo has a second custom panel, deliberately titled so it
// sorts alphabetically BEFORE its first ("Bravo error rate" < "Bravo
// refresh duration"): its id still has to come out after, proving id
// continuation follows manifest order and not, say, title order. alpha
// has a second alert - a custom rule, not another golden instantiation -
// pointed at the same panel_ref as its golden availability alert, so
// alpha's Availability panel ends up with two projected steps in a
// checkable order (golden instantiations expand before a service's own
// custom rules); bravo's Availability panel keeps just the one, from its
// golden instantiation alone. bravo also has a custom page-severity
// alert (this repo's paging/most-severe level, distinct from warn and
// crit) pointed at its own request-rate golden panel, proving
// thresholdColor's page->red mapping on a fresh injection, separately
// from crit's existing coverage on alpha's golden instantiation.
func fixtureModel() *manifest.Model {
	golden := []manifest.GoldenPanel{
		{
			Fragment: json.RawMessage(`{"title": "Availability", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "short"}, "overrides": []}, "targets": [{"refId": "A", "expr": "up{namespace=\"vgkeep\", pod=~\"{service}-.*\"}"}]}`),
			GridPos:  manifest.GridPos{H: 8, W: 12, X: 0, Y: 0},
		},
		{
			Fragment: json.RawMessage(`{"title": "{Service} request rate", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "reqps"}, "overrides": []}, "targets": [{"refId": "A", "expr": "sum(rate(vg_{service}_requests_total[5m]))"}]}`),
			GridPos:  manifest.GridPos{H: 8, W: 12, X: 12, Y: 0},
		},
		{
			Fragment: json.RawMessage(`{"title": "Goroutines", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "short"}, "overrides": []}, "targets": [{"refId": "A", "expr": "go_goroutine_count"}]}`),
			GridPos:  manifest.GridPos{H: 8, W: 12, X: 0, Y: 8},
		},
	}

	dashServices := []manifest.ServiceDash{
		{
			Service: "alpha",
			UID:     "vg-alpha",
			Title:   "Alpha",
			CustomPanels: []json.RawMessage{
				json.RawMessage(`{"title": "Alpha queue depth", "type": "timeseries", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 32}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "fieldConfig": {"defaults": {"unit": "short", "thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": null}]}}, "overrides": []}, "targets": [{"refId": "A", "expr": "vg_alpha_queue_pending"}]}`),
			},
		},
		{
			Service: "bravo",
			UID:     "vg-bravo",
			Title:   "Bravo",
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
			Golden:   golden,
			Services: dashServices,
		},
	}
}

// TestAssemble_TwoServiceFixture is the base case: build both dashboards
// from fixtureModel and check every behavior the assembler owns - panel
// id assignment, {service}/{Service} substitution, threshold projection
// (both a fresh injection and an append onto an existing threshold
// step), the panel index, and byte-for-byte determinism across repeated
// calls - before finally comparing the emitted bytes against the
// committed golden files.
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

	// Deterministic ids: golden block ids fixed by position (1-3, same
	// for every service since the block is identical), custom ids
	// continue in manifest order. bravo's second custom panel is titled
	// so it sorts alphabetically BEFORE its first ("Bravo error rate" <
	// "Bravo refresh duration") and must still land on id 5, not 4 -
	// proving the order is the manifest's own, not a resorted one.
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

	// {service}/{Service} substitution: title (the "request rate" golden
	// panel) and an expr selector (the Availability panel's pod regex).
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

	// Threshold projection, fresh case plus multiple alerts on one panel:
	// alpha's Availability panel is targeted by two alerts in this same
	// Assemble call - its golden availability instantiation (template
	// default severity crit -> red, condition "lt 1" -> value 1) and a
	// second, custom "availability-extra" rule (severity warn -> orange,
	// condition "lt 2" -> value 2) - and must end up with three steps,
	// in this order: Grafana's own base step (green, null - see
	// appendStep's baseThresholdColor), added once because thresholds
	// started out entirely absent on this panel, then the two projected
	// steps in golden-before-custom order (golden template
	// instantiations expand before a service's own custom rules).
	// bravo's Availability alert overrides severity to warn -> orange,
	// proving the override (not just the template default) drives the
	// color, and bravo has only the one alert pointed at it (so its
	// panel gets the base step plus that one projected step).
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

	// Threshold projection, page severity: this repo's paging (most
	// severe) level takes the same red line as crit, on a panel with no
	// prior thresholds - proving thresholdColor's page->red mapping is
	// wired, not just crit->red - alongside the same fresh-thresholds
	// base step.
	bravoRate := findPanel(t, bravo, "Bravo request rate")
	if bravoRate.FieldConfig.Defaults.Thresholds == nil {
		t.Fatal("bravo request rate: thresholds not injected")
	}
	if want := []thresholdStep{{Color: "green", Value: nil}, {Color: "red", Value: f64(100)}}; !reflect.DeepEqual(bravoRate.FieldConfig.Defaults.Thresholds.Steps, want) {
		t.Errorf("bravo request rate thresholds.steps = %+v, want %+v (base step first, then severity page -> red)", bravoRate.FieldConfig.Defaults.Thresholds.Steps, want)
	}

	// Threshold projection, append case: alpha's custom queue-backlog
	// alert (severity warn, condition "gt 25") appends an orange step at
	// value 25 onto the queue-depth panel's pre-existing green/null step
	// rather than replacing it. That green/null step is hand-authored in
	// this panel's own fragment (fixtureModel above), not generator-
	// injected - it looks identical to the fresh cases' new base step
	// above but proves the opposite thing: an already-populated
	// thresholds object gets no SECOND base step added on top of its
	// own.
	alphaQueue := findPanel(t, alpha, "Alpha queue depth")
	if alphaQueue.FieldConfig.Defaults.Thresholds == nil {
		t.Fatal("alpha queue depth: thresholds missing entirely")
	}
	wantQueueSteps := []thresholdStep{{Color: "green", Value: nil}, {Color: "orange", Value: f64(25)}}
	if !reflect.DeepEqual(alphaQueue.FieldConfig.Defaults.Thresholds.Steps, wantQueueSteps) {
		t.Errorf("alpha queue depth thresholds.steps = %+v, want %+v (pre-existing step kept, new step appended)", alphaQueue.FieldConfig.Defaults.Thresholds.Steps, wantQueueSteps)
	}

	// Untouched control: bravo's custom panel has no alert pointed at
	// it, so it must come through with no thresholds/custom field added
	// at all - proving injection only ever touches a panel_ref'd panel.
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
		want, err := os.ReadFile("testdata/" + name) //nolint:gosec // G304: name is built from a literal []string{"alpha","bravo"} in this same loop, never external input.
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
// threshold-relevant fields can fail to resolve, each isolated to one
// deliberate change from the otherwise-valid fixtureModel baseline so a
// failure is unambiguous about which behavior broke.
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
			// "abc" (not just the rule uid) confirms this hit
			// strconv.ParseFloat's failure branch specifically, not the
			// separate wrong-token-count guard above - ParseFloat's own
			// wrapped error is what names the offending token.
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

// TestAssemble_PanelBuildErrors covers the panel-build phase: unlike
// golden fragments (which manifest.Load already rejects if malformed),
// a service's custom_panels are never json-validated at load time, so a
// syntax error there is real input Assemble must fail on cleanly rather
// than panic on. A golden fragment is included too, since Assemble's
// signature takes an in-memory *manifest.Model rather than requiring one
// built by manifest.Load, so the same defect is reachable on either
// side of a hand-built model. The duplicate-title cases share one code
// path (addPanel's dup check in buildAllPanels) but are asserted for
// every shape that path has to handle: golden vs. custom, golden vs.
// golden, and custom vs. custom within the same service.
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
				m.Dashboards.Golden[0].Fragment = json.RawMessage(`{not valid json`)
			},
			want: "golden panel 0",
		},
		{
			name: "panel fragment has no title field",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Golden[0].Fragment = json.RawMessage(`{"gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}}`)
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
				m.Dashboards.Golden[1].Fragment = json.RawMessage(`{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0}, "targets": [{"expr": "up"}]}`)
			},
			want: `duplicate panel title "Availability"`,
		},
		{
			name: "two custom panels in the same service share a title",
			mutate: func(m *manifest.Model) {
				// bravo's second custom panel ("Bravo error rate")
				// collides with its first ("Bravo refresh duration").
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
// at all comes out as exactly the golden block and nothing more -
// fixtureModel's own two services never exercise this directly (alpha
// and bravo both declare at least one custom panel), so this uses a
// separate, minimal single-service model instead of piling a third
// service onto the shared byte-golden fixture.
func TestAssemble_EmptyCustomPanels(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Golden: []manifest.GoldenPanel{
				{
					Fragment: json.RawMessage(`{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "up{namespace=\"vgkeep\", pod=~\"{service}-.*\"}"}]}`),
					GridPos:  manifest.GridPos{H: 8, W: 12, X: 0, Y: 0},
				},
				{
					Fragment: json.RawMessage(`{"title": "{Service} request rate", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0}, "targets": [{"expr": "sum(rate(vg_{service}_requests_total[5m]))"}]}`),
					GridPos:  manifest.GridPos{H: 8, W: 12, X: 12, Y: 0},
				},
			},
			Services: []manifest.ServiceDash{
				{
					Service: "charlie",
					UID:     "vg-charlie",
					Title:   "Charlie",
					// CustomPanels deliberately left nil - the zero value
					// a service with no custom_panels: entries decodes to.
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

// TestAssemble_ServiceDisplayNameAcronym pins the owner-ruled exception
// to capitalize's plain first-letter-uppercase fallback: "bff" is an
// acronym, so its {Service} form must render "BFF", not "Bff" - "auth"
// stands in for the general case, which still gets plain capitalize
// ("Auth"). Reuses fixtureModel's own golden block shape (a "{Service}
// request rate" title placeholder alongside the Availability panel's
// {service} pod-selector expr), applied to a fresh two-service, no-
// custom-panel, no-alerts model so this test stays isolated from
// fixtureModel's own alpha/bravo assertions - checking the lowercase
// expr substitution in the same pass proves the acronym exception is
// scoped to {Service} only.
func TestAssemble_ServiceDisplayNameAcronym(t *testing.T) {
	m := &manifest.Model{
		Dashboards: manifest.DashTree{
			Golden: []manifest.GoldenPanel{
				{
					Fragment: json.RawMessage(`{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "up{namespace=\"vgkeep\", pod=~\"{service}-.*\"}"}]}`),
					GridPos:  manifest.GridPos{H: 8, W: 12, X: 0, Y: 0},
				},
				{
					Fragment: json.RawMessage(`{"title": "{Service} request rate", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0}, "targets": [{"expr": "sum(rate(vg_{service}_requests_total[5m]))"}]}`),
					GridPos:  manifest.GridPos{H: 8, W: 12, X: 12, Y: 0},
				},
			},
			Services: []manifest.ServiceDash{
				{Service: "auth", UID: "vg-auth", Title: "Auth"},
				{Service: "bff", UID: "vg-bff", Title: "BFF"},
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
