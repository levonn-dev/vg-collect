package alerts_test

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/alerts"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/dashboards"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// update rewrites the golden fixture from actual output instead of
// comparing against it; review the diff before committing.
var update = flag.Bool("update", false, "write actual Emit output to the golden files instead of comparing")

// TestEmit_RealRuleByteFixture proves byte-exact fidelity against
// vg-service-5xx's real fields, the one place a hand-authored file (not
// Emit's own prior output) is the source of truth.
func TestEmit_RealRuleByteFixture(t *testing.T) {
	m := &manifest.Model{
		Alerts: manifest.AlertTree{
			Group:      manifest.AlertGroup{Name: "vgkeep", Folder: "vgkeep", Interval: "1m"},
			Datasource: "prometheus",
			Cluster: []manifest.Rule{
				{
					UID:          "vg-service-5xx",
					Title:        "Service 5xx ratio above 5 percent",
					Expr:         `sum by (service_name) (rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m])) / sum by (service_name) (rate(http_server_request_duration_seconds_count[5m]))`,
					Condition:    "gt 0.05",
					Instant:      true,
					Range:        "10m", // relativeTimeRange.from: 600
					For:          "5m",
					NoDataState:  "OK",
					ExecErrState: "Error",
					Severity:     "page",
					Summary:      "{{ $labels.service_name }} is answering more than 5 percent 5xx",
					Runbook:      "stack.md#1-service-5xx-ratio-above-5-percent",
				},
			},
		},
	}

	got, err := alerts.Emit(m, dashboards.PanelIndex{})
	if err != nil {
		t.Fatalf("Emit: unexpected error: %v", err)
	}

	want, err := os.ReadFile("testdata/single-rule.yaml.golden")
	if err != nil {
		t.Fatalf("reading testdata/single-rule.yaml.golden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Emit output does not byte-match today's real vg-rules.yaml rule\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestEmit_ServiceDisplayNameAcronym pins the acronym exception: "bff"
// renders "BFF" in {Service} form, not "Bff"; "auth" is the general-case
// control ("Auth"). Also checks {service} lowercase substitution is unaffected.
func TestEmit_ServiceDisplayNameAcronym(t *testing.T) {
	m := &manifest.Model{
		Alerts: manifest.AlertTree{
			Group:      manifest.AlertGroup{Name: "vgkeep", Folder: "vgkeep", Interval: "1m"},
			Datasource: "prometheus",
			Templates:  templates(),
			Services: []manifest.ServiceAlerts{
				{Service: "auth", Golden: map[string]manifest.Overrides{"pdb_budget": {}}},
				{Service: "bff", Golden: map[string]manifest.Overrides{"pdb_budget": {}}},
			},
		},
	}

	got, err := alerts.Emit(m, dashboards.PanelIndex{})
	if err != nil {
		t.Fatalf("Emit: unexpected error: %v", err)
	}
	doc := string(got)

	for _, want := range []string{
		"title: Auth disruption budget exhausted",
		"title: BFF disruption budget exhausted",
		`poddisruptionbudget=~"auth.*"`,
		`poddisruptionbudget=~"bff.*"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("Emit output missing %q:\n%s", want, doc)
		}
	}
}

// templates returns the two golden templates ("availability",
// "pdb_budget") with field values mirroring the real rules they model.
func templates() map[string]manifest.Template {
	return map[string]manifest.Template{
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
		"pdb_budget": {
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
			// no PanelRef: proves a golden instantiation with no panel_ref gains no dashboard-link annotations.
		},
	}
}

// fixtureModel builds a two-service, two-cluster-rule manifest exercising
// every ordering, override, and dashboard-link rule Emit owns; see
// TestEmit_ComprehensiveFixture's own assertions for what each part proves.
func fixtureModel() *manifest.Model {
	return &manifest.Model{
		Alerts: manifest.AlertTree{
			Group:      manifest.AlertGroup{Name: "vgkeep", Folder: "vgkeep", Interval: "1m"},
			Datasource: "prometheus",
			Templates:  templates(),
			Cluster: []manifest.Rule{
				{
					UID: "vg-pod-churn", Title: "Pod restart churn or OOM kill",
					Expr:         `sum by (namespace, pod) (increase(kube_pod_container_status_restarts_total{namespace=~"vgkeep"}[15m])) > 3`,
					Condition:    "gt 0",
					Instant:      true,
					Range:        "15m",
					For:          "5m",
					NoDataState:  "OK",
					ExecErrState: "Error",
					Severity:     "warn",
					Summary:      "a pod is restart-churning or was OOM-killed",
					Runbook:      "stack.md#pod-restart-churn-or-oom-kill",
				},
				{
					UID: "vg-node-pressure", Title: "Node under memory, disk or PID pressure",
					Expr:         `kube_node_status_condition{condition=~"MemoryPressure|DiskPressure|PIDPressure", status="true"} > 0`,
					Condition:    "gt 0",
					Instant:      true,
					Range:        "10m",
					For:          "5m",
					NoDataState:  "OK",
					ExecErrState: "Error",
					Severity:     "page",
					Summary:      "node pressure condition active",
					Runbook:      "stack.md#node-pressure",
				},
			},
			Services: []manifest.ServiceAlerts{
				{
					Service: "alpha",
					Golden: map[string]manifest.Overrides{
						"pdb_budget":   {For: "2h"},
						"availability": {},
					},
					Alerts: []manifest.Rule{
						{
							UID: "vg-alpha-queue-backlog", Title: "Alpha queue not draining",
							Expr:         "max(vg_alpha_queue_pending)",
							Condition:    "gt 25",
							Instant:      true,
							Range:        "10m",
							For:          "10m",
							NoDataState:  "OK",
							ExecErrState: "Error",
							Severity:     "warn",
							Summary:      "the alpha queue has stayed above 25 pending",
							Runbook:      "alpha.md#queue-backlog",
							PanelRef:     "alpha/Alpha queue depth",
						},
						{
							UID: "vg-alpha-error-rate", Title: "Alpha error rate elevated",
							Expr:         "rate(vg_alpha_errors_total[5m])",
							Condition:    "gt 5",
							Instant:      true,
							Range:        "10m",
							For:          "5m",
							NoDataState:  "OK",
							ExecErrState: "Error",
							Severity:     "warn",
							Summary:      "alpha is logging errors at an elevated rate",
							Runbook:      "alpha.md#error-rate",
						},
					},
				},
				{
					Service: "bravo",
					Golden: map[string]manifest.Overrides{
						"availability": {Condition: "lt 2", Severity: "warn", Summary: "bravo override summary for bravo"},
					},
				},
			},
			Retired: []manifest.RetiredUID{
				{UID: "vg-old-thing-2", Date: "2026-02-01", Reason: "superseded by vg-old-thing-3"},
				{UID: "vg-old-thing-1", Date: "2026-01-01", Reason: "superseded by vg-old-thing-2"},
			},
		},
		Dashboards: manifest.DashTree{
			Services: []manifest.ServiceDash{
				{Service: "alpha", UID: "vg-alpha", Title: "Alpha"},
				{Service: "bravo", UID: "vg-bravo", Title: "Bravo"},
			},
		},
	}
}

// fixtureIndex is fixtureModel's PanelIndex, built by hand so this test
// stays isolated to Emit (TestEmit_WithRealAssemble covers the real handoff).
func fixtureIndex() dashboards.PanelIndex {
	return dashboards.PanelIndex{
		"alpha": {"Availability": 1, "Alpha queue depth": 2},
		"bravo": {"Availability": 1},
	}
}

// TestEmit_ComprehensiveFixture checks every ordering/override/
// dashboard-link/deleteRules behavior before comparing against the golden file.
func TestEmit_ComprehensiveFixture(t *testing.T) {
	m := fixtureModel()
	idx := fixtureIndex()

	got1, err := alerts.Emit(m, idx)
	if err != nil {
		t.Fatalf("Emit: unexpected error: %v", err)
	}

	got2, err := alerts.Emit(m, idx)
	if err != nil {
		t.Fatalf("Emit (second run): unexpected error: %v", err)
	}
	if string(got1) != string(got2) {
		t.Errorf("Emit is not byte-idempotent across repeated calls on the same model")
	}

	doc := string(got1)

	// Ordering: cluster (manifest order) before any service; alpha
	// (golden-sorted, then custom manifest order) before bravo.
	wantOrder := []string{
		"uid: vg-pod-churn",
		"uid: vg-node-pressure",
		"uid: vg-alpha-down",
		"uid: vg-alpha-pdb-exhausted",
		"uid: vg-alpha-queue-backlog",
		"uid: vg-alpha-error-rate",
		"uid: vg-bravo-down",
	}
	lastIdx := -1
	for _, want := range wantOrder {
		i := strings.Index(doc, want)
		if i < 0 {
			t.Fatalf("output missing %q entirely\n%s", want, doc)
		}
		if i < lastIdx {
			t.Errorf("uid marker %q appears out of order (want after the previous entries)\n%s", want, doc)
		}
		lastIdx = i
	}

	// {service}/{Service} substitution in a golden instantiation's uid, title, expr.
	if !strings.Contains(doc, "title: Alpha service down") {
		t.Errorf("golden title substitution failed for alpha:\n%s", doc)
	}
	if !strings.Contains(doc, `expr: up{namespace="vgkeep", pod=~"alpha-.*"}`) {
		t.Errorf("golden expr substitution failed for alpha:\n%s", doc)
	}
	if !strings.Contains(doc, `expr: up{namespace="vgkeep", pod=~"bravo-.*"}`) {
		t.Errorf("golden expr substitution failed for bravo:\n%s", doc)
	}

	// Override coverage: alpha's pdb_budget overrides only `for` (1h ->
	// 2h); template condition/severity/summary defaults must still show.
	pdbBlock := ruleBlock(t, doc, "vg-alpha-pdb-exhausted")
	if !strings.Contains(pdbBlock, "for: 2h") {
		t.Errorf("alpha pdb_budget: for override not applied:\n%s", pdbBlock)
	}
	if !strings.Contains(pdbBlock, "params: [1]") {
		t.Errorf("alpha pdb_budget: condition should stay at template default (lt 1):\n%s", pdbBlock)
	}
	if !strings.Contains(pdbBlock, "severity: warn") {
		t.Errorf("alpha pdb_budget: severity should stay at template default (warn):\n%s", pdbBlock)
	}

	// bravo's availability overrides condition/severity/summary but not
	// `for` (must stay at the template default, 5m).
	bravoBlock := ruleBlock(t, doc, "vg-bravo-down")
	if !strings.Contains(bravoBlock, "for: 5m") {
		t.Errorf("bravo availability: for should stay at template default (5m):\n%s", bravoBlock)
	}
	if !strings.Contains(bravoBlock, "params: [2]") {
		t.Errorf("bravo availability: condition override (lt 2) not applied:\n%s", bravoBlock)
	}
	if !strings.Contains(bravoBlock, "severity: warn") {
		t.Errorf("bravo availability: severity override (warn) not applied:\n%s", bravoBlock)
	}
	if !strings.Contains(bravoBlock, `summary: "bravo override summary for bravo"`) {
		t.Errorf("bravo availability: summary override not applied/substituted:\n%s", bravoBlock)
	}

	// Positive dashboard-link cases: golden-derived (alpha-down) and
	// custom-derived (queue-backlog) both carry panel_ref and must gain both annotations.
	alphaDown := ruleBlock(t, doc, "vg-alpha-down")
	if !strings.Contains(alphaDown, "__dashboardUid__: vg-alpha") || !strings.Contains(alphaDown, `__panelId__: "1"`) {
		t.Errorf("alpha-down: missing/wrong dashboard-link annotations:\n%s", alphaDown)
	}
	queueBacklog := ruleBlock(t, doc, "vg-alpha-queue-backlog")
	if !strings.Contains(queueBacklog, "__dashboardUid__: vg-alpha") || !strings.Contains(queueBacklog, `__panelId__: "2"`) {
		t.Errorf("alpha-queue-backlog: missing/wrong dashboard-link annotations:\n%s", queueBacklog)
	}
	bravoDown := ruleBlock(t, doc, "vg-bravo-down")
	if !strings.Contains(bravoDown, "__dashboardUid__: vg-bravo") || !strings.Contains(bravoDown, `__panelId__: "1"`) {
		t.Errorf("bravo-down: missing/wrong dashboard-link annotations (dashboardUid must vary per service):\n%s", bravoDown)
	}

	// Negative dashboard-link cases: no panel_ref means neither annotation
	// appears, golden-derived (pdb-exhausted) and custom-derived (error-rate).
	if strings.Contains(pdbBlock, "__dashboardUid__") || strings.Contains(pdbBlock, "__panelId__") {
		t.Errorf("alpha-pdb-exhausted has no panel_ref; must gain no dashboard-link annotations:\n%s", pdbBlock)
	}
	errRate := ruleBlock(t, doc, "vg-alpha-error-rate")
	if strings.Contains(errRate, "__dashboardUid__") || strings.Contains(errRate, "__panelId__") {
		t.Errorf("alpha-error-rate has no panel_ref; must gain no dashboard-link annotations:\n%s", errRate)
	}

	// runbook expansion: short form -> canonical GitHub blob URL prefix.
	if !strings.Contains(doc, "runbook_url: https://github.com/levonn-dev/vgkeep/blob/main/docs/runbooks/stack.md#pod-restart-churn-or-oom-kill") {
		t.Errorf("runbook_url expansion missing/wrong for vg-pod-churn:\n%s", doc)
	}
	if !strings.Contains(doc, "runbook_url: https://github.com/levonn-dev/vgkeep/blob/main/docs/runbooks/alpha.md#queue-backlog") {
		t.Errorf("runbook_url expansion missing/wrong for vg-alpha-queue-backlog:\n%s", doc)
	}

	// deleteRules: manifest order preserved (old-thing-2 before
	// old-thing-1), each with orgId 1.
	delSection := doc[strings.Index(doc, "deleteRules:"):]
	if strings.Index(delSection, "vg-old-thing-2") > strings.Index(delSection, "vg-old-thing-1") {
		t.Errorf("deleteRules did not preserve manifest order:\n%s", delSection)
	}
	if strings.Count(delSection, "orgId: 1") != 2 {
		t.Errorf("deleteRules: want orgId: 1 once per retired entry (2 entries):\n%s", delSection)
	}

	if t.Failed() {
		t.Fatal("earlier assertions failed; not comparing or writing golden files against a known-bad run")
	}

	const goldenName = "testdata/comprehensive.yaml.golden"
	if *update {
		if err := os.WriteFile(goldenName, got1, 0o600); err != nil {
			t.Fatalf("writing %s: %v", goldenName, err)
		}
		t.Skip("golden file updated; re-run without -update to verify")
	}
	want, err := os.ReadFile(goldenName) //nolint:gosec // G304: goldenName is a package-local literal constant, not external input.
	if err != nil {
		t.Fatalf("reading %s: %v", goldenName, err)
	}
	if doc != string(want) {
		t.Errorf("Emit output does not match %s\n--- got ---\n%s\n--- want ---\n%s", goldenName, doc, want)
	}
}

// ruleBlock isolates one rule's text (uid marker to the next sibling
// marker), so an assertion cannot accidentally match another rule's field.
func ruleBlock(t *testing.T, doc, uid string) string {
	t.Helper()
	start := strings.Index(doc, "uid: "+uid)
	if start < 0 {
		t.Fatalf("output has no rule uid %q\n%s", uid, doc)
	}
	rest := doc[start:]
	end := strings.Index(rest[1:], "\n      - uid:")
	if end < 0 {
		endAlt := strings.Index(rest[1:], "\ndeleteRules:")
		if endAlt < 0 {
			return rest
		}
		return rest[:endAlt+1]
	}
	return rest[:end+1]
}

// TestEmit_WithRealAssemble proves dashboards.Assemble's own PanelIndex
// feeds alerts.Emit, so annotations match what the same pass produced.
func TestEmit_WithRealAssemble(t *testing.T) {
	m := &manifest.Model{
		Alerts: manifest.AlertTree{
			Group:      manifest.AlertGroup{Name: "vgkeep", Folder: "vgkeep", Interval: "1m"},
			Datasource: "prometheus",
			Templates:  templates(),
			Services: []manifest.ServiceAlerts{
				{Service: "charlie", Golden: map[string]manifest.Overrides{"availability": {}}},
			},
		},
		Dashboards: manifest.DashTree{
			Blocks: map[string]manifest.Block{
				"shared": {Panels: []string{
					`{"title": "Availability", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0}, "targets": [{"expr": "up{namespace=\"vgkeep\", pod=~\"{service}-.*\"}"}]}`,
					`{"title": "{Service} request rate", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0}, "targets": [{"expr": "sum(rate(vg_{service}_requests_total[5m]))"}]}`,
				}},
			},
			Services: []manifest.ServiceDash{
				{Service: "charlie", UID: "vg-charlie", Title: "Charlie", GoldenBlocks: map[string]int{"shared": 0}},
			},
		},
	}

	_, idx, err := dashboards.Assemble(m)
	if err != nil {
		t.Fatalf("Assemble: unexpected error: %v", err)
	}
	wantID, ok := idx["charlie"]["Availability"]
	if !ok {
		t.Fatalf("Assemble did not index charlie's Availability panel: %+v", idx)
	}

	got, err := alerts.Emit(m, idx)
	if err != nil {
		t.Fatalf("Emit: unexpected error: %v", err)
	}
	doc := string(got)

	if !strings.Contains(doc, "__dashboardUid__: vg-charlie") {
		t.Errorf("Emit did not carry the real dashboard uid through:\n%s", doc)
	}
	wantPanelIDLine := `__panelId__: "` + strconv.Itoa(wantID) + `"`
	if !strings.Contains(doc, wantPanelIDLine) {
		t.Errorf("Emit's __panelId__ (%q) does not match Assemble's own PanelIndex assignment (%d):\n%s", wantPanelIDLine, wantID, doc)
	}
}

// TestEmit_PerRuleDatasourceResolution proves Rule.Datasource resolves:
// a set value renders on refId A (refId C always stays __expr__); unset
// falls back to Alerts.Datasource. Uses vg-loki-errors/vg-pg-saturation's real fields.
func TestEmit_PerRuleDatasourceResolution(t *testing.T) {
	m := &manifest.Model{
		Alerts: manifest.AlertTree{
			Group:      manifest.AlertGroup{Name: "vgkeep", Folder: "vgkeep", Interval: "1m"},
			Datasource: "prometheus",
			Cluster: []manifest.Rule{
				{
					UID: "vg-loki-errors", Title: "Error log spike",
					Expr:         `sum by (service_name) (count_over_time({service_name=~".+"} | severity_text="ERROR" [5m]))`,
					Condition:    "gt 20",
					Instant:      true,
					Range:        "5m",
					For:          "5m",
					NoDataState:  "OK",
					ExecErrState: "Error",
					Severity:     "warn",
					Summary:      "{{ $labels.service_name }} logged more than 20 errors in 5 minutes",
					Runbook:      "stack.md#3-error-log-spike",
					Datasource:   "loki",
				},
				{
					UID: "vg-pg-saturation", Title: "Postgres connections above 80 percent of max",
					Expr:         "sum by (service) (pg_stat_activity_count) / max by (service) (pg_settings_max_connections)",
					Condition:    "gt 0.8",
					Instant:      true,
					Range:        "10m",
					For:          "5m",
					NoDataState:  "OK",
					ExecErrState: "Error",
					Severity:     "warn",
					Summary:      "{{ $labels.service }} is using more than 80 percent of max_connections",
					Runbook:      "stack.md#6-postgres-connections-above-80-percent-of-max",
					// Datasource left unset - must fall back to the tree default.
				},
			},
		},
	}

	got, err := alerts.Emit(m, dashboards.PanelIndex{})
	if err != nil {
		t.Fatalf("Emit: unexpected error: %v", err)
	}
	doc := string(got)

	lokiBlock := ruleBlock(t, doc, "vg-loki-errors")
	if !strings.Contains(lokiBlock, "datasourceUid: loki") {
		t.Errorf("vg-loki-errors: refId A datasourceUid should be the rule's own override (loki):\n%s", lokiBlock)
	}
	if !strings.Contains(lokiBlock, "datasourceUid: __expr__") {
		t.Errorf("vg-loki-errors: refId C datasourceUid must stay __expr__ regardless of the rule's datasource override:\n%s", lokiBlock)
	}
	if strings.Contains(lokiBlock, "datasourceUid: prometheus") {
		t.Errorf("vg-loki-errors: must not fall back to the tree default once it sets its own datasource:\n%s", lokiBlock)
	}

	pgBlock := ruleBlock(t, doc, "vg-pg-saturation")
	if !strings.Contains(pgBlock, "datasourceUid: prometheus") {
		t.Errorf("vg-pg-saturation: refId A datasourceUid should fall back to the tree default (prometheus):\n%s", pgBlock)
	}
	if strings.Contains(pgBlock, "datasourceUid: loki") {
		t.Errorf("vg-pg-saturation: must not pick up the sibling rule's datasource override:\n%s", pgBlock)
	}
}

// TestEmit_ServiceWithNoRules proves a service with empty Golden/Alerts
// (nil map/slice) contributes nothing and produces no error.
func TestEmit_ServiceWithNoRules(t *testing.T) {
	m := &manifest.Model{
		Alerts: manifest.AlertTree{
			Group:      manifest.AlertGroup{Name: "vgkeep", Folder: "vgkeep", Interval: "1m"},
			Datasource: "prometheus",
			Cluster: []manifest.Rule{
				{
					UID: "vg-pod-churn", Title: "Pod restart churn or OOM kill",
					Expr: "up", Condition: "gt 0", Instant: true, Range: "5m", For: "5m",
					NoDataState: "OK", ExecErrState: "Error", Severity: "warn",
					Summary: "a pod is restart-churning or was OOM-killed", Runbook: "stack.md#x",
				},
			},
			Services: []manifest.ServiceAlerts{
				{Service: "empty-service"}, // Golden and Alerts both left at their nil zero value.
			},
		},
	}

	got, err := alerts.Emit(m, dashboards.PanelIndex{})
	if err != nil {
		t.Fatalf("Emit: unexpected error: %v", err)
	}
	doc := string(got)

	if strings.Contains(doc, "empty-service") {
		t.Errorf("a service with no golden instantiations and no custom alerts must contribute no text at all:\n%s", doc)
	}
	if !strings.Contains(doc, "uid: vg-pod-churn") {
		t.Errorf("the cluster rule must still be present:\n%s", doc)
	}
	if n := strings.Count(doc, "\n      - uid:"); n != 1 {
		t.Errorf("want exactly one rule (the cluster rule only); a service with no rules must add zero: got %d rule(s)\n%s", n, doc)
	}
}

// TestEmit_Errors tables every way an expanded rule can fail to
// resolve; the last case (idx entry with no matching model dashboard)
// cannot arise from a real Assemble call but Emit must still refuse it.
func TestEmit_Errors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*manifest.Model, dashboards.PanelIndex)
		want   []string
	}{
		{
			name: "condition is not two whitespace-separated tokens",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Cluster[0].Condition = "not-a-number"
			},
			want: []string{"vg-pod-churn"},
		},
		{
			name: "condition's second token does not parse as a number",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Cluster[0].Condition = "gt abc"
			},
			want: []string{"vg-pod-churn", "abc"},
		},
		{
			// distinct from the cases above: svc.Alerts is a separate loop
			// in expandRules, with its own error-collection call site.
			name: "a service's own custom rule has an unparseable condition",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Services[0].Alerts[0].Condition = "not-a-number"
			},
			want: []string{"vg-alpha-queue-backlog"},
		},
		{
			// exercises expandGoldenInstance's own error return, not reached by any case above.
			name: "a service's golden instantiation has an unparseable condition after override",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Services[0].Golden["availability"] = manifest.Overrides{Condition: "not-a-number"}
			},
			want: []string{"vg-alpha-down"},
		},
		{
			name: "custom rule has no range set",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Cluster[0].Range = ""
			},
			want: []string{"vg-pod-churn"},
		},
		{
			name: "custom rule's range does not parse as a duration",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Cluster[0].Range = "not-a-duration"
			},
			want: []string{"vg-pod-churn", "not-a-duration"},
		},
		{
			name: "panel_ref has no slash separating service from title",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Services[0].Alerts[0].PanelRef = "alpha-no-slash"
			},
			want: []string{"vg-alpha-queue-backlog", "malformed panel_ref", "alpha-no-slash"},
		},
		{
			name: "panel_ref names a service missing from the panel index",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Services[0].Alerts[0].PanelRef = "charlie/Availability"
			},
			want: []string{"vg-alpha-queue-backlog", "charlie", "no dashboard"},
		},
		{
			name: "panel_ref names a title missing from that service's panel index",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Services[0].Alerts[0].PanelRef = "alpha/Does not exist"
			},
			want: []string{"vg-alpha-queue-backlog", "alpha", "Does not exist"},
		},
		{
			name: "panel_ref names a service present in the index but absent from the model's dashboards",
			mutate: func(m *manifest.Model, idx dashboards.PanelIndex) {
				idx["delta"] = map[string]int{"Some Panel": 1}
				m.Alerts.Services[0].Alerts[0].PanelRef = "delta/Some Panel"
			},
			want: []string{"vg-alpha-queue-backlog", "delta", "no dashboard uid"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureModel()
			idx := fixtureIndex()
			tc.mutate(m, idx)

			_, err := alerts.Emit(m, idx)
			if err == nil {
				t.Fatal("Emit: want an error, got nil")
			}
			msg := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("Emit error = %q, want it to contain %q", msg, want)
				}
			}
		})
	}
}
