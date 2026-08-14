// Package alerts renders a loaded observability manifest into the
// Grafana alert provisioning file: one rule group (golden-template
// instantiations expanded per service, then that service's own custom
// rules, in manifest order) plus a deleteRules stanza per retired uid.
// Writing the result to its destination path is a later concern, the
// same split internal/dashboards draws for the dashboard side; this
// package only builds the bytes.
package alerts

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/dashboards"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// runbookPrefix is the canonical GitHub blob URL every rule's
// runbook_url annotation expands from a manifest's short form (e.g.
// "stack.md#service-down"): the exact prefix every runbook_url in
// today's vg-rules.yaml already uses. Kept in this one place so the
// expansion can never drift between rules.
const runbookPrefix = "https://github.com/levonn-dev/vgkeep/blob/main/docs/runbooks/"

// goldenRelativeSeconds is the relativeTimeRange.from every golden
// template instantiation's query (refId A) uses. A golden Template has
// no manifest-authored range - Template carries no Range field, and
// Overrides has no range field either (only for/condition/severity/
// summary) - so this is a fixed generator constant, not manifest-driven,
// unlike a custom Rule's relativeTimeRange (see rangeSeconds below),
// which always comes from its own Range field. The value matches
// vg-social-down's real relativeTimeRange: social's existing rule is the
// availability template's own zero-change migration source, so the
// template must reproduce social's real value exactly, and both golden
// templates today (availability, pdb_budget) query a current-state gauge
// rather than a rate()/increase() window, so a fixed five-minute
// lookback is enough to avoid a spurious no-data gap either way.
const goldenRelativeSeconds = 300

// expandedRule is every field a rendered rule needs, already resolved:
// {service}/{Service} substituted, golden overrides applied, and the
// condition string split into its evaluator type/value - the shared
// shape expandRules produces for both a golden template instantiation
// and a fully custom Rule, so ruleNode only ever has one input shape to
// render regardless of a rule's origin.
type expandedRule struct {
	uid             string
	title           string
	expr            string
	conditionOp     string
	conditionValue  string // raw text (e.g. "0.05", "1"), never reformatted from a parsed float
	instant         bool
	relativeSeconds int
	forDuration     string
	noDataState     string
	execErrState    string
	severity        string
	summary         string
	runbookShort    string
	panelRef        string // "" means no D10 annotations
	datasource      string // refId A's datasourceUid, already resolved against the tree default
}

// Emit renders m into the complete vg-rules.yaml bytes: apiVersion 1,
// one group (metadata from m.Alerts.Group), rules ordered cluster-file-
// then-services-in-manifest-order (golden template instantiations before
// a service's own custom rules, matching internal/dashboards' own
// expandAlerts order so the two packages agree on where an alert with a
// given uid "lives"), then a deleteRules stanza per retired uid (omitted
// entirely when there are none, matching today's file). idx is
// dashboards.Assemble's own PanelIndex output for the same m - a rule
// with a non-empty panel_ref gains __dashboardUid__/__panelId__
// annotations resolved against it and m.Dashboards.Services (D10); a
// rule with no panel_ref gains neither. Emit is a pure function of its
// two arguments: calling it twice on the same inputs produces
// byte-identical output.
func Emit(m *manifest.Model, idx dashboards.PanelIndex) ([]byte, error) {
	expanded, errs := expandRules(m)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	dashUIDs := make(map[string]string, len(m.Dashboards.Services))
	for _, sd := range m.Dashboards.Services {
		dashUIDs[sd.Service] = sd.UID
	}

	ruleNodes := make([]*yaml.Node, 0, len(expanded))
	for _, er := range expanded {
		rn, err := ruleNode(er, idx, dashUIDs)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ruleNodes = append(ruleNodes, rn)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	group := mapNode(
		strNode("orgId"), intNode(1),
		strNode("name"), strNode(m.Alerts.Group.Name),
		strNode("folder"), strNode(m.Alerts.Group.Folder),
		strNode("interval"), strNode(m.Alerts.Group.Interval),
		strNode("rules"), seqNode(ruleNodes...),
	)

	topKV := []*yaml.Node{
		strNode("apiVersion"), intNode(1),
		strNode("groups"), seqNode(group),
	}
	if len(m.Alerts.Retired) > 0 {
		delNodes := make([]*yaml.Node, 0, len(m.Alerts.Retired))
		for _, ret := range m.Alerts.Retired {
			delNodes = append(delNodes, mapNode(strNode("orgId"), intNode(1), strNode("uid"), strNode(ret.UID)))
		}
		topKV = append(topKV, strNode("deleteRules"), seqNode(delNodes...))
	}

	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{mapNode(topKV...)}}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encoding vg-rules.yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding vg-rules.yaml: %w", err)
	}

	return padFlowMappings(buf.Bytes()), nil
}

// expandRules walks every alert the manifest declares - cluster rules in
// their own manifest order, then each service in the model's own order
// (golden template instantiations, sorted by template name since
// ServiceAlerts.Golden is a Go map and ranging it directly would make
// output order depend on map iteration; then that service's custom
// rules in manifest order) - collecting every expansion problem
// (unparseable condition, a custom rule with no usable range) instead of
// stopping at the first, matching internal/manifest.Load's and
// internal/dashboards.Assemble's own collect-everything convention.
func expandRules(m *manifest.Model) ([]expandedRule, []error) {
	var (
		out  []expandedRule
		errs []error
	)

	treeDatasource := m.Alerts.Datasource

	for _, r := range m.Alerts.Cluster {
		er, err := expandCustomRule(r, treeDatasource)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, er)
	}

	for _, svc := range m.Alerts.Services {
		names := make([]string, 0, len(svc.Golden))
		for name := range svc.Golden {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			er, err := expandGoldenInstance(m.Alerts.Templates[name], svc.Golden[name], svc.Service, treeDatasource)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out = append(out, er)
		}

		for _, r := range svc.Alerts {
			er, err := expandCustomRule(r, treeDatasource)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out = append(out, er)
		}
	}

	return out, errs
}

// expandCustomRule resolves a cluster.yaml or per-service custom Rule
// into an expandedRule. Unlike a golden instantiation, a custom rule's
// relativeTimeRange.from always comes from its own Range field (parsed
// as a Go duration string into whole seconds) - Range is required here:
// there is no context-free default that would reproduce any of today's
// real rules' actual relativeTimeRange, migrated or not, so an empty
// Range is a hard error rather than a silent zero. datasource resolves
// the same way Range does not: r.Datasource is optional, and an empty
// value falls back to treeDatasource (m.Alerts.Datasource) - today's
// real rules are all on the same Prometheus instance except one Loki
// query, so leaving the field empty is the common case, not the
// exception.
func expandCustomRule(r manifest.Rule, treeDatasource string) (expandedRule, error) {
	op, val, err := splitCondition(r.UID, r.Condition)
	if err != nil {
		return expandedRule{}, err
	}
	seconds, err := rangeSeconds(r.UID, r.Range)
	if err != nil {
		return expandedRule{}, err
	}
	datasource := r.Datasource
	if datasource == "" {
		datasource = treeDatasource
	}
	return expandedRule{
		uid:             r.UID,
		title:           r.Title,
		expr:            r.Expr,
		conditionOp:     op,
		conditionValue:  val,
		instant:         r.Instant,
		relativeSeconds: seconds,
		forDuration:     r.For,
		noDataState:     r.NoDataState,
		execErrState:    r.ExecErrState,
		severity:        r.Severity,
		summary:         r.Summary,
		runbookShort:    r.Runbook,
		panelRef:        r.PanelRef,
		datasource:      datasource,
	}, nil
}

// expandGoldenInstance resolves one service's instantiation of tmpl,
// applying ov's overrides (for/condition/severity/summary only - never
// uid or expr: Overrides has no uid or expr field at all, so a service
// needing a different expr writes a custom rule instead of overriding
// one) and substituting {service}/{Service} into every string field a
// template can carry. A zero-value ov.Field means "use the template's
// own value" - Overrides has no way to distinguish "explicitly set back
// to the template default" from "left absent", which no manifest
// content needs today. A golden instantiation always uses
// treeDatasource: Template has no Datasource field and Overrides has no
// override slot for one either, since every template query today is the
// same Prometheus instance the tree default already names.
func expandGoldenInstance(tmpl manifest.Template, ov manifest.Overrides, service, treeDatasource string) (expandedRule, error) {
	forDuration := tmpl.For
	if ov.For != "" {
		forDuration = ov.For
	}
	condition := tmpl.Condition
	if ov.Condition != "" {
		condition = ov.Condition
	}
	severity := tmpl.Severity
	if ov.Severity != "" {
		severity = ov.Severity
	}
	summary := tmpl.Summary
	if ov.Summary != "" {
		summary = ov.Summary
	}

	uid := substitute(tmpl.UID, service)
	op, val, err := splitCondition(uid, condition)
	if err != nil {
		return expandedRule{}, err
	}

	return expandedRule{
		uid:             uid,
		title:           substitute(tmpl.Title, service),
		expr:            substitute(tmpl.Expr, service),
		conditionOp:     op,
		conditionValue:  val,
		instant:         tmpl.Instant,
		relativeSeconds: goldenRelativeSeconds,
		forDuration:     forDuration,
		noDataState:     tmpl.NoDataState,
		execErrState:    tmpl.ExecErrState,
		severity:        severity,
		summary:         substitute(summary, service),
		runbookShort:    tmpl.Runbook,
		panelRef:        substitute(tmpl.PanelRef, service),
		datasource:      treeDatasource,
	}, nil
}

// substitute replaces {Service}/{service} placeholders (capitalized and
// lowercase forms) with service, wherever they appear in s - a
// template's uid, title, expr, summary or panel_ref. Mirrors
// internal/dashboards' identical private helper; not shared, since that
// one is unexported to its own package and the two generator halves
// otherwise share no code.
func substitute(s, service string) string {
	s = strings.ReplaceAll(s, "{Service}", displayName(service))
	s = strings.ReplaceAll(s, "{service}", service)
	return s
}

// serviceDisplayNames overrides capitalize's plain first-letter
// fallback for a service whose {Service} form is not just its name
// capitalized: bff is an acronym, not a plain word, so it must render
// "BFF", not "Bff" - the owner ruling this table exists for. Mirrors
// internal/dashboards' and internal/lint's identical tables; add an
// entry here only when capitalize's fallback is wrong for that service
// - every other service's {Service} form still comes from capitalize
// alone.
var serviceDisplayNames = map[string]string{
	"bff": "BFF",
}

// displayName resolves service's {Service} form: serviceDisplayNames'
// override if one exists, else capitalize's plain fallback. Mirrors
// internal/dashboards' and internal/lint's identical private helper.
func displayName(service string) string {
	if d, ok := serviceDisplayNames[service]; ok {
		return d
	}
	return capitalize(service)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// splitCondition parses a manifest condition string ("lt 1", "gt 0.05")
// into its evaluator type and value. The value is validated as numeric
// (Grafana's evaluator schema requires it) but returned as its original
// text, not a reformatted float64 - so "0.05" or "25" round-trip through
// the emitted YAML exactly as the manifest wrote them, with no risk of a
// float64 round-trip silently changing precision or dropping/adding
// digits.
func splitCondition(uid, condition string) (op, value string, err error) {
	fields := strings.Fields(condition)
	if len(fields) != 2 {
		return "", "", fmt.Errorf("rule %s: cannot parse condition %q: want \"<op> <value>\"", uid, condition)
	}
	if _, err := strconv.ParseFloat(fields[1], 64); err != nil {
		return "", "", fmt.Errorf("rule %s: condition %q: value %q is not numeric: %w", uid, condition, fields[1], err)
	}
	return fields[0], fields[1], nil
}

// rangeSeconds parses a custom Rule's Range field (a Go duration string
// like "10m" or "26h") into whole seconds for refId A's
// relativeTimeRange.from. An empty Range is an error naming the rule's
// uid - see expandCustomRule's doc comment for why there is no default.
func rangeSeconds(uid, rangeStr string) (int, error) {
	if rangeStr == "" {
		return 0, fmt.Errorf("rule %s: range is required (relativeTimeRange has no default)", uid)
	}
	d, err := time.ParseDuration(rangeStr)
	if err != nil {
		return 0, fmt.Errorf("rule %s: range %q: %w", uid, rangeStr, err)
	}
	return int(d / time.Second), nil
}

// resolvePanelLink splits ref's "service/title" shape and resolves it
// against idx (D10's panel id source) and dashUIDs (the service's
// dashboard uid, from m.Dashboards.Services) - both checked explicitly
// so a panel_ref that Assemble would also have rejected, or one that
// simply names a service dashUIDs has no entry for, fails Emit loudly
// too rather than silently emitting an annotation with a blank or
// missing field.
func resolvePanelLink(uid, ref string, idx dashboards.PanelIndex, dashUIDs map[string]string) (dashboardUID string, panelID int, err error) {
	service, title, ok := strings.Cut(ref, "/")
	if !ok {
		return "", 0, fmt.Errorf("rule %s: malformed panel_ref %q: want \"service/title\"", uid, ref)
	}
	svcPanels, ok := idx[service]
	if !ok {
		return "", 0, fmt.Errorf("rule %s: panel_ref %q: service %q has no dashboard", uid, ref, service)
	}
	id, ok := svcPanels[title]
	if !ok {
		return "", 0, fmt.Errorf("rule %s: panel_ref %q: service %q has no panel titled %q", uid, ref, service, title)
	}
	dashUID, ok := dashUIDs[service]
	if !ok {
		return "", 0, fmt.Errorf("rule %s: panel_ref %q: service %q has no dashboard uid", uid, ref, service)
	}
	return dashUID, id, nil
}

// ruleNode builds one rule's complete envelope: refId A (the query,
// instant + relativeTimeRange from er.relativeSeconds, datasourceUid
// from er.datasource - the rule's own override or the tree default,
// already resolved by expandCustomRule/expandGoldenInstance), refId C
// (the threshold expression, condition parsed into evaluator type/value,
// datasourceUid always __expr__ regardless of er.datasource - it names
// Grafana's server-side expression engine, never a real data source),
// condition: C, noDataState/execErrState/for/labels verbatim, and
// annotations (summary, runbook_url, plus D10's two annotations when
// er.panelRef is set).
func ruleNode(er expandedRule, idx dashboards.PanelIndex, dashUIDs map[string]string) (*yaml.Node, error) {
	dataA := mapNode(
		strNode("refId"), strNode("A"),
		strNode("relativeTimeRange"), flowMapNode(strNode("from"), intNode(er.relativeSeconds), strNode("to"), intNode(0)),
		strNode("datasourceUid"), strNode(er.datasource),
		strNode("model"), mapNode(
			strNode("refId"), strNode("A"),
			strNode("instant"), boolNode(er.instant),
			strNode("expr"), strNode(er.expr),
		),
	)
	dataC := mapNode(
		strNode("refId"), strNode("C"),
		strNode("relativeTimeRange"), flowMapNode(strNode("from"), intNode(0), strNode("to"), intNode(0)),
		strNode("datasourceUid"), strNode("__expr__"),
		strNode("model"), mapNode(
			strNode("refId"), strNode("C"),
			strNode("type"), strNode("threshold"),
			strNode("expression"), strNode("A"),
			strNode("conditions"), seqNode(
				mapNode(strNode("evaluator"), flowMapNode(
					strNode("type"), strNode(er.conditionOp),
					strNode("params"), flowSeqNode(numLiteralNode(er.conditionValue)),
				)),
			),
		),
	)

	annotationsKV := []*yaml.Node{
		strNode("summary"), quotedStrNode(er.summary),
		strNode("runbook_url"), strNode(runbookPrefix + er.runbookShort),
	}
	if er.panelRef != "" {
		dashUID, panelID, err := resolvePanelLink(er.uid, er.panelRef, idx, dashUIDs)
		if err != nil {
			return nil, err
		}
		annotationsKV = append(annotationsKV,
			strNode("__dashboardUid__"), strNode(dashUID),
			strNode("__panelId__"), quotedStrNode(strconv.Itoa(panelID)),
		)
	}

	return mapNode(
		strNode("uid"), strNode(er.uid),
		strNode("title"), strNode(er.title),
		strNode("condition"), strNode("C"),
		strNode("data"), seqNode(dataA, dataC),
		strNode("noDataState"), strNode(er.noDataState),
		strNode("execErrState"), strNode(er.execErrState),
		strNode("for"), strNode(er.forDuration),
		strNode("annotations"), mapNode(annotationsKV...),
		strNode("labels"), mapNode(strNode("severity"), strNode(er.severity)),
	), nil
}

// --- yaml.v3 node helpers -------------------------------------------
//
// Every rule field is built through these rather than struct tags plus
// yaml.Marshal, because the envelope must reproduce today's hand-authored
// file's exact per-field styles (summary always double-quoted even when
// plain style would be syntactically valid; runbook_url always plain) -
// choices yaml.v3's own automatic style resolution would not reliably
// reproduce (it quotes only when a value is not representable in plain
// style, e.g. a leading '{', not as a blanket per-key policy).

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// quotedStrNode forces double-quoted style regardless of content -
// summary (every occurrence in today's file, whether or not plain style
// would have been valid for that particular text) and __panelId__
// (whose numeric-looking text must stay a string, not fall back to
// yaml.v3's own implicit-type quoting heuristic).
func quotedStrNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.DoubleQuotedStyle}
}

func boolNode(b bool) *yaml.Node {
	v := "false"
	if b {
		v = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
}

func intNode(n int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(n)}
}

// numLiteralNode carries a condition's already-validated numeric text
// verbatim (see splitCondition) rather than reformatting it through a
// parsed float64, so e.g. "0.05" can never come out as "0.05000" or
// "5e-02". The tag is chosen from the text's own shape purely for
// semantic correctness (plain-style output is identical either way,
// since a plain scalar's bytes are just its Value verbatim).
func numLiteralNode(text string) *yaml.Node {
	tag := "!!int"
	if strings.ContainsAny(text, ".eE") {
		tag = "!!float"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: text}
}

func mapNode(kv ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Content: kv}
}

func seqNode(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Content: items}
}

func flowMapNode(kv ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle, Content: kv}
}

func flowSeqNode(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Content: items}
}

// padFlowMappings inserts the inner-brace padding today's hand-authored
// vg-rules.yaml uses for both flow mappings this package ever emits
// ("{ from: 600, to: 0 }", "{ type: gt, params: [0.05] }"), which
// yaml.v3's flow-mapping emitter never produces on its own ("{from: ...}"
// - verified against the emitter source, emitterc.go's
// yaml_emitter_emit_flow_mapping_key/value: both '{' and '}' are written
// via yaml_emitter_write_indicator with need_whitespace=false, and no
// per-node Style covers this). Both patterns are anchored on their
// preceding key name ("relativeTimeRange: ", "evaluator: "), so neither
// can match inside an unrelated PromQL expr string even if one someday
// contained similar punctuation - an expr would have to contain the
// literal text "relativeTimeRange: {from: <digits>, to: <digits>}" (or
// the evaluator equivalent) to collide, which is not a valid PromQL
// fragment.
var (
	reRelativeTimeRange = regexp.MustCompile(`relativeTimeRange: \{from: (-?\d+), to: (-?\d+)\}`)
	reEvaluator         = regexp.MustCompile(`evaluator: \{type: (\w+), params: \[([^\]]*)\]\}`)
)

func padFlowMappings(doc []byte) []byte {
	doc = reRelativeTimeRange.ReplaceAll(doc, []byte(`relativeTimeRange: { from: $1, to: $2 }`))
	doc = reEvaluator.ReplaceAll(doc, []byte(`evaluator: { type: $1, params: [$2] }`))
	return doc
}
