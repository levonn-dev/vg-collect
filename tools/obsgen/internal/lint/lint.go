// Package lint validates a loaded manifest (and referenced repo content:
// runbook anchors, the runbook index, registered metric names) beyond
// internal/manifest.Load's decode. Run never mutates anything.
//
// checkUIDs differs from Load's own: Load excludes golden-template uids
// at load time (unique per template key, not per literal string); this
// package catches a post-substitution literal-uid collision instead.
//
// unresolvedMetric only flags a vg_-prefixed name absent from known/
// prefixes - catching a vg_-owned metric rename, not policing every
// series this repo emits but never registers by hand.
//
// Placeholder checks scan whole fragments; Grafana's {{label}} legend
// vars are stripped first so they aren't mistaken for placeholders.
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

// Finding is one problem Run found. Path is a manifest source file
// (e.g. "alerts/cluster.yaml") or a real repo-relative file; Rule is a
// stable kebab-case id (e.g. "unknown-metric"); Message is human-readable.
type Finding struct {
	Path    string
	Rule    string
	Message string
}

// Run checks m plus repoRoot-relative content (runbook anchors, Known
// metric names, shipped dashboard files). If Known itself fails,
// query/metric-name checks are skipped after one metric-scan-error
// finding, to avoid flooding false positives; every other check still runs.
func Run(m *manifest.Model, repoRoot string) []Finding {
	var findings []Finding

	findings = append(findings, checkGoldenTemplates(m)...)
	findings = append(findings, checkGoldenBlocks(m)...)

	items := expandItems(m)
	findings = append(findings, checkUIDs(items, m.Alerts.Retired)...)

	panels := collectPanels(m)

	headingCache := map[string]map[string]bool{}
	// loaded once, outside the loop: if the index can't be read, reporting
	// that once beats flooding a "missing row" finding per rule.
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
	// every panel is scanned by its whole fragment text, not just title: a
	// typo'd placeholder can hide inside a label selector too (e.g. pod=~"{servce}-.*").
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

// expandedItem is one live rule instance, fully resolved (substituted or
// authored) with datasource resolved against the tree default.
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

// expandItems walks cluster rules, then each service's golden
// instantiations (sorted; Golden is a Go map) then custom rules. An
// unknown golden key is skipped here (checkGoldenTemplates reports it),
// not expanded as a zero-value Template.
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

// goldenItem applies only the summary override (of the four permitted
// override fields): summary is the only one expand.Substitute touches,
// so it's the only one that could carry a typo'd placeholder past
// checkPlaceholders if the template default were used instead.
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
		// runbook is never substituted: the same literal string for every service instantiating the template.
		runbook:    tmpl.Runbook,
		datasource: treeDatasource,
		sourcePath: path,
	}
}

// --- uid uniqueness / retired overlap / unknown golden template --------

// checkGoldenTemplates flags a golden: key naming a template that
// doesn't exist - a typo internal/alerts.Emit and internal/
// dashboards.Assemble both mishandle without naming it.
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

// checkGoldenBlocks flags a block no service's golden_blocks
// instantiates, the opposite direction of checkGoldenTemplates.
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
// substitution) live set, plus any retired uid still resolving to a
// live one - a strictly larger set than Load's own checkUIDs, which
// excludes golden-template uids pre-substitution.
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

// placeholderRE matches a leftover {word} token: the real failure mode
// is a *misspelled* placeholder (e.g. "{Svc}"), since expand.Substitute
// only replaces the two canonical spellings exhaustively. Safe to flag
// broadly: PromQL label-matcher braces always hold an operator, never a
// lone word, and Grafana's {{legend}} syntax is stripped first.
var placeholderRE = regexp.MustCompile(`\{[A-Za-z]+\}`)

// grafanaLegendFormatRE matches Grafana's own {{label}} legend syntax
// (a separate mechanism from {service}/{Service} substitution).
// "{{pod}}" contains the substring "{pod}", which would false-positive
// placeholderRE, so placeholderFinding strips it first.
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

// panelSpec is the minimal shape lint reads from one assembled panel.
// fragment keeps the whole source text (not a re-serialization) so the
// placeholder scan reads exactly what was decoded.
type panelSpec struct {
	title    string
	targets  []panelTarget
	source   string
	fragment string
}

// panelTarget is one query on a panel with its datasource already
// resolved (see resolvePanelDatasource), the same routing input checkRuleQuery uses.
type panelTarget struct {
	expr       string
	datasource string
}

// panelJSON decodes just enough of a panel fragment to build a
// panelSpec. A target's own datasource, when set, overrides its panel's.
type panelJSON struct {
	Title      string        `json:"title"`
	Datasource datasourceRef `json:"datasource"`
	Targets    []struct {
		Expr       string        `json:"expr"`
		Datasource datasourceRef `json:"datasource"`
	} `json:"targets"`
}

// datasourceRef is a Grafana datasource reference reduced to its type
// name, decoded tolerantly (object or bare string) so a strict decode
// never rejects a whole panel fragment over one routing-only field.
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

// collectPanels builds every service's panel set: golden blocks (via
// expand.Blocks) plus custom panels. A fragment that fails to parse as
// JSON is silently skipped: Assemble already reports that at gen time;
// this package checks panels it CAN read.
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

// resolvePanelDatasource picks a target's actual datasource: its own,
// else the panel's, else Prometheus (Grafana's own inheritance default).
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

// metricTokenRE is the fallback scan for non-PromQL content (LogQL,
// shell-quoted queries): metric-shaped text, no syntax parsing.
var metricTokenRE = regexp.MustCompile(`vg_[a-z0-9_]+`)

func tokenScanNames(text string) []string {
	return metricTokenRE.FindAllString(text, -1)
}

// selectorNames walks expr's AST for every vector selector's metric
// name. A selector named only via a __name__ regex/negated matcher
// (e.g. {__name__=~"otelcol_.*"}) has an empty Name and is skipped.
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
// ever flags (catches a vg_-owned rename, not every series a dependency emits).
const vgJurisdictionPrefix = "vg_"

// unresolvedMetric reports whether name is absent from known/prefixes
// AND inside the vg_ jurisdiction; outside that jurisdiction is never eligible, known or not.
func unresolvedMetric(name string, known map[string]struct{}, prefixes []string) bool {
	if isKnownMetric(name, known, prefixes) {
		return false
	}
	return strings.HasPrefix(name, vgJurisdictionPrefix)
}

// selectorFindings reports every named selector unresolvedMetric flags;
// a non-vg_ selector is outside jurisdiction and silently skipped.
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

// checkQueryExpr AST-parses expr as authored PromQL; a parse failure is
// itself the finding. No Grafana-variable tolerance, unlike checkPanelExpr.
func checkQueryExpr(p parser.Parser, expr, path, context string, known map[string]struct{}, prefixes []string) []Finding {
	parsed, err := p.ParseExpr(expr)
	if err != nil {
		return []Finding{{Path: path, Rule: "expr-parse-error", Message: fmt.Sprintf("%s: %v", context, err)}}
	}
	return selectorFindings(parsed, path, context, known, prefixes)
}

// checkPanelExpr parses a Grafana-variable-substituted copy of expr,
// but every finding quotes the expr as authored (substitution shifts
// the column numbers a parse error reports).
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

// Grafana expands template variables client-side before a panel query
// reaches Prometheus, so promql/parser rejects the raw $-tokens; these
// patterns substitute valid stand-ins purely so the query can be parsed
// and walked - substituted text is never reported or written anywhere.
//
//	$__rate_interval / $__interval / $__range   -> 5m           (also ${...})
//	any other $var, ${var} or ${var:format}     -> grafana_var
//
// The duration group only ever stands where PromQL demands one (a range
// selector's [...]); 5m is an arbitrary valid duration. Everything else
// becomes a bare identifier (valid as a quoted literal, selector, or
// label), deliberately not vg_-prefixed so it can't be mistaken for a
// real metric.
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

// checkQueryTokens is checkQueryExpr's non-PromQL sibling: a token
// scan, no parse step. Uses unresolvedMetric (not just tokenScanNames'
// vg_ regex) so both paths stay gated identically if that regex ever changes.
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

// checkRuleQuery routes expr by resolved datasource: AST for
// "prometheus", token-scan otherwise (today, only vg-loki-errors' LogQL).
func checkRuleQuery(p parser.Parser, it expandedItem, known map[string]struct{}, prefixes []string) []Finding {
	context := "rule " + it.uid
	if it.datasource == promDatasource {
		return checkQueryExpr(p, it.expr, it.sourcePath, context, known, prefixes)
	}
	return checkQueryTokens(it.expr, it.sourcePath, context+" (datasource "+it.datasource+", token-scanned)", known, prefixes)
}

// checkPanelQuery routes each target like checkRuleQuery: AST (with
// Grafana-variable tolerance) for Prometheus, token-scan otherwise (e.g. a Loki logs panel).
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

// headingLineRE matches one ATX heading line; parseMarkdown tracks
// fenced-block state so a "#"-led line inside a fence isn't mistaken for one.
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

// slugDrop matches characters GitHub's slug drops outright (no
// separator: "don't" -> "dont", not "don-t"); slugSpace is the
// whitespace run replaced by one hyphen (e.g. "1. Service down" -> "1-service-down").
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
// result since a runbook like stack.md is cited by many rules.
func headingsFor(repoRoot, file string, cache map[string]map[string]bool) (map[string]bool, error) {
	if got, ok := cache[file]; ok {
		return got, nil
	}
	path := filepath.Join(repoRoot, "docs", "runbooks", file)
	data, err := os.ReadFile(path) //nolint:gosec // G304: file is the manifest's own trusted, repo-authored runbook field.
	if err != nil {
		return nil, err
	}
	headings, _ := parseMarkdown(string(data))
	cache[file] = headings
	return headings, nil
}

// checkRunbookAnchor reports a runbook value with no "#anchor" suffix,
// naming a file that doesn't exist/can't be read, or with no heading slugging to that anchor.
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

// runbookIndexRowRE matches one alert-table data row by its Rule cell
// ("<uid> - <title>"); requiring the "vg-" prefix keeps the header and separator rows from matching.
var runbookIndexRowRE = regexp.MustCompile(`(?m)^\|\s*(vg-[a-z0-9-]+)\s+-\s+`)

// runbookIndexUIDs reads repoRoot/docs/runbooks/README.md and returns
// the set of rule uids its alert table carries a row for.
func runbookIndexUIDs(repoRoot string) (map[string]bool, error) {
	path := filepath.Join(repoRoot, "docs", "runbooks", "README.md")
	data, err := os.ReadFile(path) //nolint:gosec // G304: repoRoot is main's own trusted <repo-root> CLI argument.
	if err != nil {
		return nil, err
	}
	uids := make(map[string]bool)
	for _, m := range runbookIndexRowRE.FindAllStringSubmatch(string(data), -1) {
		uids[m[1]] = true
	}
	return uids, nil
}

// checkRunbookIndexRow reports a rule missing from README.md's alert
// table; nothing else notices that table going stale.
func checkRunbookIndexRow(it expandedItem, indexUIDs map[string]bool) []Finding {
	if indexUIDs[it.uid] {
		return nil
	}
	return []Finding{{
		Path: it.sourcePath, Rule: "runbook-index-row-missing",
		Message: fmt.Sprintf("rule %s: no row in docs/runbooks/README.md's alert table", it.uid),
	}}
}

// checkRunbookBlock tries AST parsing first, token-scan fallback
// otherwise; unlike checkQueryExpr, a parse failure is never itself a
// finding - most fenced blocks aren't PromQL, and that's expected.
func checkRunbookBlock(p parser.Parser, block, path string, known map[string]struct{}, prefixes []string) []Finding {
	if parsed, err := p.ParseExpr(block); err == nil {
		return selectorFindings(parsed, path, "fenced query", known, prefixes)
	}
	return checkQueryTokens(block, path, "fenced query (not valid PromQL, token-scanned)", known, prefixes)
}

// checkRunbookDocs scans every runbook's fenced code blocks,
// independent of which rules cite that file (a stale metric name can hide in an uncited doc too).
func checkRunbookDocs(p parser.Parser, repoRoot string, known map[string]struct{}, prefixes []string) []Finding {
	paths, err := filepath.Glob(filepath.Join(repoRoot, "docs", "runbooks", "*.md"))
	if err != nil || len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)

	var findings []Finding
	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from filepath.Glob over a fixed, repo-relative pattern, not external input.
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
