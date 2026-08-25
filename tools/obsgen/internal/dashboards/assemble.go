// Package dashboards assembles per-service Grafana dashboard JSON from a
// loaded manifest: golden blocks, custom panels, and section rows, laid
// out by (gridPos.y, gridPos.x) with generator-assigned ids, plus
// alert-threshold projection onto panel_ref'd panels. Row titles are
// layout-only, never addressable; this package builds bytes and
// PanelIndex only.
//
// Projected threshold steps start with Grafana's implicit null base step.
package dashboards

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/expand"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/grid"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// PanelIndex maps service -> panel title -> generator-assigned panel id;
// alert emission uses this from the same pass so the two can never drift apart.
type PanelIndex map[string]map[string]int

func (idx PanelIndex) set(service, title string, id int) {
	if idx[service] == nil {
		idx[service] = make(map[string]int)
	}
	idx[service][title] = id
}

// GeneratedHeader is the sentence every generated output carries (this
// package's description field; alerts' vg-rules.yaml gets an equivalent "# " line).
const GeneratedHeader = "Generated from deploy/observability/. Do not edit by hand."

// dashboardEnvelope is the fixed Grafana dashboard shape every file
// shares (only uid, title, panels vary per service). Field order is the
// emitted JSON key order (encoding/json preserves it), matching Grafana's own.
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

// built is Assemble's working state between its three phases: mutable
// per-service panels, their marshal order, and the assigned-id index.
type built struct {
	panels map[string]map[string]*orderedMap // service -> title -> panel; a row's title never enters this map, so panel_ref can never resolve to one
	order  map[string][]stagedPanel          // service -> panels (content + rows) in final marshal order
	idx    PanelIndex                        // content panels only; a row never gets a PanelIndex entry
}

// Assemble builds one dashboard JSON file per service, keyed
// "<service>.json", plus PanelIndex, in three phases (build+assign ids,
// project thresholds, marshal) that stop at the first phase with any problem.
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

// stagedPanel is one service's panel mid-layout: parsed, gridPos
// resolved, but not yet id-assigned (see buildAllPanels). isRow marks a
// section row: sorted and id-assigned like any panel, but never entered
// into the panel index.
type stagedPanel struct {
	om    *orderedMap
	title string
	pos   manifest.GridPos
	isRow bool
}

// sectionRow is a section row panel's fixed field set, marshaled
// alphabetically (Grafana's own row-panel key order), unlike other
// panels which list id first. ID starts as a placeholder, overwritten
// once id-assignment runs.
type sectionRow struct {
	Collapsed bool              `json:"collapsed"`
	GridPos   manifest.GridPos  `json:"gridPos"`
	ID        int               `json:"id"`
	Panels    []json.RawMessage `json:"panels"`
	Title     string            `json:"title"`
	Type      string            `json:"type"`
}

// buildSectionRow constructs one section's row panel: full-width, one row
// tall, at the manifest's literal anchor; title is never substituted.
func buildSectionRow(title string, anchor int) (*orderedMap, error) {
	raw, err := json.Marshal(sectionRow{
		GridPos: manifest.GridPos{H: 1, W: 24, X: 0, Y: anchor},
		Panels:  []json.RawMessage{},
		Title:   title,
		Type:    "row",
	})
	if err != nil {
		return nil, err
	}
	return parseOrderedMap(raw)
}

// buildAllPanels parses golden-block, custom, and section-row panels
// into orderedMaps, lays each service's set out by (gridPos.y, gridPos.x)
// stable, and assigns ids 1, 2, 3... in that order (rows included, but
// never indexed). Checks every rect for grid violations; a service with
// at least one section also gets compaction-stability checks.
func buildAllPanels(m *manifest.Model) (*built, []error) {
	b := &built{
		panels: make(map[string]map[string]*orderedMap),
		order:  make(map[string][]stagedPanel),
		idx:    PanelIndex{},
	}
	var errs []error

	blocksByService := make(map[string][]expand.BlockPanel)
	for _, bp := range expand.Blocks(m) {
		blocksByService[bp.Service] = append(blocksByService[bp.Service], bp)
	}

	for _, sd := range m.Dashboards.Services {
		byTitle := make(map[string]*orderedMap)
		var staged []stagedPanel

		stage := func(raw json.RawMessage, context string, anchor *int) {
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
				// a silent overwrite would duplicate the panel in the emitted
				// array and drop the earlier one's content; titles are
				// panel_ref's and PanelIndex's key, so a collision fails loudly.
				errs = append(errs, fmt.Errorf("%s: %s: duplicate panel title %q", sd.Service, context, title))
				return
			}
			pos, err := resolveGridPos(om, anchor)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %s: %w", sd.Service, context, err))
				return
			}
			byTitle[title] = om
			staged = append(staged, stagedPanel{om: om, title: title, pos: pos})
		}

		for i, bp := range blocksByService[sd.Service] {
			anchor := bp.AnchorY
			stage(json.RawMessage(bp.Fragment), fmt.Sprintf("golden block %q panel %d", bp.Block, i), &anchor)
		}
		for i, raw := range sd.CustomPanels {
			stage(raw, fmt.Sprintf("custom panel %d", i), nil)
		}
		for _, title := range slices.Sorted(maps.Keys(sd.Sections)) {
			anchor := sd.Sections[title]
			om, err := buildSectionRow(title, anchor)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: section %q: %w", sd.Service, title, err))
				continue
			}
			staged = append(staged, stagedPanel{
				om:    om,
				title: title,
				pos:   manifest.GridPos{H: 1, W: 24, X: 0, Y: anchor},
				isRow: true,
			})
		}

		sort.SliceStable(staged, func(i, j int) bool {
			if staged[i].pos.Y != staged[j].pos.Y {
				return staged[i].pos.Y < staged[j].pos.Y
			}
			return staged[i].pos.X < staged[j].pos.X
		})

		var order []stagedPanel
		rects := make([]grid.Rect, 0, len(staged))
		for i, p := range staged {
			id := i + 1
			p.om.prepend("id", json.RawMessage(strconv.Itoa(id)))
			order = append(order, p)
			if !p.isRow {
				b.idx.set(sd.Service, p.title, id)
			}
			rects = append(rects, grid.Rect{Title: p.title, X: p.pos.X, Y: p.pos.Y, W: p.pos.W, H: p.pos.H})
		}
		for _, v := range grid.Check(rects) {
			errs = append(errs, fmt.Errorf("%s: %s: %s", sd.Service, v.Kind, v.Detail))
		}
		// stability enforcement is opt-in: a service with no sections keeps
		// the original overlap/bounds-only gate, so pre-existing
		// arrangements aren't rejected by a check that postdates them.
		if len(sd.Sections) > 0 {
			for _, v := range grid.CheckStability(rects) {
				errs = append(errs, fmt.Errorf("%s: %s: %s", sd.Service, v.Kind, v.Detail))
			}
		}

		b.panels[sd.Service] = byTitle
		b.order[sd.Service] = order
	}

	return b, errs
}

// resolveGridPos reads a panel's gridPos; anchor (non-nil only for a
// golden block panel) is added to gridPos.y and written back, leaving
// every other key untouched. A custom panel (anchor nil) is read only.
func resolveGridPos(om *orderedMap, anchor *int) (manifest.GridPos, error) {
	gp, err := om.child("gridPos")
	if err != nil {
		return manifest.GridPos{}, fmt.Errorf("gridPos: %w", err)
	}
	raw, err := gp.marshal()
	if err != nil {
		return manifest.GridPos{}, fmt.Errorf("gridPos: %w", err)
	}
	var pos manifest.GridPos
	if err := json.Unmarshal(raw, &pos); err != nil {
		return manifest.GridPos{}, fmt.Errorf("gridPos: %w", err)
	}

	if anchor != nil {
		pos.Y += *anchor
		gp.set("y", json.RawMessage(strconv.Itoa(pos.Y)))
		gpBytes, err := gp.marshal()
		if err != nil {
			return manifest.GridPos{}, fmt.Errorf("gridPos: %w", err)
		}
		om.set("gridPos", gpBytes)
	}

	return pos, nil
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

// expandedAlert is the minimal, fully-substituted shape threshold
// projection needs from any alert, golden or custom.
type expandedAlert struct {
	uid       string
	panelRef  string
	condition string
	severity  string
}

// expandAlerts walks cluster rules, then each service's golden
// instantiations (sorted by template name; Golden is a Go map) then its
// custom rules, substituting {service}/{Service} and applying overrides.
func expandAlerts(m *manifest.Model) []expandedAlert {
	var out []expandedAlert

	for _, r := range m.Alerts.Cluster {
		out = append(out, expandedAlert{uid: r.UID, panelRef: r.PanelRef, condition: r.Condition, severity: r.Severity})
	}

	for _, svc := range m.Alerts.Services {
		for _, name := range slices.Sorted(maps.Keys(svc.Golden)) {
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
				uid:       expand.Substitute(tmpl.UID, svc.Service),
				panelRef:  expand.Substitute(tmpl.PanelRef, svc.Service),
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

// injectAllThresholds appends a threshold step to every panel_ref'd
// alert's panel, collecting every problem instead of stopping at the first.
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

// parseConditionValue extracts the numeric bound from "lt 1"/"gt 25";
// the operator itself is not validated (a threshold step records only
// value and color, not direction).
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

// thresholdColor maps severity to color: warn orange, crit/page red
// (page is the most severe level, sharing crit's color). Any other
// severity fails loudly rather than drawing a blank line.
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

// appendThresholdStep injects a {color, value} step into
// fieldConfig.defaults.thresholds.steps (appending, not replacing; see
// appendStep) and sets thresholdsStyle.mode to "line", creating any missing level.
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

// thresholdStep is one entry of thresholds.steps; Value is a pointer
// only so the base step (see baseThresholdColor) can marshal JSON null.
type thresholdStep struct {
	Color string   `json:"color"`
	Value *float64 `json:"value"`
}

// baseThresholdColor (green, value null) is the first step Grafana
// itself writes for a hand-drawn thresholds object; appendStep
// reproduces it only when creating thresholds from nothing, never adding a second one.
const baseThresholdColor = "green"

func appendStep(defaults *orderedMap, color string, value float64) error {
	_, hadThresholds := defaults.get("thresholds")

	thresholds, err := defaults.child("thresholds")
	if err != nil {
		return fmt.Errorf("thresholds: %w", err)
	}

	// mode and steps are checked independently: either already being set
	// must survive untouched; only a fresh thresholds object defaults to "absolute".
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

// marshalAll renders each service's panel set in the id-assignment
// order buildAllPanels recorded (never by ranging b.panels, a Go map).
func marshalAll(m *manifest.Model, b *built) (map[string][]byte, []error) {
	files := make(map[string][]byte)
	var errs []error

	for _, sd := range m.Dashboards.Services {
		staged := b.order[sd.Service]
		panels := make([]json.RawMessage, 0, len(staged))
		for _, p := range staged {
			raw, err := p.om.marshal()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: marshaling panel %q: %w", sd.Service, p.title, err))
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

// orderedMap is a JSON object decoded/re-encoded via an explicit key
// list, never Go map iteration order (randomized) or encoding/json's
// alphabetical sort - either would reorder the manifest author's fields.
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

// prepend assigns key's value as the first key if new (existing keys
// update in place, like set). Used only for "id" (fragments never carry one).
func (om *orderedMap) prepend(key string, value json.RawMessage) {
	if _, ok := om.values[key]; ok {
		om.values[key] = value
		return
	}
	om.keys = append([]string{key}, om.keys...)
	om.values[key] = value
}

// child returns key's value reparsed as its own orderedMap, or a fresh
// empty one if key is absent (get-or-create for nested lookups).
func (om *orderedMap) child(key string) (*orderedMap, error) {
	raw, ok := om.get(key)
	if !ok {
		return newOrderedMap(), nil
	}
	return parseOrderedMap(raw)
}

// marshal writes om back to compact JSON in exactly om.keys' order;
// json.MarshalIndent re-indents it later, so this never needs to.
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

// parseOrderedMap decodes raw into an orderedMap, preserving key order
// via json.Decoder's token stream (json.Unmarshal into a map would not).
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
