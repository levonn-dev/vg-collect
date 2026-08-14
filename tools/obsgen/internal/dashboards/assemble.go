// Package dashboards assembles the per-service Grafana dashboard JSON
// files from a loaded observability manifest: the shared golden panel
// block plus each service's own custom panels, with generator-assigned
// panel ids and alert-threshold projection onto panel_ref'd panels.
// Writing the result to its destination path is a later concern; this
// package only builds the bytes and the panel index alert emission needs
// to derive its own dashboard-link annotations from the same pass.
package dashboards

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// PanelIndex maps service -> panel title (post-substitution) -> the
// generator-assigned panel id, for every panel in every emitted
// dashboard. Alert emission consumes this to link a panel_ref'd rule
// back to the exact panel threshold projection just wrote a step onto,
// from the same assembly pass that built the dashboards, so the two can
// never drift apart.
type PanelIndex map[string]map[string]int

func (idx PanelIndex) set(service, title string, id int) {
	if idx[service] == nil {
		idx[service] = make(map[string]int)
	}
	idx[service][title] = id
}

// GeneratedHeader is the sentence every generated output carries so a
// reader landing on the file knows to edit deploy/observability/
// instead: this package's own dashboards embed it in their JSON
// envelope's description field (JSON has no comment syntax); the
// sibling alerts package's vg-rules.yaml is plain YAML and carries an
// equivalent leading "# "-prefixed comment line instead, added by
// main.go's writer. Exported so both outputs say exactly the same
// thing without the sentence being copied by hand into a second file.
const GeneratedHeader = "Generated from deploy/observability/. Do not edit by hand."

// dashboardEnvelope is the fixed Grafana dashboard shape every emitted
// file shares - schema/behavior settings no manifest field controls
// (only uid, title, and panels vary per service). Field declaration
// order is the emitted JSON key order (encoding/json preserves it),
// matching the key order every hand-authored dashboard under
// deploy/charts/platform/files/dashboards already uses; Description
// sits right after Title, matching how Grafana itself serializes a
// dashboard's own description field.
type dashboardEnvelope struct {
	UID           string              `json:"uid"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	SchemaVersion int                 `json:"schemaVersion"`
	Editable      bool                `json:"editable"`
	Timezone      string              `json:"timezone"`
	Time          dashboardTimeSpan   `json:"time"`
	Refresh       string              `json:"refresh"`
	Tags          []string            `json:"tags"`
	Templating    dashboardTemplating `json:"templating"`
	Panels        []json.RawMessage   `json:"panels"`
}

type dashboardTimeSpan struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type dashboardTemplating struct {
	List []json.RawMessage `json:"list"`
}

// built is Assemble's working state between its three phases: every
// service's panels, still mutable (orderedMap, not yet marshaled) so
// threshold injection can modify one before the final pass, plus the
// title order each service's panels were assembled in (id assignment
// order) and the index every assigned id landed in.
type built struct {
	panels map[string]map[string]*orderedMap // service -> title -> panel
	order  map[string][]string               // service -> titles, assembly order
	idx    PanelIndex
}

// Assemble builds one dashboard JSON file per service in m.Dashboards,
// keyed "<service>.json", plus the panel index recording every panel's
// generator-assigned id. It runs in three phases - build every panel and
// assign ids, project alert thresholds onto panel_ref'd panels, marshal
// the finished dashboards - stopping at the first phase that finds any
// problem (a later phase's logic assumes the previous one fully
// succeeded, e.g. threshold injection needs every service's panel set
// already built), but collecting every problem within a phase via
// errors.Join rather than stopping at that phase's first one.
func Assemble(m *manifest.Model) (map[string][]byte, PanelIndex, error) {
	b, errs := buildAllPanels(m)
	if len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}

	if errs := injectAllThresholds(m, b); len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}

	files, errs := marshalAll(m, b)
	if len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}

	return files, b.idx, nil
}

// buildAllPanels parses every service's golden-block instances (with
// {service}/{Service} substituted) and custom panels into mutable
// orderedMaps, assigning each a generator-assigned id: golden panels get
// ids fixed by their position in the golden block (the same ids for
// every service, since every service gets the identical block), and
// custom panels continue numbering from there in the manifest's own
// order. Each service starts its own id sequence at 1.
func buildAllPanels(m *manifest.Model) (*built, []error) {
	b := &built{
		panels: make(map[string]map[string]*orderedMap),
		order:  make(map[string][]string),
		idx:    PanelIndex{},
	}
	var errs []error

	for _, sd := range m.Dashboards.Services {
		byTitle := make(map[string]*orderedMap)
		var order []string
		nextID := 1

		addPanel := func(raw json.RawMessage, context string) {
			om, err := parseOrderedMap(raw)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %s: %w", sd.Service, context, err))
				return
			}
			title, err := panelTitle(om)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %s: %w", sd.Service, context, err))
				return
			}
			if _, dup := byTitle[title]; dup {
				// A silent overwrite here would leave order carrying the
				// title twice while byTitle keeps only the later panel:
				// the emitted array would contain that one panel object
				// twice (once per duplicate entry in order) under two
				// different ids, and the earlier panel's content and id
				// would simply vanish. Titles are the stable identifier
				// panel_ref and PanelIndex both key on, so a collision
				// fails loudly instead.
				errs = append(errs, fmt.Errorf("%s: %s: duplicate panel title %q", sd.Service, context, title))
				return
			}
			om.prepend("id", json.RawMessage(strconv.Itoa(nextID)))
			byTitle[title] = om
			order = append(order, title)
			b.idx.set(sd.Service, title, nextID)
			nextID++
		}

		for i, gp := range m.Dashboards.Golden {
			raw := json.RawMessage(substitute(string(gp.Fragment), sd.Service))
			addPanel(raw, fmt.Sprintf("golden panel %d", i))
		}
		for i, raw := range sd.CustomPanels {
			addPanel(raw, fmt.Sprintf("custom panel %d", i))
		}

		b.panels[sd.Service] = byTitle
		b.order[sd.Service] = order
	}

	return b, errs
}

// panelTitle extracts a parsed panel's title field, the identifier
// threshold projection and the panel index both key on.
func panelTitle(om *orderedMap) (string, error) {
	raw, ok := om.get("title")
	if !ok {
		return "", errors.New("panel fragment has no title field")
	}
	var title string
	if err := json.Unmarshal(raw, &title); err != nil {
		return "", fmt.Errorf("panel title: %w", err)
	}
	return title, nil
}

// substitute replaces {Service}/{service} placeholders (capitalized and
// lowercase forms) with service, wherever they appear in s - a golden
// panel fragment's title, expr, or a label selector. Order between the
// two replacements does not matter: the two placeholder spellings never
// overlap as substrings of each other.
func substitute(s, service string) string {
	s = strings.ReplaceAll(s, "{Service}", displayName(service))
	s = strings.ReplaceAll(s, "{service}", service)
	return s
}

// serviceDisplayNames overrides capitalize's plain first-letter
// fallback for a service whose {Service} form is not just its name
// capitalized: bff is an acronym, not a plain word, so it must render
// "BFF", not "Bff" - the owner ruling this table exists for. Mirrors
// internal/alerts' and internal/lint's identical tables; add an entry
// here only when capitalize's fallback is wrong for that service -
// every other service's {Service} form still comes from capitalize
// alone.
var serviceDisplayNames = map[string]string{
	"bff": "BFF",
}

// displayName resolves service's {Service} form: serviceDisplayNames'
// override if one exists, else capitalize's plain fallback. Mirrors
// internal/alerts' and internal/lint's identical private helper.
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

// expandedAlert is the minimal shape threshold projection needs from any
// alert - a golden template instantiation or a fully custom rule - once
// {service}/{Service} substitution and golden override application have
// both happened. Building the full emitted rule envelope (labels,
// annotations, the query/condition node pair) is a separate concern;
// this type carries only what projection reads.
type expandedAlert struct {
	uid       string
	panelRef  string
	condition string
	severity  string
}

// expandAlerts walks every alert the manifest declares - cluster rules,
// then each service's golden template instantiations followed by its
// custom rules - substituting {service}/{Service} and applying golden
// overrides so every result is concrete. A service's golden template
// names are sorted before iteration: ServiceAlerts.Golden is a Go map,
// and ranging it directly would make step-append order (and so output
// bytes) depend on map iteration, which Go deliberately randomizes per
// range statement.
func expandAlerts(m *manifest.Model) []expandedAlert {
	var out []expandedAlert

	for _, r := range m.Alerts.Cluster {
		out = append(out, expandedAlert{uid: r.UID, panelRef: r.PanelRef, condition: r.Condition, severity: r.Severity})
	}

	for _, svc := range m.Alerts.Services {
		names := make([]string, 0, len(svc.Golden))
		for name := range svc.Golden {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			tmpl := m.Alerts.Templates[name]
			ov := svc.Golden[name]

			condition := tmpl.Condition
			if ov.Condition != "" {
				condition = ov.Condition
			}
			severity := tmpl.Severity
			if ov.Severity != "" {
				severity = ov.Severity
			}

			out = append(out, expandedAlert{
				uid:       substitute(tmpl.UID, svc.Service),
				panelRef:  substitute(tmpl.PanelRef, svc.Service),
				condition: condition,
				severity:  severity,
			})
		}

		for _, r := range svc.Alerts {
			out = append(out, expandedAlert{uid: r.UID, panelRef: r.PanelRef, condition: r.Condition, severity: r.Severity})
		}
	}

	return out
}

// injectAllThresholds walks every expanded alert with a non-empty
// panel_ref and appends a threshold step to the panel it names,
// collecting every problem found (an unresolvable ref, an unparseable
// condition, an unrecognized severity) rather than stopping at the
// first, matching the manifest loader's own collect-everything
// convention.
func injectAllThresholds(m *manifest.Model, b *built) []error {
	var errs []error

	for _, a := range expandAlerts(m) {
		if a.panelRef == "" {
			continue
		}

		service, title, err := splitPanelRef(a.panelRef)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %s: %w", a.uid, err))
			continue
		}
		svcPanels, ok := b.panels[service]
		if !ok {
			errs = append(errs, fmt.Errorf("rule %s: panel_ref %q: service %q has no dashboard", a.uid, a.panelRef, service))
			continue
		}
		panel, ok := svcPanels[title]
		if !ok {
			errs = append(errs, fmt.Errorf("rule %s: panel_ref %q: service %q has no panel titled %q", a.uid, a.panelRef, service, title))
			continue
		}

		value, err := parseConditionValue(a.condition)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %s: %w", a.uid, err))
			continue
		}
		color, err := thresholdColor(a.severity)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %s: %w", a.uid, err))
			continue
		}

		if err := appendThresholdStep(panel, color, value); err != nil {
			errs = append(errs, fmt.Errorf("rule %s: %w", a.uid, err))
		}
	}

	return errs
}

// splitPanelRef splits a panel_ref's "service/title" shape.
func splitPanelRef(ref string) (service, title string, err error) {
	i := strings.IndexByte(ref, '/')
	if i < 0 {
		return "", "", fmt.Errorf("malformed panel_ref %q: want \"service/title\"", ref)
	}
	return ref[:i], ref[i+1:], nil
}

// parseConditionValue extracts the numeric bound from a condition string
// (e.g. "lt 1", "gt 25"): an operator token and a number, whitespace
// separated. The operator itself is not validated or used - a threshold
// step only records a value and a color, not a direction - so any
// two-token condition with a numeric second token parses, keeping this
// forward-compatible with operators beyond the two seen in the manifests
// today.
func parseConditionValue(condition string) (float64, error) {
	fields := strings.Fields(condition)
	if len(fields) != 2 {
		return 0, fmt.Errorf("cannot parse numeric bound from condition %q", condition)
	}
	v, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse numeric bound from condition %q: %w", condition, err)
	}
	return v, nil
}

// thresholdColor maps an alert's severity to its threshold step color:
// warn is orange; crit and page both take the same red line (page is
// this repo's paging - most severe - level, so it shares crit's color
// rather than getting a third one). warn, crit, and page are the only
// severities any golden template or migrated rule uses; anything else
// has no defined color, so it fails loudly here rather than silently
// drawing a blank or wrongly-colored line.
func thresholdColor(severity string) (string, error) {
	switch severity {
	case "warn":
		return "orange", nil
	case "crit", "page":
		return "red", nil
	default:
		return "", fmt.Errorf("unrecognized severity %q for a threshold color", severity)
	}
}

// appendThresholdStep injects a {color, value} step into panel's
// fieldConfig.defaults.thresholds.steps (defaulting an absent mode to
// "absolute"; a wholly new thresholds object also gets Grafana's own
// base step ahead of the projected one, see appendStep; appending to
// steps that already existed - the base step among them or not -
// rather than replacing them) and sets
// fieldConfig.defaults.custom.thresholdsStyle.mode to "line", creating
// whichever of fieldConfig/defaults/custom/thresholds is missing along
// the way. Every level is read-modify-written back through
// orderedMap.set so a level that already existed keeps its other keys
// and its own key order untouched.
func appendThresholdStep(panel *orderedMap, color string, value float64) error {
	fieldConfig, err := panel.child("fieldConfig")
	if err != nil {
		return fmt.Errorf("fieldConfig: %w", err)
	}
	defaults, err := fieldConfig.child("defaults")
	if err != nil {
		return fmt.Errorf("fieldConfig.defaults: %w", err)
	}

	if err := appendStep(defaults, color, value); err != nil {
		return err
	}
	if err := setThresholdsStyleLine(defaults); err != nil {
		return err
	}

	defaultsBytes, err := defaults.marshal()
	if err != nil {
		return err
	}
	fieldConfig.set("defaults", defaultsBytes)

	fieldConfigBytes, err := fieldConfig.marshal()
	if err != nil {
		return err
	}
	panel.set("fieldConfig", fieldConfigBytes)

	return nil
}

// thresholdStep is one entry of fieldConfig.defaults.thresholds.steps.
// Value is a pointer only so baseThresholdColor's own step can marshal
// a JSON null: every real projected step (appendStep's color/value
// parameters) always carries a concrete value.
type thresholdStep struct {
	Color string   `json:"color"`
	Value *float64 `json:"value"`
}

// baseThresholdColor is the color Grafana itself writes, at value null,
// as the first step of every thresholds object a human draws in its
// panel editor - "green (fine) until the first real boundary below."
// appendStep reproduces that same convention when it is the one
// creating the thresholds object from nothing, so a generated panel's
// steps array looks like any Grafana-drawn one instead of starting
// straight at its first real boundary. A panel fragment that already
// authored its own thresholds object (see appendStep's hadThresholds)
// keeps whatever it authored, base step or not - this generator never
// adds a second one.
const baseThresholdColor = "green"

func appendStep(defaults *orderedMap, color string, value float64) error {
	_, hadThresholds := defaults.get("thresholds")

	thresholds, err := defaults.child("thresholds")
	if err != nil {
		return fmt.Errorf("thresholds: %w", err)
	}

	// mode and steps are checked for presence independently: a panel's
	// existing thresholds might in principle carry a mode with no steps
	// yet (or vice versa), and either one already being set must survive
	// untouched - only a wholly fresh thresholds object gets "absolute"
	// as its default mode.
	if _, ok := thresholds.get("mode"); !ok {
		thresholds.set("mode", json.RawMessage(`"absolute"`))
	}

	var steps []json.RawMessage
	if raw, ok := thresholds.get("steps"); ok {
		if err := json.Unmarshal(raw, &steps); err != nil {
			return fmt.Errorf("thresholds.steps: %w", err)
		}
	} else if !hadThresholds {
		baseBytes, err := json.Marshal(thresholdStep{Color: baseThresholdColor})
		if err != nil {
			return err
		}
		steps = append(steps, baseBytes)
	}

	stepBytes, err := json.Marshal(thresholdStep{Color: color, Value: &value})
	if err != nil {
		return err
	}
	steps = append(steps, stepBytes)

	stepsBytes, err := json.Marshal(steps)
	if err != nil {
		return err
	}
	thresholds.set("steps", stepsBytes)

	thresholdsBytes, err := thresholds.marshal()
	if err != nil {
		return err
	}
	defaults.set("thresholds", thresholdsBytes)
	return nil
}

func setThresholdsStyleLine(defaults *orderedMap) error {
	custom, err := defaults.child("custom")
	if err != nil {
		return fmt.Errorf("custom: %w", err)
	}
	thresholdsStyle, err := custom.child("thresholdsStyle")
	if err != nil {
		return fmt.Errorf("thresholdsStyle: %w", err)
	}

	thresholdsStyle.set("mode", json.RawMessage(`"line"`))

	thresholdsStyleBytes, err := thresholdsStyle.marshal()
	if err != nil {
		return err
	}
	custom.set("thresholdsStyle", thresholdsStyleBytes)

	customBytes, err := custom.marshal()
	if err != nil {
		return err
	}
	defaults.set("custom", customBytes)
	return nil
}

// marshalAll renders each service's finished panel set into a complete
// dashboard JSON document, in the id-assignment order buildAllPanels
// recorded (never by ranging b.panels[service], a Go map).
func marshalAll(m *manifest.Model, b *built) (map[string][]byte, []error) {
	files := make(map[string][]byte)
	var errs []error

	for _, sd := range m.Dashboards.Services {
		titles := b.order[sd.Service]
		panels := make([]json.RawMessage, 0, len(titles))
		for _, title := range titles {
			raw, err := b.panels[sd.Service][title].marshal()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: marshaling panel %q: %w", sd.Service, title, err))
				continue
			}
			panels = append(panels, raw)
		}

		env := dashboardEnvelope{
			UID:           sd.UID,
			Title:         sd.Title,
			Description:   GeneratedHeader,
			SchemaVersion: 39,
			Editable:      true,
			Timezone:      "browser",
			Time:          dashboardTimeSpan{From: "now-1h", To: "now"},
			Refresh:       "30s",
			Tags:          []string{"vgkeep"},
			Templating:    dashboardTemplating{List: []json.RawMessage{}},
			Panels:        panels,
		}

		body, err := json.MarshalIndent(env, "", "  ")
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: marshaling dashboard: %w", sd.Service, err))
			continue
		}
		files[sd.Service+".json"] = append(body, '\n')
	}

	if len(errs) > 0 {
		return nil, errs
	}
	return files, nil
}

// orderedMap is a JSON object decoded and re-encoded through an explicit
// key list, never a Go map's iteration order (which a bare range
// randomizes, and which encoding/json would otherwise resolve by sorting
// keys alphabetically - either way, silently reordering the manifest
// author's own field order). assemble.go uses it to modify one or two
// fields inside a verbatim panel fragment (the generator-assigned id,
// injected threshold steps) while leaving every other key - and its
// position - exactly as written.
type orderedMap struct {
	keys   []string
	values map[string]json.RawMessage
}

func newOrderedMap() *orderedMap {
	return &orderedMap{values: make(map[string]json.RawMessage)}
}

func (om *orderedMap) get(key string) (json.RawMessage, bool) {
	v, ok := om.values[key]
	return v, ok
}

// set assigns key's value: a new key is appended last, an existing key
// is updated in place at its current position.
func (om *orderedMap) set(key string, value json.RawMessage) {
	if _, ok := om.values[key]; !ok {
		om.keys = append(om.keys, key)
	}
	om.values[key] = value
}

// prepend assigns key's value as the first key if key is new (an
// existing key is updated in place instead, same as set - a key can
// only occupy one position). Used only for "id": Grafana's own panel
// JSON always lists id first, and no manifest panel fragment carries one
// (ids are generator-assigned).
func (om *orderedMap) prepend(key string, value json.RawMessage) {
	if _, ok := om.values[key]; ok {
		om.values[key] = value
		return
	}
	om.keys = append([]string{key}, om.keys...)
	om.values[key] = value
}

// child returns key's value reparsed as its own orderedMap, or a fresh
// empty one if key is absent - the get-or-create step every nested
// fieldConfig/defaults/thresholds/custom/thresholdsStyle lookup uses.
func (om *orderedMap) child(key string) (*orderedMap, error) {
	raw, ok := om.get(key)
	if !ok {
		return newOrderedMap(), nil
	}
	return parseOrderedMap(raw)
}

// marshal writes om back to compact JSON bytes in exactly om.keys'
// order. The final dashboard is marshaled through json.MarshalIndent,
// which re-indents this compact output along with everything else in
// one pass, so marshal never needs to reason about indentation.
func (om *orderedMap) marshal() (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range om.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(om.values[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// parseOrderedMap decodes raw's top-level object into an orderedMap,
// preserving its key order via json.Decoder's token stream instead of
// json.Unmarshal into a Go map (which does not preserve source order at
// all).
func parseOrderedMap(raw json.RawMessage) (*orderedMap, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))

	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a json object, got %v", tok)
	}

	om := newOrderedMap()
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected a string object key, got %v", keyTok)
		}

		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		om.set(key, val)
	}

	if _, err := dec.Token(); err != nil { // consume the closing '}'
		return nil, err
	}
	return om, nil
}
