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

// update regenerates testdata/comprehensive.yaml.golden from Emit's
// actual output instead of comparing against it:
// `go test -run TestEmit_ComprehensiveFixture -update`. Every use
// requires re-reading the diff and reviewing it before committing - this
// flag records verified-correct output, it does not decide correctness.
var update = flag.Bool("update", false, "write actual Emit output to the golden files instead of comparing")

// TestEmit_RealRuleByteFixture proves byte-exact fidelity against a real
// rule: a manifest.Rule built from vg-service-5xx's real fields (copied
// verbatim from today's deploy/charts/platform/files/alerting/vg-rules.yaml,
// lines 1-35) must produce output byte-identical to that same slice of
// the real file -
// one group, one rule, no retired entries so no deleteRules stanza. This
// is the one place a hand-authored file, not Emit's own prior output, is
// the source of truth.
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

// TestEmit_ServiceDisplayNameAcronym pins the owner-ruled exception to
// capitalize's plain first-letter-uppercase fallback: "bff" is an
// acronym, so its {Service} form must render "BFF", not "Bff" - "auth"
// stands in for the general case, which still gets plain capitalize
// ("Auth"). Reuses templates()' real pdb_budget entry (title:
// "{Service} disruption budget exhausted", the exact field this
// exception was ruled for) rather than a one-off fixture; pdb_budget
// carries no panel_ref (see templates()' own comment), so this model
// needs no dashboards/PanelIndex setup at all. pdb_budget's expr field
// carries the {service} lowercase form too (the poddisruptionbudget
// selector), checked here in the same pass to prove the acronym
// exception is scoped to {Service} only - substitute's lowercase
// ReplaceAll is untouched by this change.
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
// "pdb_budget") with field values that mirror social's real
// vg-social-down rule and each service's real disruption-budget guard,
// reused as-is rather than invented, so the fixture's golden-path
// behavior tracks the real templates' actual field shape.
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
			// no PanelRef: proves a golden-template instantiation with no
			// panel_ref gains no dashboard-link annotations, same as a
			// custom rule.
		},
	}
}

// fixtureModel builds a two-service (alpha, bravo), two-cluster-rule
// manifest exercising every ordering and dashboard-link rule Emit owns:
//   - cluster rules before any service, in their own manifest order
//     (vg-pod-churn then vg-node-pressure - reversed from alphabetical,
//     so an accidental alpha-sort would be caught).
//   - services in roster order (alpha, bravo - already alphabetical,
//     matching the real six-service roster's own order, per the
//     resolution that Emit trusts manifest/roster order rather than
//     re-sorting, consistent with dashboards.Assemble's own precedent).
//   - within alpha, both golden templates (sorted by template name:
//     "availability" before "pdb_budget" even though alpha's own Golden
//     map is written pdb_budget-first below, proving the sort is real)
//     expand before alpha's two custom rules, which keep their own
//     manifest order (queue-backlog before error-rate).
//   - override coverage split across services: alpha's pdb_budget
//     overrides only For (1h -> 2h); bravo's availability overrides
//     Condition/Severity/Summary (lt 1 -> lt 2, crit -> warn, template
//     summary -> a distinct override summary) but leaves For at the
//     template default (5m) - between the two services every one of the
//     four permitted override fields is exercised at least once, and at
//     least one field per case is deliberately left at the template
//     default to prove overrides are field-by-field, not all-or-nothing.
//   - the dashboard-link annotations appear both ways, on both rule
//     origins: alpha's availability instantiation and alpha's
//     queue-backlog custom rule both carry a panel_ref (golden-derived
//     and custom-derived positive cases); alpha's pdb_budget
//     instantiation and alpha's error-rate custom rule both omit one
//     (golden-derived and custom-derived negative cases). bravo's
//     availability instantiation is a second positive case on a second
//     service, proving dashboardUid varies per service rather than
//     being hardcoded.
//   - two retired uids in a deliberately non-alphabetical order
//     (old-thing-2 before old-thing-1), proving deleteRules preserves
//     manifest order rather than sorting.
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

// fixtureIndex is the PanelIndex fixtureModel's panel_refs resolve
// against, built by hand (not via dashboards.Assemble) so this test
// stays isolated to Emit's own behavior - TestEmit_WithRealAssemble
// covers the real cross-package handoff separately.
func fixtureIndex() dashboards.PanelIndex {
	return dashboards.PanelIndex{
		"alpha": {"Availability": 1, "Alpha queue depth": 2},
		"bravo": {"Availability": 1},
	}
}

// TestEmit_ComprehensiveFixture builds both dashboards from fixtureModel
// and checks every ordering/override/dashboard-link-annotation/
// deleteRules behavior Emit owns structurally before comparing the
// emitted bytes against the committed golden file.
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

	// {service}/{Service} substitution in a golden instantiation's uid,
	// title and expr.
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

	// Positive cases for the dashboard-link annotations: golden-derived
	// (alpha-down) and custom-derived (alpha-queue-backlog) both carry
	// panel_ref and must gain both annotations, on the exact ids/uids
	// fixtureIndex assigns.
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

	// Negative cases for the dashboard-link annotations: no panel_ref
	// means neither annotation appears at all, golden-derived
	// (pdb-exhausted) and custom-derived (error-rate).
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
	want, err := os.ReadFile(goldenName) //nolint:gosec // G304: goldenName is a package-local literal constant, never external input.
	if err != nil {
		t.Fatalf("reading %s: %v", goldenName, err)
	}
	if doc != string(want) {
		t.Errorf("Emit output does not match %s\n--- got ---\n%s\n--- want ---\n%s", goldenName, doc, want)
	}
}

// ruleBlock isolates one rule's own text (from its "uid: <uid>" marker up
// to the next "      - uid:" sibling marker, or end of string for the
// last rule), so an assertion about one rule's fields cannot accidentally
// match another rule's - e.g. "for: 5m" appears on more than one rule in
// fixtureModel, so a bare strings.Contains(doc, ...) would not prove
// which rule it belongs to.
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

// TestEmit_WithRealAssemble proves the real cross-package handoff for
// the dashboard-link annotations: dashboards.Assemble's own PanelIndex
// (not a hand-typed one) feeds alerts.Emit, so the panel id and
// dashboard uid an alert's annotations carry are exactly what the same
// manifest's dashboard assembly actually produced, from the same pass -
// the property PanelIndex's own doc comment ("the two can never drift
// apart") depends on.
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

// TestEmit_PerRuleDatasourceResolution proves Rule.Datasource resolves
// correctly: a rule that sets it renders that value on refId A's
// datasourceUid (refId C's own datasourceUid always stays __expr__
// regardless - it names Grafana's server-side expression engine, never a
// real data source); a rule that leaves it unset falls back to the
// tree-level Alerts.Datasource. Uses vg-loki-errors' and
// vg-pg-saturation's real fields (copied verbatim from today's
// deploy/charts/platform/files/alerting/vg-rules.yaml) so the override
// case is checked against real content, not an invented one - today's
// file has exactly one rule whose datasourceUid differs from every
// other rule's (vg-loki-errors, on loki; every other rule, including
// vg-pg-saturation, on prometheus).
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

// TestEmit_ServiceWithNoRules proves a service with an empty Golden map
// and an empty Alerts slice (no golden instantiations, no custom rules -
// the zero value of ServiceAlerts beyond its own Service field)
// contributes nothing to the output and produces no error, rather than,
// say, an empty rules entry or a spurious failure from ranging a nil map
// or nil slice.
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

// TestEmit_Errors tables every way an expanded rule can fail to resolve:
// malformed/non-numeric conditions, a custom rule missing its required
// range, and every panel_ref resolution failure (malformed shape,
// service missing from the index entirely, title missing from an
// indexed service, and a service present in the index but absent from
// the model's own dashboard list - a hand-built idx/model mismatch that
// could not arise from a real Assemble call on this same model, but
// which Emit must still refuse rather than emit a dashboardUid-less
// annotation).
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
			// Distinct from the two cases above: those mutate a cluster
			// rule, exercising expandRules' cluster loop. A service's own
			// custom rule (svc.Alerts) is a separate loop in expandRules
			// with its own error-collection call site, so it needs its
			// own case to be covered rather than assumed identical.
			name: "a service's own custom rule has an unparseable condition",
			mutate: func(m *manifest.Model, _ dashboards.PanelIndex) {
				m.Alerts.Services[0].Alerts[0].Condition = "not-a-number"
			},
			want: []string{"vg-alpha-queue-backlog"},
		},
		{
			// Distinct again: a golden template instantiation's
			// (post-override) condition failing to parse exercises
			// expandGoldenInstance's own error return and expandRules'
			// per-service golden loop, neither reached by any
			// cluster-rule or custom-rule case above.
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
