// Package lint validates a loaded observability manifest (and the repo
// content it references - runbook anchors, the runbook index,
// registered metric names) beyond what internal/manifest.Load's strict
// decode already enforces.
// Run never mutates anything and never touches deploy/charts/platform;
// it only reports what it finds, leaving main.go to decide the process
// exit code. It deliberately does not re-check anything Load already
// enforces (e.g. the alerts/golden.yaml services roster's alphabetical
// order); its own uid-uniqueness and retired/live-overlap checks are
// not a duplicate of Load's despite the similar name - Load's own
// checkUIDs explicitly excludes golden-template uids (they still carry
// an unexpanded {service} placeholder at load time), so a collision
// that only exists after {service} substitution - the exact form every
// generated rule actually ships in - is a gap only this package closes
// (see checkUIDs below).
//
// The unknown-metric check's jurisdiction is deliberately narrow: it
// only ever flags a vg_-prefixed selector name (see vgJurisdictionPrefix).
// Its purpose is catching a vg_-owned metric rename - a rule or panel
// still citing a name nothing registers anymore, the walk->step
// incident that motivated this whole package - not policing every
// exporter/runtime/kube series (http_server_..., kube_..., node_...,
// up, otelcol_...) this repo emits or scrapes but never registers by
// hand. external_metric_prefixes is still consulted for anything it
// matches regardless of that name's own prefix; only a name outside
// both known/prefixes AND the vg_ family is silently out of scope
// rather than a finding.
//
// Placeholder checks scan whole fragments, not only titles; Grafana
// legendFormat variables ({{label}}) are stripped before the scan so
// they never read as unsubstituted placeholders.
package lint

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/expand"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// Finding is one problem Run found. Path locates it: a manifest source
// file (by the fixed deploy/observability/ layout every manifest
// package in this module already assumes, e.g. "alerts/cluster.yaml")
// for a model-level check, or a real repo-relative file (e.g.
// "docs/runbooks/stack.md") for a check that reads one directly. Rule
// is a short kebab-case id naming which check fired (e.g.
// "unknown-metric"), stable across runs so tooling could filter on it.
// Message is the human-readable detail.
type Finding struct {
	Path    string
	Rule    string
	Message string
}

// Run checks m against every rule this package owns, plus repoRoot-
// relative content the manifest references (runbook anchors, every
// registered metric name via Known, and the shipped dashboard files
// under repoRoot's deploy/charts/platform/files/dashboards). Query/
// metric-name checks (checkRuleQuery, checkPanelQuery, checkRunbookDocs,
// checkDashboardFiles) are skipped - after one metric-scan-error
// Finding - if Known itself fails: with no real known set, every one of
// those checks would otherwise report every legitimate metric as
// unknown, drowning the one real problem (the scan failure) in a flood
// of false positives. Every other check (uid uniqueness, unresolved
// placeholders, panel_ref resolution, runbook anchor existence) does
// not depend on Known and still runs.
func Run(m *manifest.Model, repoRoot string) []Finding {
	var findings []Finding

	findings = append(findings, checkGoldenTemplates(m)...)
	findings = append(findings, checkGoldenBlocks(m)...)

	items := expandItems(m)
	findings = append(findings, checkUIDs(items, m.Alerts.Retired)...)

	panels := collectPanels(m)

	headingCache := map[string]map[string]bool{}
	// Loaded once, outside the per-item loop, the same reasoning as
	// Known below: if the index itself cannot be read, reporting that
	// once and skipping checkRunbookIndexRow is more useful than
	// flooding the output with a "missing row" finding for every rule.
	indexUIDs, indexErr := runbookIndexUIDs(repoRoot)
	if indexErr != nil {
		findings = append(findings, Finding{
			Path: "docs/runbooks/README.md", Rule: "runbook-index-error",
			Message: indexErr.Error(),
		})
	}
	for _, it := range items {
		findings = append(findings, checkPlaceholders(it)...)
		findings = append(findings, checkPanelRef(it, panels)...)
		findings = append(findings, checkRunbookAnchor(repoRoot, it, headingCache)...)
		if indexErr == nil {
			findings = append(findings, checkRunbookIndexRow(it, indexUIDs)...)
		}
	}
	// Every panel is scanned by its whole substituted (golden) or
	// as-authored (custom) fragment text, not just its parsed title: a
	// typo'd placeholder can just as easily hide inside a quoted label
	// selector (e.g. pod=~"{servce}-.*") as inside a title, and the
	// title-only check used to walk straight past that.
	for _, sd := range m.Dashboards.Services {
		for _, panel := range panels[sd.Service] {
			context := fmt.Sprintf("panel %s/%s", sd.Service, panel.title)
			if fnd := placeholderFinding(panel.source, context, "fragment", panel.fragment); fnd != nil {
				findings = append(findings, *fnd)
			}
		}
	}

	known, err := Known(repoRoot)
	if err != nil {
		findings = append(findings, Finding{Path: repoRoot, Rule: "metric-scan-error", Message: err.Error()})
		return findings
	}
	prefixes := m.Alerts.ExternalMetricPrefixes

	p := parser.NewParser(parser.Options{})
	for _, it := range items {
		findings = append(findings, checkRuleQuery(p, it, known, prefixes)...)
	}
	for _, sd := range m.Dashboards.Services {
		for _, panel := range panels[sd.Service] {
			findings = append(findings, checkPanelQuery(p, panel, sd.Service, known, prefixes)...)
		}
	}
	findings = append(findings, checkRunbookDocs(p, repoRoot, known, prefixes)...)
	findings = append(findings, checkDashboardFiles(filepath.Join(repoRoot, "deploy/charts/platform/files/dashboards"), p, known, prefixes)...)

	return findings
}

// --- expansion: one entry per live rule instance -----------------------

// expandedItem is one live rule instance, fully resolved: {service}/
// {Service} substituted (for a golden template instantiation) or taken
// as authored (for a cluster or per-service custom rule), and
// datasource resolved against the tree default. sourcePath is the
// manifest file the rule lives in, by the fixed convention
// internal/manifest.Load itself reads from (this package does not
// carry that path on the model).
type expandedItem struct {
	uid        string
	title      string
	expr       string
	summary    string
	panelRef   string
	runbook    string
	datasource string
	sourcePath string
}

// expandItems walks every alert the manifest declares - cluster rules,
// then each service's golden template instantiations (sorted by
// template name - ServiceAlerts.Golden is a Go map) followed by its
// custom rules - mirroring internal/alerts.expandRules' and internal/
// dashboards.expandAlerts' own walk order. A golden key naming an
// unknown template is skipped here (checkGoldenTemplates reports it
// separately) rather than expanding a zero-value Template, which would
// produce a confusing empty-string uid/expr finding instead of the one
// real problem.
func expandItems(m *manifest.Model) []expandedItem {
	var out []expandedItem
	treeDatasource := m.Alerts.Datasource

	for _, r := range m.Alerts.Cluster {
		out = append(out, ruleItem(r, treeDatasource, "alerts/cluster.yaml"))
	}
	for _, svc := range m.Alerts.Services {
		path := "alerts/" + svc.Service + ".yaml"

		for _, name := range slices.Sorted(maps.Keys(svc.Golden)) {
			tmpl, ok := m.Alerts.Templates[name]
			if !ok {
				continue
			}
			out = append(out, goldenItem(tmpl, svc.Golden[name], svc.Service, treeDatasource, path))
		}
		for _, r := range svc.Alerts {
			out = append(out, ruleItem(r, treeDatasource, path))
		}
	}
	return out
}

func ruleItem(r manifest.Rule, treeDatasource, path string) expandedItem {
	ds := r.Datasource
	if ds == "" {
		ds = treeDatasource
	}
	return expandedItem{
		uid: r.UID, title: r.Title, expr: r.Expr, summary: r.Summary,
		panelRef: r.PanelRef, runbook: r.Runbook, datasource: ds, sourcePath: path,
	}
}

// goldenItem mirrors internal/alerts.expandGoldenInstance's override
// resolution (a zero-value override field means "use the template's
// own value", across all four permitted fields: for, condition,
// severity, summary) but, like internal/dashboards.expandAlerts,
// applies only the overrides relevant to what this package's own
// expandedItem tracks - here that is summary alone. An overridden
// summary is the one override that can carry a {service}/{Service}
// placeholder into the generated rule (summary is one of the five
// fields expand.Substitute ever touches; for/condition/severity are
// never touched by expand.Substitute in expandGoldenInstance either),
// so building the linted item from the template's own default summary
// instead of the override actually in effect would let a typo'd
// override placeholder (e.g. "{servce} down") reach the generated
// vg-rules.yaml with checkPlaceholders none the wiser. for/condition/
// severity get no equivalent treatment: an override on any of them
// can never introduce a placeholder, and no other check in this
// package reads them.
func goldenItem(tmpl manifest.Template, ov manifest.Overrides, service, treeDatasource, path string) expandedItem {
	summary := tmpl.Summary
	if ov.Summary != "" {
		summary = ov.Summary
	}

	return expandedItem{
		uid:      expand.Substitute(tmpl.UID, service),
		title:    expand.Substitute(tmpl.Title, service),
		expr:     expand.Substitute(tmpl.Expr, service),
		summary:  expand.Substitute(summary, service),
		panelRef: expand.Substitute(tmpl.PanelRef, service),
		// Never substituted: a runbook anchor is the same literal string
		// for every service instantiating the template (see internal/
		// alerts.expandGoldenInstance, which resolves runbookShort the
		// same un-substituted way).
		runbook:    tmpl.Runbook,
		datasource: treeDatasource,
		sourcePath: path,
	}
}

// --- uid uniqueness / retired overlap / unknown golden template --------

// checkGoldenTemplates flags a service's golden: map naming a template
// key alerts/golden.yaml never defines - an authoring typo that
// internal/alerts.Emit and internal/dashboards.Assemble both currently
// mishandle (Emit errors with a blank-uid message; Assemble silently
// skips the key), rather than reporting the typo itself.
func checkGoldenTemplates(m *manifest.Model) []Finding {
	var findings []Finding
	for _, svc := range m.Alerts.Services {
		for _, name := range slices.Sorted(maps.Keys(svc.Golden)) {
			if _, ok := m.Alerts.Templates[name]; !ok {
				findings = append(findings, Finding{
					Path:    "alerts/" + svc.Service + ".yaml",
					Rule:    "unknown-golden-template",
					Message: fmt.Sprintf("service %q instantiates unknown golden template %q", svc.Service, name),
				})
			}
		}
	}
	return findings
}

// checkGoldenBlocks flags a dashboards/golden.yaml block that no
// service's golden_blocks ever instantiates - authored content (or
// content every last instantiating service has since dropped) that no
// generated dashboard actually renders. It mirrors checkGoldenTemplates
// from the opposite direction: that check flags a service naming a
// template that does not exist, this one flags a block that exists but
// nothing names.
func checkGoldenBlocks(m *manifest.Model) []Finding {
	used := make(map[string]bool, len(m.Dashboards.Blocks))
	for _, sd := range m.Dashboards.Services {
		for name := range sd.GoldenBlocks {
			used[name] = true
		}
	}

	var findings []Finding
	for _, name := range slices.Sorted(maps.Keys(m.Dashboards.Blocks)) {
		if !used[name] {
			findings = append(findings, Finding{
				Path:    "dashboards/golden.yaml",
				Rule:    "unused-golden-block",
				Message: fmt.Sprintf("block %q is never instantiated by any service's golden_blocks", name),
			})
		}
	}
	return findings
}

// checkUIDs finds every uid collision across the fully expanded (post-
// substitution) live rule set, plus every retired uid that still
// resolves to one of those live uids. This walks a strictly larger set
// than internal/manifest.Load's own checkUIDs, which knowingly excludes
// golden-template uids (they are unique per template key at load time,
// not per literal string - see that function's own doc comment); once
// {service} is substituted, two different template keys - or a
// template instantiation and a hand-authored custom rule - can produce
// the identical literal uid, and that is exactly the collision this
// function exists to catch.
func checkUIDs(items []expandedItem, retired []manifest.RetiredUID) []Finding {
	var findings []Finding
	live := make(map[string]string, len(items))

	for _, it := range items {
		if prev, ok := live[it.uid]; ok {
			findings = append(findings, Finding{
				Path:    it.sourcePath,
				Rule:    "duplicate-uid",
				Message: fmt.Sprintf("uid %q also used in %s (after {service} expansion)", it.uid, prev),
			})
			continue
		}
		live[it.uid] = it.sourcePath
	}

	for _, r := range retired {
		if src, ok := live[r.UID]; ok {
			findings = append(findings, Finding{
				Path:    "alerts/retired.yaml",
				Rule:    "retired-live-collision",
				Message: fmt.Sprintf("retired uid %q still resolves to a live rule in %s", r.UID, src),
			})
		}
	}
	return findings
}

// --- unresolved placeholders --------------------------------------------

// placeholderRE matches a leftover {word} token: expand.Substitute
// only ever replaces the two exact spellings "{service}"/"{Service}"
// via strings.ReplaceAll, which is exhaustive - so if either canonical
// spelling could still be found post-substitution, this check would
// find nothing, ever. The real failure mode is a *misspelled* template
// placeholder (e.g. "{Svc}", "{srv}"): expand.Substitute never touches
// it, so it survives verbatim into the generated output. A bare {word} is
// safe to flag broadly here because it does not collide with a PromQL
// label matcher's braces (they always hold an operator, e.g.
// "{namespace=...}", never a lone word) or, once
// grafanaLegendFormatRE has stripped it first (see placeholderFinding),
// with Grafana's own double-brace legend syntax either.
var placeholderRE = regexp.MustCompile(`\{[A-Za-z]+\}`)

// grafanaLegendFormatRE matches Grafana's own {{label}} legend-format
// template syntax: a completely different mechanism from this
// generator's {service}/{Service} substitution (Grafana expands it
// client-side against each query result's own labels;
// expand.Substitute never touches it), which real dashboards put on
// nearly every timeseries panel target (e.g. "legendFormat": "{{pod}}",
// "legendFormat": "{{http_route}} {{http_response_status_code}}") -
// and which nests a single-brace-shaped span inside it, since "{{pod}}"
// contains the exact substring "{pod}". Scanning a whole panel fragment
// (see the Run loop that calls placeholderFinding on panel.fragment)
// would otherwise mistake every one of those for a leftover {word}
// placeholder; placeholderFinding blanks every {{...}} span out before
// running placeholderRE so a legitimate legend format is never
// mistaken for one.
var grafanaLegendFormatRE = regexp.MustCompile(`\{\{[^{}]*\}\}`)

func placeholderFinding(path, context, field, value string) *Finding {
	m := placeholderRE.FindString(grafanaLegendFormatRE.ReplaceAllString(value, ""))
	if m == "" {
		return nil
	}
	return &Finding{
		Path:    path,
		Rule:    "unresolved-placeholder",
		Message: fmt.Sprintf("%s: %s still contains %s after substitution", context, field, m),
	}
}

func checkPlaceholders(it expandedItem) []Finding {
	context := "rule " + it.uid
	fields := []struct{ name, value string }{
		{"uid", it.uid}, {"title", it.title}, {"expr", it.expr},
		{"summary", it.summary}, {"panel_ref", it.panelRef},
	}
	var findings []Finding
	for _, f := range fields {
		if fnd := placeholderFinding(it.sourcePath, context, f.name, f.value); fnd != nil {
			findings = append(findings, *fnd)
		}
	}
	return findings
}

// --- panels: collection + panel_ref resolution --------------------------

// panelSpec is the minimal shape lint reads out of one assembled
// panel: its title (the identifier panel_ref resolves against) and
// every query its targets carry. source is the manifest file it came
// from, for Finding.Path. fragment is the panel's whole fragment text
// - substituted for a golden panel, as-authored for a custom one -
// kept alongside the parsed fields so checkPlaceholders' whole-fragment
// scan (see the Run loop that reads it) has the exact same text
// parsePanelSpec itself decoded, not a re-serialization of it.
type panelSpec struct {
	title    string
	targets  []panelTarget
	source   string
	fragment string
}

// panelTarget is one query on a panel, with the datasource it will
// actually run against already resolved (see resolvePanelDatasource) -
// the same routing input checkRuleQuery reads off an expandedItem.
type panelTarget struct {
	expr       string
	datasource string
}

// panelJSON decodes just enough of a verbatim Grafana panel fragment to
// build a panelSpec. Grafana carries a datasource on the panel and,
// independently, on each of its targets (real dashboards under
// deploy/charts/platform/files/dashboards/ set both, to the same value
// on a Prometheus panel and to loki on the logs panel every service
// dashboard ends with); a target that names one overrides its panel.
type panelJSON struct {
	Title      string        `json:"title"`
	Datasource datasourceRef `json:"datasource"`
	Targets    []struct {
		Expr       string        `json:"expr"`
		Datasource datasourceRef `json:"datasource"`
	} `json:"targets"`
}

// datasourceRef is a Grafana datasource reference reduced to its type
// name. It decodes tolerantly on purpose: today's dashboards all use
// the object form ({"type": "prometheus", "uid": "prometheus"}), but
// Grafana also accepts a bare string naming a datasource or variable,
// and a strict decode would reject that whole panel fragment - taking
// its title (and so every panel_ref pointing at it) down with it over
// one field this package reads for routing only. Anything else,
// including null, leaves the type empty, which resolves to the
// inherited/default datasource.
type datasourceRef struct {
	typ string
}

func (d *datasourceRef) UnmarshalJSON(data []byte) error {
	var obj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		d.typ = obj.Type
		return nil
	}
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		d.typ = name
	}
	return nil
}

// collectPanels builds every service's panel set: every golden block it
// instantiates (substituted per service, via expand.Blocks - mirroring
// internal/dashboards' buildAllPanels) plus that service's own custom
// panels (already concrete). A fragment that fails to parse as JSON is
// silently skipped here - internal/dashboards.Assemble already reports a
// malformed fragment clearly (with the offending service and panel
// context) the moment anyone actually runs gen, and this package's own
// job is query/anchor/uid hygiene on panels it CAN read, not JSON
// validity.
func collectPanels(m *manifest.Model) map[string][]panelSpec {
	out := make(map[string][]panelSpec, len(m.Dashboards.Services))
	for _, sd := range m.Dashboards.Services {
		out[sd.Service] = nil
	}

	for _, bp := range expand.Blocks(m) {
		source := fmt.Sprintf("golden.yaml block %s", bp.Block)
		if spec, ok := parsePanelSpec(bp.Fragment, source); ok {
			out[bp.Service] = append(out[bp.Service], spec)
		}
	}
	for _, sd := range m.Dashboards.Services {
		customPath := "dashboards/" + sd.Service + ".yaml"
		for _, raw := range sd.CustomPanels {
			if spec, ok := parsePanelSpec(string(raw), customPath); ok {
				out[sd.Service] = append(out[sd.Service], spec)
			}
		}
	}
	return out
}

func parsePanelSpec(raw, source string) (panelSpec, bool) {
	var pj panelJSON
	if err := json.Unmarshal([]byte(raw), &pj); err != nil {
		return panelSpec{}, false
	}
	targets := make([]panelTarget, 0, len(pj.Targets))
	for _, t := range pj.Targets {
		if t.Expr == "" {
			continue
		}
		targets = append(targets, panelTarget{
			expr:       t.Expr,
			datasource: resolvePanelDatasource(t.Datasource.typ, pj.Datasource.typ),
		})
	}
	return panelSpec{title: pj.Title, targets: targets, source: source, fragment: raw}, true
}

// resolvePanelDatasource picks the datasource one target's query
// actually runs against: its own, else the panel's, else Prometheus.
// The default matches how Grafana itself treats a panel that names no
// datasource (it inherits the dashboard's, which is Prometheus for
// every dashboard this repo generates) and how a rule with no
// datasource of its own falls back to the alert tree's.
func resolvePanelDatasource(target, panel string) string {
	if target != "" {
		return target
	}
	if panel != "" {
		return panel
	}
	return promDatasource
}

// checkPanelRef reports an unresolvable panel_ref: a malformed
// "service/title" shape, a service with no dashboard at all, or a
// service whose dashboard has no panel with that exact title.
func checkPanelRef(it expandedItem, panels map[string][]panelSpec) []Finding {
	if it.panelRef == "" {
		return nil
	}
	service, title, ok := strings.Cut(it.panelRef, "/")
	if !ok {
		return []Finding{{
			Path: it.sourcePath, Rule: "unresolved-panel-ref",
			Message: fmt.Sprintf("rule %s: malformed panel_ref %q (want \"service/title\")", it.uid, it.panelRef),
		}}
	}
	svcPanels, ok := panels[service]
	if !ok {
		return []Finding{{
			Path: it.sourcePath, Rule: "unresolved-panel-ref",
			Message: fmt.Sprintf("rule %s: panel_ref %q: service %q has no dashboard", it.uid, it.panelRef, service),
		}}
	}
	for _, p := range svcPanels {
		if p.title == title {
			return nil
		}
	}
	return []Finding{{
		Path: it.sourcePath, Rule: "unresolved-panel-ref",
		Message: fmt.Sprintf("rule %s: panel_ref %q: service %q has no panel titled %q", it.uid, it.panelRef, service, title),
	}}
}

// --- query validity and names ---------------------------------------------

// metricTokenRE is the fallback scan for content that is not PromQL: a
// LogQL rule expr, or a runbook line quoting a query inside a shell
// command. It looks for anything metric-shaped rather than trying to
// parse the surrounding syntax at all.
var metricTokenRE = regexp.MustCompile(`vg_[a-z0-9_]+`)

func tokenScanNames(text string) []string {
	return metricTokenRE.FindAllString(text, -1)
}

// selectorNames walks expr's AST and returns every vector selector's
// metric name. A selector named only via a __name__ regex or negated
// matcher (e.g. {__name__=~"otelcol_exporter_.*"}, used by the real
// vg-collector-drops rule) has an empty Name - PromQL's parser only
// populates Name for a bare identifier or an exact __name__ match, both
// literal - and is skipped: it does not name one specific metric, so
// there is nothing to check it against.
func selectorNames(expr parser.Expr) []string {
	var names []string
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if vs, ok := node.(*parser.VectorSelector); ok && vs.Name != "" {
			names = append(names, vs.Name)
		}
		return nil
	})
	return names
}

func isKnownMetric(name string, known map[string]struct{}, prefixes []string) bool {
	if _, ok := known[name]; ok {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// vgJurisdictionPrefix is the only metric-name family unresolvedMetric
// ever flags - see the package doc comment for why (in short: this
// check exists to catch a vg_-owned metric rename, not to police every
// series this repo's dependencies happen to emit).
const vgJurisdictionPrefix = "vg_"

// unresolvedMetric reports whether name is a genuine unknown-metric
// finding: absent from known/prefixes (isKnownMetric) AND inside this
// lint's own vg_ jurisdiction. A name outside that jurisdiction is
// skipped regardless of whether it is known - it was never eligible to
// be flagged in the first place.
func unresolvedMetric(name string, known map[string]struct{}, prefixes []string) bool {
	if isKnownMetric(name, known, prefixes) {
		return false
	}
	return strings.HasPrefix(name, vgJurisdictionPrefix)
}

// selectorFindings reports every named vector selector in an already-
// parsed query that unresolvedMetric flags (absent from known/prefixes
// AND vg_-prefixed) - a non-vg_ selector (http_server_..., kube_...,
// up, ...) is outside this check's jurisdiction and is silently
// skipped, known or not.
func selectorFindings(parsed parser.Expr, path, context string, known map[string]struct{}, prefixes []string) []Finding {
	var findings []Finding
	for _, name := range selectorNames(parsed) {
		if unresolvedMetric(name, known, prefixes) {
			findings = append(findings, Finding{
				Path: path, Rule: "unknown-metric",
				Message: fmt.Sprintf("%s: metric %q is not a known registration or external prefix", context, name),
			})
		}
	}
	return findings
}

// checkQueryExpr AST-parses expr as PromQL exactly as authored: a parse
// failure is itself the finding (expr-parse-error); on success, every
// selector selectorFindings flags is its own unknown-metric finding.
// Used for a rule expr resolved to the "prometheus" datasource -
// deliberately with no Grafana-variable tolerance, since a Grafana
// alert rule's query reaches the datasource as authored (see
// substituteGrafanaVars).
func checkQueryExpr(p parser.Parser, expr, path, context string, known map[string]struct{}, prefixes []string) []Finding {
	parsed, err := p.ParseExpr(expr)
	if err != nil {
		return []Finding{{Path: path, Rule: "expr-parse-error", Message: fmt.Sprintf("%s: %v", context, err)}}
	}
	return selectorFindings(parsed, path, context, known, prefixes)
}

// checkPanelExpr is checkQueryExpr's dashboard-panel sibling: it parses
// a Grafana-variable-substituted copy of expr, while every finding
// still quotes the expr as authored - the parser only ever sees the
// substituted text, and the substitution shifts the column numbers a
// parse error reports, so the authored expr is spelled out in the
// message rather than left for the reader to guess at.
func checkPanelExpr(p parser.Parser, expr, path, context string, known map[string]struct{}, prefixes []string) []Finding {
	parsed, err := p.ParseExpr(substituteGrafanaVars(expr))
	if err != nil {
		return []Finding{{
			Path: path, Rule: "expr-parse-error",
			Message: fmt.Sprintf("%s: %v (expr: %s)", context, err, expr),
		}}
	}
	return selectorFindings(parsed, path, context, known, prefixes)
}

// Grafana expands its own template variables client-side before a panel
// query ever reaches Prometheus, so a panel expr is not standalone
// PromQL and promql/parser rejects the raw $-tokens on sight. These two
// patterns rewrite those tokens into syntactically valid stand-ins
// purely so the surrounding query can be parsed and its metric names
// walked; the substituted text is never reported and never written
// anywhere.
//
// Substitution table (the forms real dashboards under
// deploy/charts/platform/files/dashboards/ actually carry):
//
//	$__rate_interval / $__interval / $__range   -> 5m           (also their ${...} spelling)
//	any other $var, ${var} or ${var:format}     -> grafana_var
//
// The first group is exactly the macros whose value is a duration, and
// they only ever stand where PromQL demands one (a range selector's
// [...]); 5m is an arbitrary valid duration. A macro spelled with a
// trailing _ms/_s carries a number rather than a duration, and the
// trailing word boundary keeps it out of that group. Everything else
// becomes a bare identifier, the one stand-in that stays valid in every
// position real dashboards put a variable in: a quoted label-matcher
// value (namespace="$namespace", pod=~"$pod") accepts any literal, and
// an identifier also keeps a bare selector or a grouping label
// parseable. The identifier is deliberately not vg_-prefixed, so it can
// never be mistaken for a metric of this repo's own (see
// unresolvedMetric).
var (
	grafanaDurationMacroRE = regexp.MustCompile(`\$(?:__rate_interval|__interval|__range)\b|\$\{(?:__rate_interval|__interval|__range)\}`)
	grafanaVarRE           = regexp.MustCompile(`\$\{[^{}]*\}|\$[A-Za-z_][A-Za-z0-9_]*`)
)

const (
	macroDuration   = "5m"
	macroIdentifier = "grafana_var"
)

func substituteGrafanaVars(expr string) string {
	expr = grafanaDurationMacroRE.ReplaceAllString(expr, macroDuration)
	return grafanaVarRE.ReplaceAllString(expr, macroIdentifier)
}

// checkQueryTokens is checkQueryExpr's non-PromQL sibling: no parse
// step, no parse-error finding - just a token scan, with every
// unresolvedMetric-flagged token its own unknown-metric finding
// (tokenScanNames' own vg_[a-z0-9_]+ regex already only ever finds
// vg_-prefixed tokens, so the jurisdiction half of unresolvedMetric is
// a no-op here in practice; using the same function as checkQueryExpr
// regardless keeps both paths gated identically rather than relying on
// that regex never changing). Used for a rule expr or a panel target
// resolved to any datasource other than "prometheus" and, via
// checkRunbookBlock, for any runbook fenced block that does not parse
// as PromQL at all.
func checkQueryTokens(text, path, context string, known map[string]struct{}, prefixes []string) []Finding {
	var findings []Finding
	for _, name := range tokenScanNames(text) {
		if unresolvedMetric(name, known, prefixes) {
			findings = append(findings, Finding{
				Path: path, Rule: "unknown-metric",
				Message: fmt.Sprintf("%s: token %q is not a known registration or external prefix", context, name),
			})
		}
	}
	return findings
}

// promDatasource is the datasource whose queries are PromQL, and so
// the only one either check below routes to the AST path.
const promDatasource = "prometheus"

// checkRuleQuery routes one rule's expr per its resolved datasource:
// PromQL AST treatment when it resolves to "prometheus" (rule-level
// override, or the tree default), token-scan fallback otherwise (today
// that means exactly one rule, vg-loki-errors, whose expr is LogQL).
func checkRuleQuery(p parser.Parser, it expandedItem, known map[string]struct{}, prefixes []string) []Finding {
	context := "rule " + it.uid
	if it.datasource == promDatasource {
		return checkQueryExpr(p, it.expr, it.sourcePath, context, known, prefixes)
	}
	return checkQueryTokens(it.expr, it.sourcePath, context+" (datasource "+it.datasource+", token-scanned)", known, prefixes)
}

// checkPanelQuery routes each of one panel's target queries the same
// way checkRuleQuery routes a rule's, by the datasource that target
// resolves to: PromQL AST treatment (with Grafana-variable tolerance -
// see checkPanelExpr) for Prometheus, token-scan fallback for anything
// else. Every real dashboard ends with a Loki-datasourced logs panel
// whose expr is LogQL, and a target may name a datasource its panel
// does not.
func checkPanelQuery(p parser.Parser, panel panelSpec, service string, known map[string]struct{}, prefixes []string) []Finding {
	var findings []Finding
	context := fmt.Sprintf("panel %s/%s", service, panel.title)
	for _, t := range panel.targets {
		if t.datasource == promDatasource {
			findings = append(findings, checkPanelExpr(p, t.expr, panel.source, context, known, prefixes)...)
			continue
		}
		findings = append(findings, checkQueryTokens(t.expr, panel.source, context+" (datasource "+t.datasource+", token-scanned)", known, prefixes)...)
	}
	return findings
}

// --- runbook anchors ------------------------------------------------------

// headingLineRE matches one ATX heading line ("#" through "######",
// then whitespace, then the heading text) - applied line by line by
// parseMarkdown, which tracks fenced-block state so a "#"-led line
// inside a fenced block (a shell comment, say) is never mistaken for a
// heading.
var headingLineRE = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)

// parseMarkdown extracts every heading's GitHub slug and every fenced
// (```) code block's body from one runbook file, in a single line-
// oriented pass.
func parseMarkdown(markdown string) (headings map[string]bool, blocks []string) {
	headings = make(map[string]bool)
	lines := strings.Split(markdown, "\n")

	inFence := false
	var current []string

	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				blocks = append(blocks, strings.Join(current, "\n"))
				current = nil
			}
			inFence = !inFence
			continue
		}
		if inFence {
			current = append(current, line)
			continue
		}
		if m := headingLineRE.FindStringSubmatch(line); m != nil {
			headings[githubSlug(m[1])] = true
		}
	}
	return headings, blocks
}

// slugDrop matches every character a GitHub anchor slug removes
// outright (no separator takes its place - "don't" becomes "dont", not
// "don-t"); slugSpace matches the whitespace run replaced by one
// hyphen. Verified against every runbook_url anchor already live in
// deploy/charts/platform/files/alerting/vg-rules.yaml (e.g. "1.
// Service 5xx ratio above 5 percent" -> "1-service-5xx-ratio-above-5-
// percent": the period is dropped, not replaced).
var (
	slugDrop  = regexp.MustCompile(`[^a-z0-9 _-]+`)
	slugSpace = regexp.MustCompile(`\s+`)
)

func githubSlug(heading string) string {
	s := strings.ToLower(heading)
	s = slugDrop.ReplaceAllString(s, "")
	s = slugSpace.ReplaceAllString(s, "-")
	return s
}

// headingsFor reads and slugs repoRoot/docs/runbooks/file, caching the
// result: a runbook like stack.md is cited by many rules, and re-
// reading/re-parsing it once per citing rule would be wasted work.
func headingsFor(repoRoot, file string, cache map[string]map[string]bool) (map[string]bool, error) {
	if got, ok := cache[file]; ok {
		return got, nil
	}
	path := filepath.Join(repoRoot, "docs", "runbooks", file)
	data, err := os.ReadFile(path) //nolint:gosec // G304: file is the manifest's own trusted, repo-authored runbook field, the same threat model internal/manifest.decodeFile already documents for its own file reads.
	if err != nil {
		return nil, err
	}
	headings, _ := parseMarkdown(string(data))
	cache[file] = headings
	return headings, nil
}

// checkRunbookAnchor reports a rule whose runbook value has no
// "#anchor" suffix, names a docs/runbooks file that does not exist or
// cannot be read, or names a file with no heading slugging to that
// anchor.
func checkRunbookAnchor(repoRoot string, it expandedItem, cache map[string]map[string]bool) []Finding {
	file, anchor, ok := strings.Cut(it.runbook, "#")
	if !ok {
		return []Finding{{
			Path: it.sourcePath, Rule: "runbook-anchor-missing",
			Message: fmt.Sprintf("rule %s: runbook %q has no #anchor", it.uid, it.runbook),
		}}
	}
	headings, err := headingsFor(repoRoot, file, cache)
	if err != nil {
		return []Finding{{
			Path: it.sourcePath, Rule: "runbook-anchor-missing",
			Message: fmt.Sprintf("rule %s: docs/runbooks/%s: %v", it.uid, file, err),
		}}
	}
	if !headings[anchor] {
		return []Finding{{
			Path: it.sourcePath, Rule: "runbook-anchor-missing",
			Message: fmt.Sprintf("rule %s: docs/runbooks/%s has no heading matching anchor %q", it.uid, file, anchor),
		}}
	}
	return nil
}

// --- runbook index row ---------------------------------------------------

// runbookIndexRowRE matches one data row of docs/runbooks/README.md's
// hand-maintained alert table by its Rule cell, which is always
// "<uid> - <title>" (e.g. "vg-service-5xx - Service 5xx ratio above 5
// percent"). The captured group requires the literal "vg-" prefix
// every real uid carries, so neither the header row ("| Rule | ... |")
// nor the "|---|...|" separator row - neither of which starts a cell
// with "vg-" - is ever mistaken for a data row.
var runbookIndexRowRE = regexp.MustCompile(`(?m)^\|\s*(vg-[a-z0-9-]+)\s+-\s+`)

// runbookIndexUIDs reads repoRoot/docs/runbooks/README.md and returns
// the set of rule uids its alert table carries a row for.
func runbookIndexUIDs(repoRoot string) (map[string]bool, error) {
	path := filepath.Join(repoRoot, "docs", "runbooks", "README.md")
	data, err := os.ReadFile(path) //nolint:gosec // G304: repoRoot is main's own trusted <repo-root> CLI argument (see runLint), the same threat model this package's other repoRoot-rooted reads already document (e.g. headingsFor, checkRunbookDocs).
	if err != nil {
		return nil, err
	}
	uids := make(map[string]bool)
	for _, m := range runbookIndexRowRE.FindAllStringSubmatch(string(data), -1) {
		uids[m[1]] = true
	}
	return uids, nil
}

// checkRunbookIndexRow reports an expanded rule with no row in
// docs/runbooks/README.md's alert table - the index a reader lands on
// from Grafana before diving into a service runbook. Nothing else
// notices that table going stale when a rule is added.
func checkRunbookIndexRow(it expandedItem, indexUIDs map[string]bool) []Finding {
	if indexUIDs[it.uid] {
		return nil
	}
	return []Finding{{
		Path: it.sourcePath, Rule: "runbook-index-row-missing",
		Message: fmt.Sprintf("rule %s: no row in docs/runbooks/README.md's alert table", it.uid),
	}}
}

// checkRunbookBlock is checkRunbookDocs' per-block treatment: AST
// treatment when block happens to parse as PromQL, token-scan fallback
// otherwise (a shell line quoting a query, or simply prose/bash/
// mermaid content that is not a query at all). Unlike checkQueryExpr,
// a parse failure here is never itself a finding - most fenced blocks
// in a runbook are not PromQL and that is expected, not an error; only
// an unresolvedMetric-flagged name/token (vg_-prefixed and unknown),
// found either way, is reported - same jurisdiction as checkQueryExpr/
// checkQueryTokens, so a runbook example quoting a real but non-vg_
// series (up, kube_..., http_server_...) never fires here either.
func checkRunbookBlock(p parser.Parser, block, path string, known map[string]struct{}, prefixes []string) []Finding {
	if parsed, err := p.ParseExpr(block); err == nil {
		return selectorFindings(parsed, path, "fenced query", known, prefixes)
	}
	return checkQueryTokens(block, path, "fenced query (not valid PromQL, token-scanned)", known, prefixes)
}

// checkRunbookDocs scans every docs/runbooks/*.md file's fenced code
// blocks, independent of which rules cite that file - the walk->step
// rename incident this whole check exists to catch would have shown up
// as a runbook query citing a metric name nothing registers anymore,
// whether or not any live rule still pointed at that file/anchor.
func checkRunbookDocs(p parser.Parser, repoRoot string, known map[string]struct{}, prefixes []string) []Finding {
	paths, err := filepath.Glob(filepath.Join(repoRoot, "docs", "runbooks", "*.md"))
	if err != nil || len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)

	var findings []Finding
	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from filepath.Glob over a fixed, repo-relative pattern, never external input.
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			rel = path
		}
		_, blocks := parseMarkdown(string(data))
		for _, block := range blocks {
			findings = append(findings, checkRunbookBlock(p, block, rel, known, prefixes)...)
		}
	}
	return findings
}
