// Package alerts renders a loaded manifest into the Grafana alert
// provisioning file: one rule group (golden templates then custom
// rules) plus a deleteRules stanza per retired uid. Builds bytes only.
package alerts

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/dashboards"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/expand"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// runbookPrefix is the GitHub blob URL prefix every rule's runbook_url
// expands a manifest's short form into (e.g. "stack.md#service-down").
const runbookPrefix = "https://github.com/levonn-dev/vgkeep/blob/main/docs/runbooks/"

// goldenRelativeSeconds (300 = 5 min) is the fixed relativeTimeRange.from
// for every golden template: Template/Overrides carry no range field, and
// both templates query a gauge, not rate()/increase(), so a fixed
// lookback avoids a spurious no-data gap.
const goldenRelativeSeconds = 300

// expandedRule is a rendered rule's fields, already resolved (substitution,
// overrides, condition split) for both a golden instantiation and a Rule.
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
	panelRef        string // "" means no dashboard-link annotations
	datasource      string // refId A's datasourceUid, already resolved against the tree default
}

// Emit renders m into vg-rules.yaml bytes: cluster rules, then each
// service's golden instantiations, then its custom rules, matching
// internal/dashboards' own ordering. idx resolves a rule's panel_ref
// into dashboard-link annotations. Pure: same inputs, byte-identical output.
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

// expandRules walks cluster rules, then each service's golden
// instantiations (sorted by template name; Golden is a Go map) then its
// custom rules, collecting every error instead of stopping at the first.
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
		for _, name := range slices.Sorted(maps.Keys(svc.Golden)) {
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

// expandCustomRule resolves a Rule into an expandedRule. Range is
// required (a duration string; empty is a hard error, no default
// exists). Datasource is optional and falls back to treeDatasource.
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

// expandGoldenInstance applies ov's overrides (for/condition/severity/
// summary only - Overrides has no uid or expr field) and substitutes
// {service}/{Service}. A zero-value override means "use the template's
// value"; datasource is always treeDatasource (no override slot exists).
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

	uid := expand.Substitute(tmpl.UID, service)
	op, val, err := splitCondition(uid, condition)
	if err != nil {
		return expandedRule{}, err
	}

	return expandedRule{
		uid:             uid,
		title:           expand.Substitute(tmpl.Title, service),
		expr:            expand.Substitute(tmpl.Expr, service),
		conditionOp:     op,
		conditionValue:  val,
		instant:         tmpl.Instant,
		relativeSeconds: goldenRelativeSeconds,
		forDuration:     forDuration,
		noDataState:     tmpl.NoDataState,
		execErrState:    tmpl.ExecErrState,
		severity:        severity,
		summary:         expand.Substitute(summary, service),
		runbookShort:    tmpl.Runbook,
		panelRef:        expand.Substitute(tmpl.PanelRef, service),
		datasource:      treeDatasource,
	}, nil
}

// splitCondition parses "lt 1"/"gt 0.05" into evaluator type and value;
// value is validated numeric but kept as text to avoid float round-trip precision loss.
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

// rangeSeconds parses Range (a Go duration string like "10m") into whole
// seconds; empty Range errors naming the rule's uid.
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

// resolvePanelLink splits ref's "service/title" and resolves it against
// idx and dashUIDs; either miss fails Emit loudly instead of emitting a blank annotation.
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

// ruleNode builds one rule's envelope: refId A (the query, using
// er.datasource) and refId C (the threshold expression; datasourceUid
// is always "__expr__", Grafana's expression engine, never er.datasource).
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
// Built manually rather than via struct tags + yaml.Marshal, to
// reproduce exact per-field styles (summary always double-quoted,
// runbook_url always plain) that yaml.v3's automatic resolution won't guarantee.

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// quotedStrNode forces double-quoted style regardless of content: used
// for summary and for __panelId__ (numeric-looking text that must stay a string).
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

// numLiteralNode carries a condition's numeric text verbatim (never
// reformatted through float64, avoiding e.g. "0.05" becoming "5e-02").
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

// padFlowMappings adds inner-brace spacing ("{ from: ..., to: ... }")
// that yaml.v3 never emits and has no per-node style for. Both regexes
// anchor on their preceding key name, so neither can match inside a PromQL expr string.
var (
	reRelativeTimeRange = regexp.MustCompile(`relativeTimeRange: \{from: (-?\d+), to: (-?\d+)\}`)
	reEvaluator         = regexp.MustCompile(`evaluator: \{type: (\w+), params: \[([^\]]*)\]\}`)
)

func padFlowMappings(doc []byte) []byte {
	doc = reRelativeTimeRange.ReplaceAll(doc, []byte(`relativeTimeRange: { from: $1, to: $2 }`))
	doc = reEvaluator.ReplaceAll(doc, []byte(`evaluator: { type: $1, params: [$2] }`))
	return doc
}
