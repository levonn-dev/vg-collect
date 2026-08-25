package lint_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/lint"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// TestKnown_ExtractsRepresentativeRegistrations proves names.Known
// against a fixture mirroring all three real registration shapes
// (vgotel direct call, pass-through closure, direct OTel SDK call),
// scanning services/ and libs/go/ and every exporter unit form.
func TestKnown_ExtractsRepresentativeRegistrations(t *testing.T) {
	known, err := lint.Known("testdata/names-valid")
	if err != nil {
		t.Fatalf("Known: unexpected error: %v", err)
	}

	want := []string{
		// services/widget: direct vgotel call, curly-brace unit -> no suffix, counter -> _total.
		"vg_widget_cache_fail_open_total",
		// services/widget: CounterLogged/HistogramLogged - the logger
		// parameter shifts name/unit one position right.
		"vg_widget_saves_count_total",
		"vg_widget_save_wait_duration_seconds_bucket",
		"vg_widget_save_wait_duration_seconds_count",
		"vg_widget_save_wait_duration_seconds_sum",
		// services/widget: pass-through closure, curly-brace unit, counter.
		"vg_widget_spins_count_total",
		// services/widget: pass-through closure, "s" unit, histogram -> three suffixes.
		"vg_widget_refresh_duration_seconds_bucket",
		"vg_widget_refresh_duration_seconds_count",
		"vg_widget_refresh_duration_seconds_sum",
		// services/widget: direct OTel SDK call, observable gauge with an
		// explicit metric.WithUnit - no structural suffix at all.
		"vg_widget_pool_connections",
		// services/widget: direct OTel SDK call, counter with no
		// metric.WithUnit option at all (the no-unit case) -> counter -> _total.
		"vg_widget_no_unit_count_total",
		// direct OTel SDK call, "s" unit: suffix applied to the base name
		// BEFORE the three-way split (the closure case above tests it via vgotel instead).
		"vg_widget_pool_wait_seconds_bucket",
		"vg_widget_pool_wait_seconds_count",
		"vg_widget_pool_wait_seconds_sum",
		// libs/go/sharedmetric: direct vgotel call, "ms" unit, histogram.
		"vg_shared_queue_lag_milliseconds_bucket",
		"vg_shared_queue_lag_milliseconds_count",
		"vg_shared_queue_lag_milliseconds_sum",
		// libs/go/sharedmetric: direct vgotel call, "By" unit, counter.
		"vg_shared_payload_size_bytes_total",
	}
	for _, name := range want {
		if _, ok := known[name]; !ok {
			t.Errorf("Known()[%q] missing; got %v", name, known)
		}
	}

	// The dynamic (non-literal) registration in services/widget must
	// contribute nothing - not an error, just silently unrecognized.
	for name := range known {
		if strings.Contains(name, "dynamic") {
			t.Errorf("Known() must not resolve a non-literal metric name, found %q", name)
		}
	}

	// testdata/names-valid/other/decoy.go sits outside services/ and
	// libs/go/ and must never be scanned.
	if _, ok := known["vg_decoy_should_not_appear_total"]; ok {
		t.Error("Known() scanned a file outside services/ and libs/go/")
	}

	// exact size proves no extra names snuck in beyond want (the dynamic and decoy registrations must not contribute).
	if len(known) != len(want) {
		t.Errorf("len(Known()) = %d, want %d; got %v", len(known), len(want), known)
	}
}

// TestKnown_UnrecognizedUnitErrors proves an exporter unit outside the
// four documented forms ({x}, s, ms, By) fails loud rather than
// silently guessing a suffix or omitting one.
func TestKnown_UnrecognizedUnitErrors(t *testing.T) {
	_, err := lint.Known("testdata/names-bad-unit")
	if err == nil {
		t.Fatal("Known: want an error for an unrecognized unit, got nil")
	}
	if !strings.Contains(err.Error(), "kg") {
		t.Errorf("Known error = %q, want it to mention the offending unit %q", err.Error(), "kg")
	}
}

// TestKnown_MissingTree proves a repoRoot missing services/libs/go/
// fails loud, not silently yielding an empty known set.
func TestKnown_MissingTree(t *testing.T) {
	_, err := lint.Known("testdata/does-not-exist")
	if err == nil {
		t.Fatal("Known: want an error for a missing repoRoot, got nil")
	}
}

// repoRoot is the fixture every TestRun_* case reads real files from
// (docs/runbooks, services/, libs/go/).
const repoRoot = "testdata/repo"

// validModel builds a small, internally-consistent manifest.Model that
// produces zero findings against repoRoot: one cluster rule, one
// service with a golden instantiation and two custom rules (prometheus,
// loki), one golden panel, three custom panels. Every TestRun_Findings
// case gets a fresh call (never shared: Model holds slices/maps) and
// changes exactly one thing.
func validModel() *manifest.Model {
	return &manifest.Model{
		Alerts: manifest.AlertTree{
			Group:                  manifest.AlertGroup{Name: "vgkeep", Folder: "vgkeep", Interval: "1m"},
			Datasource:             "prometheus",
			ExternalMetricPrefixes: []string{"up"},
			Templates: map[string]manifest.Template{
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
					Runbook:      "stack.md#1-widget-down-hard",
					PanelRef:     "{service}/Availability",
				},
			},
			Cluster: []manifest.Rule{
				{
					UID: "vg-cluster-thing", Title: "Cluster thing",
					Expr: `up{namespace="vgkeep"}`, Condition: "gt 0",
					Instant: true, Range: "5m", For: "5m",
					NoDataState: "OK", ExecErrState: "Error", Severity: "warn",
					Summary: "a cluster thing happened", Runbook: "stack.md#2-cluster-thing",
				},
			},
			Services: []manifest.ServiceAlerts{
				{
					Service: "widget",
					Golden:  map[string]manifest.Overrides{"availability": {}},
					Alerts: []manifest.Rule{
						{
							UID: "vg-widget-spins-high", Title: "Widget spins elevated",
							Expr: "rate(vg_widget_spins_count_total[5m])", Condition: "gt 10",
							Instant: true, Range: "10m", For: "5m",
							NoDataState: "OK", ExecErrState: "Error", Severity: "warn",
							Summary: "widget spin rate is elevated", Runbook: "widget.md#2-widgets-queue-backlog",
							PanelRef: "widget/Spins",
						},
						{
							// clean-side twin of the token-scan case below: a loki rule
							// (LogQL, never AST-parsed) quoting a REGISTERED name, proving the token-scan path passes cleanly too.
							UID: "vg-widget-loki-check", Title: "Widget error log spike",
							Expr: `{service_name="widget"} |= "vg_widget_spins_count_total"`, Condition: "gt 0",
							Instant: true, Range: "5m", For: "5m",
							NoDataState: "OK", ExecErrState: "Error", Severity: "warn",
							Summary: "widget is logging something", Runbook: "widget.md#2-widgets-queue-backlog",
							Datasource: "loki",
						},
					},
				},
			},
		},
		Dashboards: manifest.DashTree{
			Blocks: map[string]manifest.Block{
				"availability": {Panels: []string{
					`{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "up{namespace=\"vgkeep\", pod=~\"{service}-.*\"}", "legendFormat": "{{pod}}"}]}`,
				}},
			},
			Services: []manifest.ServiceDash{
				{
					Service: "widget", UID: "vg-widget", Title: "Widget",
					GoldenBlocks: map[string]int{"availability": 0},
					CustomPanels: []json.RawMessage{
						json.RawMessage(`{"title": "Spins", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "targets": [{"expr": "sum(rate(vg_widget_spins_count_total[5m]))"}]}`),
						// clean-side macro case: [$__rate_interval] must parse
						// (after parse-only substitution) and still get its metric names checked.
						json.RawMessage(`{"title": "Spin rate", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 8}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "targets": [{"refId": "A", "datasource": {"type": "prometheus", "uid": "prometheus"}, "expr": "sum(rate(vg_widget_spins_count_total[$__rate_interval]))"}]}`),
						// clean-side loki case: LogQL must route to token scan (no
						// expr-parse-error) while the vg_ name it quotes stays checked.
						json.RawMessage(`{"title": "Recent error logs", "gridPos": {"h": 8, "w": 24, "x": 0, "y": 16}, "datasource": {"type": "loki", "uid": "loki"}, "targets": [{"refId": "A", "datasource": {"type": "loki", "uid": "loki"}, "expr": "{service_name=\"widget\"} | severity_text=\"ERROR\" |= \"vg_widget_spins_count_total\""}]}`),
					},
				},
			},
		},
	}
}

// TestRun_Clean proves the happy path: validModel against repoRoot
// produces zero findings, exercising both the AST and token-scan paths
// cleanly, not just their failure modes.
func TestRun_Clean(t *testing.T) {
	findings := lint.Run(validModel(), repoRoot)
	if len(findings) != 0 {
		t.Errorf("Run() = %+v, want no findings", findings)
	}
}

// findingsWithRule filters findings to those whose Rule matches want.
func findingsWithRule(findings []lint.Finding, want string) []lint.Finding {
	var out []lint.Finding
	for _, f := range findings {
		if f.Rule == want {
			out = append(out, f)
		}
	}
	return out
}

// TestRun_Findings tables every check Run owns, each isolated to one
// change from validModel's clean baseline.
func TestRun_Findings(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*manifest.Model)
		wantRule   string
		wantPath   string // exact Finding.Path
		wantSubstr string // the single strongest substring of Finding.Message
	}{
		{
			name: "duplicate uid after {service} expansion",
			mutate: func(m *manifest.Model) {
				// collides with the golden instantiation's expanded uid
				// (vg-{service}-down -> vg-widget-down), invisible to Load's own checkUIDs.
				m.Alerts.Cluster[0].UID = "vg-widget-down"
			},
			wantRule: "duplicate-uid",
			// the finding lands on the SECOND occurrence (alerts/widget.yaml)
			// and names the FIRST (alerts/cluster.yaml).
			wantPath:   "alerts/widget.yaml",
			wantSubstr: "also used in alerts/cluster.yaml",
		},
		{
			name: "retired uid collides with a live expanded uid",
			mutate: func(m *manifest.Model) {
				m.Alerts.Retired = []manifest.RetiredUID{{UID: "vg-widget-down", Date: "2026-01-01", Reason: "test"}}
			},
			wantRule:   "retired-live-collision",
			wantPath:   "alerts/retired.yaml",
			wantSubstr: "still resolves to a live rule in alerts/widget.yaml",
		},
		{
			name: "unresolved placeholder from a misspelled template token",
			mutate: func(m *manifest.Model) {
				tmpl := m.Alerts.Templates["availability"]
				tmpl.Title = "{Svc} service down" // {Svc}, not {Service} - substitute never touches it
				m.Alerts.Templates["availability"] = tmpl
			},
			wantRule:   "unresolved-placeholder",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: "title still contains {Svc}",
		},
		{
			// goldenItem must build the item from the override actually in
			// effect, not the template default, or this typo reaches vg-rules.yaml unnoticed.
			name: "unresolved placeholder from a misspelled golden override summary",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Golden["availability"] = manifest.Overrides{Summary: "{servce} down"}
			},
			wantRule:   "unresolved-placeholder",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: "summary still contains {servce}",
		},
		{
			name: "unknown golden template name",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Golden["does-not-exist"] = manifest.Overrides{}
			},
			wantRule:   "unknown-golden-template",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: `unknown golden template "does-not-exist"`,
		},
		{
			name: "a defined golden block that no service instantiates is flagged unused",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Blocks["orphan"] = manifest.Block{Panels: []string{
					`{"title": "Orphan", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "up"}]}`,
				}}
			},
			wantRule:   "unused-golden-block",
			wantPath:   "dashboards/golden.yaml",
			wantSubstr: `block "orphan" is never instantiated`,
		},
		{
			name: "panel_ref malformed (no slash)",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].PanelRef = "widget-no-slash"
			},
			wantRule:   "unresolved-panel-ref",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: `malformed panel_ref "widget-no-slash"`,
		},
		{
			name: "panel_ref names a service with no dashboard",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].PanelRef = "ghost/Availability"
			},
			wantRule:   "unresolved-panel-ref",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: `service "ghost" has no dashboard`,
		},
		{
			name: "panel_ref names a title missing from that service's dashboard",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].PanelRef = "widget/Does not exist"
			},
			wantRule:   "unresolved-panel-ref",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: `service "widget" has no panel titled "Does not exist"`,
		},
		{
			name: "runbook value has no #anchor",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].Runbook = "widget.md"
			},
			wantRule:   "runbook-anchor-missing",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: `runbook "widget.md" has no #anchor`,
		},
		{
			name: "runbook file does not exist",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].Runbook = "does-not-exist.md#foo"
			},
			wantRule: "runbook-anchor-missing",
			wantPath: "alerts/widget.yaml",
			// stops short of the raw os.ReadFile error text (platform-dependent); only this package's own formatting is asserted.
			wantSubstr: "rule vg-widget-spins-high: docs/runbooks/does-not-exist.md:",
		},
		{
			name: "runbook anchor missing from an existing file",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].Runbook = "widget.md#does-not-exist"
			},
			wantRule:   "runbook-anchor-missing",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: `docs/runbooks/widget.md has no heading matching anchor "does-not-exist"`,
		},
		{
			name: "rule has no row in the runbook index",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].UID = "vg-widget-not-indexed"
			},
			wantRule:   "runbook-index-row-missing",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: "rule vg-widget-not-indexed: no row",
		},
		{
			name: "rule expr does not parse (prometheus datasource)",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].Expr = "not a valid expr ("
			},
			wantRule: "expr-parse-error",
			wantPath: "alerts/widget.yaml",
			// only this package's own "rule <uid>:" prefix is asserted, not promql/parser's own wording.
			wantSubstr: "rule vg-widget-spins-high:",
		},
		{
			name: "rule expr names an unknown metric (prometheus datasource, AST path)",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].Expr = "rate(vg_widget_totally_unknown_total[5m])"
			},
			wantRule:   "unknown-metric",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: `metric "vg_widget_totally_unknown_total" is not a known registration or external prefix`,
		},
		{
			name: "rule on a non-prometheus datasource token-scans instead of AST-parsing",
			mutate: func(m *manifest.Model) {
				m.Alerts.Services[0].Alerts[0].Datasource = "loki"
				// not valid PromQL (LogQL): proves routing skips AST parsing rather than reporting a spurious expr-parse-error.
				m.Alerts.Services[0].Alerts[0].Expr = `{service_name="widget"} |= "vg_widget_totally_unknown_total"`
			},
			wantRule: "unknown-metric",
			wantPath: "alerts/widget.yaml",
			// proves both the routing (token-scanned, not AST-parsed) and the specific token found.
			wantSubstr: `(datasource loki, token-scanned): token "vg_widget_totally_unknown_total"`,
		},
		{
			name: "panel expr does not parse",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{"title": "Spins", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "targets": [{"expr": "not a valid expr ("}]}`)
			},
			wantRule:   "expr-parse-error",
			wantPath:   "dashboards/widget.yaml",
			wantSubstr: "panel widget/Spins:",
		},
		{
			name: "panel expr names an unknown metric",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{"title": "Spins", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "targets": [{"expr": "vg_widget_totally_unknown_total"}]}`)
			},
			wantRule:   "unknown-metric",
			wantPath:   "dashboards/widget.yaml",
			wantSubstr: `panel widget/Spins: metric "vg_widget_totally_unknown_total"`,
		},
		{
			name: "loki-datasourced panel token-scans instead of AST-parsing",
			mutate: func(m *manifest.Model) {
				// panel-level datasource only (no target override), exercising the
				// fallback step; LogQL routed to AST would spuriously expr-parse-error.
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{"title": "Spins", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "datasource": {"type": "loki", "uid": "loki"}, "targets": [{"expr": "{service_name=\"widget\"} | severity_text=\"ERROR\" |= \"vg_widget_totally_unknown_total\""}]}`)
			},
			wantRule: "unknown-metric",
			wantPath: "dashboards/widget.yaml",
			// proves both the routing (token-scanned, not AST-parsed) and the specific token found.
			wantSubstr: `(datasource loki, token-scanned): token "vg_widget_totally_unknown_total"`,
		},
		{
			name: "a target's own datasource overrides the panel's (loki target on a prometheus panel)",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{"title": "Spins", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "datasource": {"type": "prometheus", "uid": "prometheus"}, "targets": [{"datasource": {"type": "loki", "uid": "loki"}, "expr": "{service_name=\"widget\"} |= \"vg_widget_totally_unknown_total\""}]}`)
			},
			wantRule:   "unknown-metric",
			wantPath:   "dashboards/widget.yaml",
			wantSubstr: `(datasource loki, token-scanned): token "vg_widget_totally_unknown_total"`,
		},
		{
			name: "a target's own datasource overrides the panel's (prometheus target on a loki panel)",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{"title": "Spins", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "datasource": {"type": "loki", "uid": "loki"}, "targets": [{"datasource": {"type": "prometheus", "uid": "prometheus"}, "expr": "vg_widget_totally_unknown_total"}]}`)
			},
			wantRule: "unknown-metric",
			wantPath: "dashboards/widget.yaml",
			// "metric" (not "token"), no token-scanned marker: proves target-level prometheus won over panel-level loki.
			wantSubstr: `panel widget/Spins: metric "vg_widget_totally_unknown_total"`,
		},
		{
			name: "a datasource named by bare string still routes (and keeps the panel readable)",
			mutate: func(m *manifest.Model) {
				// Grafana's other accepted spelling; a strict object-only decode
				// would drop the whole panel, cascading into a panel_ref finding instead.
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{"title": "Spins", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "datasource": "loki", "targets": [{"expr": "{service_name=\"widget\"} |= \"vg_widget_totally_unknown_total\""}]}`)
			},
			wantRule:   "unknown-metric",
			wantPath:   "dashboards/widget.yaml",
			wantSubstr: `(datasource loki, token-scanned): token "vg_widget_totally_unknown_total"`,
		},
		{
			name: "a macro-carrying panel expr still has its metric names checked",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{"title": "Spins", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "targets": [{"expr": "sum(rate(vg_widget_totally_unknown_total[$__rate_interval]))"}]}`)
			},
			wantRule:   "unknown-metric",
			wantPath:   "dashboards/widget.yaml",
			wantSubstr: `panel widget/Spins: metric "vg_widget_totally_unknown_total"`,
		},
		{
			name: "a rule expr carrying a Grafana macro is still a parse error (rules get no substitution)",
			mutate: func(m *manifest.Model) {
				// a rule's query is sent to the datasource as authored (no
				// dashboard macro expansion), so a macro here is a real defect.
				m.Alerts.Services[0].Alerts[0].Expr = "rate(vg_widget_spins_count_total[$__rate_interval])"
			},
			wantRule:   "expr-parse-error",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: "rule vg-widget-spins-high:",
		},
		{
			name: "golden panel title still has an unresolved placeholder",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Blocks["availability"].Panels[0] = `{"title": "{Svc} Availability", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "up"}]}`
			},
			// also cascades into a second finding (panel_ref no longer resolves);
			// findingsWithRule filters to this case's own rule, so it doesn't interfere.
			wantRule:   "unresolved-placeholder",
			wantPath:   "golden.yaml block availability",
			wantSubstr: "fragment still contains {Svc}",
		},
		{
			// proves the whole-fragment scan catches a placeholder inside a label selector, not just the parsed title.
			name: "golden panel fragment has an unresolved placeholder outside the title (a quoted selector)",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Blocks["availability"].Panels[0] = `{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "up{namespace=\"vgkeep\", pod=~\"{servce}-.*\"}"}]}`
			},
			wantRule:   "unresolved-placeholder",
			wantPath:   "golden.yaml block availability",
			wantSubstr: "fragment still contains {servce}",
		},
		{
			name: "a malformed custom panel is skipped, cascading to an unresolved panel_ref rather than a crash",
			mutate: func(m *manifest.Model) {
				m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(`{not valid json`)
			},
			wantRule:   "unresolved-panel-ref",
			wantPath:   "alerts/widget.yaml",
			wantSubstr: `service "widget" has no panel titled "Spins"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validModel()
			tc.mutate(m)

			findings := lint.Run(m, repoRoot)
			matches := findingsWithRule(findings, tc.wantRule)
			if len(matches) != 1 {
				t.Fatalf("Run() has %d %q finding(s), want exactly 1; got %+v", len(matches), tc.wantRule, findings)
			}

			got := matches[0]
			if got.Path != tc.wantPath {
				t.Errorf("%s finding Path = %q, want %q (Message: %q)", tc.wantRule, got.Path, tc.wantPath, got.Message)
			}
			if !strings.Contains(got.Message, tc.wantSubstr) {
				t.Errorf("%s finding Message = %q, want it to contain %q", tc.wantRule, got.Message, tc.wantSubstr)
			}
		})
	}
}

// TestRun_PanelMacroDoesNotMaskBreakage proves the parse-only
// substitution cannot hide a real break (macro + unbalanced paren still
// errors), and the finding quotes the expr as authored, not substituted.
func TestRun_PanelMacroDoesNotMaskBreakage(t *testing.T) {
	const broken = `sum(rate(vg_widget_spins_count_total[$__rate_interval])`

	m := validModel()
	m.Dashboards.Services[0].CustomPanels[0] = json.RawMessage(
		`{"title": "Spins", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8}, "targets": [{"expr": "` + broken + `"}]}`)

	findings := lint.Run(m, repoRoot)
	parseErrors := findingsWithRule(findings, "expr-parse-error")
	if len(parseErrors) != 1 {
		t.Fatalf("want exactly 1 expr-parse-error, got %d: %+v", len(parseErrors), findings)
	}
	if got := parseErrors[0].Path; got != "dashboards/widget.yaml" {
		t.Errorf("expr-parse-error Path = %q, want %q", got, "dashboards/widget.yaml")
	}
	if !strings.Contains(parseErrors[0].Message, "panel widget/Spins:") {
		t.Errorf("expr-parse-error Message = %q, want this package's own panel prefix", parseErrors[0].Message)
	}
	if !strings.Contains(parseErrors[0].Message, broken) {
		t.Errorf("expr-parse-error Message = %q, want it to quote the authored expr %q", parseErrors[0].Message, broken)
	}
}

// TestRun_MetricJurisdiction proves only a vg_-prefixed unknown name
// fires: a real but non-vg_ series (http_server_request_duration_seconds_count)
// this repo emits but never registers must not, regardless of being real.
func TestRun_MetricJurisdiction(t *testing.T) {
	m := validModel()
	m.Alerts.Cluster = append(m.Alerts.Cluster,
		manifest.Rule{
			UID: "vg-cluster-non-vg-name", Title: "Non-vg_ name check",
			Expr:      `sum(rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m]))`,
			Condition: "gt 0.05", Instant: true, Range: "5m", For: "5m",
			NoDataState: "OK", ExecErrState: "Error", Severity: "warn",
			Summary: "a real but unregistered, non-vg_ metric name", Runbook: "stack.md#2-cluster-thing",
		},
		manifest.Rule{
			UID: "vg-cluster-vg-unknown-name", Title: "vg_ unknown name check",
			Expr: "sum(rate(vg_widget_totally_unknown_total[5m]))", Condition: "gt 0",
			Instant: true, Range: "5m", For: "5m",
			NoDataState: "OK", ExecErrState: "Error", Severity: "warn",
			Summary: "a vg_-prefixed name nothing registers", Runbook: "stack.md#2-cluster-thing",
		},
	)

	findings := lint.Run(m, repoRoot)
	unknown := findingsWithRule(findings, "unknown-metric")
	if len(unknown) != 1 {
		t.Fatalf("want exactly 1 unknown-metric finding (the vg_ one only), got %d: %+v", len(unknown), unknown)
	}
	if !strings.Contains(unknown[0].Message, "vg_widget_totally_unknown_total") {
		t.Errorf("the one unknown-metric finding should be about the vg_ name, got %+v", unknown[0])
	}
}

// TestRun_RunbookDocScan proves every runbook's fenced block is
// checked regardless of citation, both AST and token-scan paths catch
// an unregistered name, and a real non-vg_ name still never fires.
func TestRun_RunbookDocScan(t *testing.T) {
	m := validModel()
	// this fixture's runbooks don't define validModel's anchors; only the
	// doc-wide scan's findings are asserted below.
	findings := lint.Run(m, "testdata/repo-runbook-drift")

	unknown := findingsWithRule(findings, "unknown-metric")
	if len(unknown) < 2 {
		t.Fatalf("want at least 2 unknown-metric findings (AST path + token-scan path), got %d: %+v", len(unknown), unknown)
	}

	var astHit, tokenHit, nonVGHit bool
	for _, f := range unknown {
		if f.Path != "docs/runbooks/widget.md" {
			continue
		}
		if strings.Contains(f.Message, "http_server_request_duration_seconds_count") {
			nonVGHit = true
			continue
		}
		if !strings.Contains(f.Message, "vg_widget_refresh_walk_seconds_count") {
			continue
		}
		switch {
		case strings.Contains(f.Message, "fenced query:"):
			astHit = true
		case strings.Contains(f.Message, "token-scanned"):
			tokenHit = true
		}
	}
	if !astHit {
		t.Errorf("no AST-path unknown-metric finding for the fenced PromQL block; got %+v", unknown)
	}
	if !tokenHit {
		t.Errorf("no token-scan-path unknown-metric finding for the shell-quoted block; got %+v", unknown)
	}
	if nonVGHit {
		t.Error("a non-vg_ name (section 3, outside this lint's jurisdiction) must never fire, even in the runbook scan")
	}
}

// TestRun_MetricScanErrorDegradesGracefully proves a failed Known
// yields exactly one finding and skips metric-dependent checks, while
// Known-independent checks (uid uniqueness) still run.
func TestRun_MetricScanErrorDegradesGracefully(t *testing.T) {
	m := validModel()
	m.Alerts.Cluster[0].UID = "vg-widget-down" // also trip a uid collision, unrelated to the metric scan

	findings := lint.Run(m, "testdata/does-not-exist")

	scanErrors := findingsWithRule(findings, "metric-scan-error")
	if len(scanErrors) != 1 {
		t.Fatalf("want exactly one metric-scan-error finding, got %d: %+v", len(scanErrors), findings)
	}

	if got := findingsWithRule(findings, "duplicate-uid"); len(got) == 0 {
		t.Errorf("duplicate-uid check must still run when Known fails; got %+v", findings)
	}
	if got := findingsWithRule(findings, "unknown-metric"); len(got) != 0 {
		t.Errorf("unknown-metric must be skipped (not falsely fired) when Known fails; got %+v", got)
	}
}
