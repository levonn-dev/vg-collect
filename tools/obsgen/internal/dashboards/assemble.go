// Package dashboards assembles the per-service Grafana dashboard JSON
// files from a loaded observability manifest: every golden block a
// service instantiates (each panel's y offset by that service's own
// anchor), its own custom panels, and one full-width row panel per
// section it declares (pinned at a literal y anchor, titled from the
// manifest with no substitution, carrying no children) - laid out in
// (gridPos.y, gridPos.x) order with generator-assigned panel ids and
// alert-threshold projection onto panel_ref'd panels. A row's title
// never enters the panel index or the panel_ref-resolution map: rows
// are grid items for layout purposes only, never addressable content
// (see stagedPanel.isRow). Writing the result to its destination path is
// a later concern; this package only builds the bytes and the panel
// index alert emission needs to derive its own dashboard-link
// annotations from the same pass.
//
// Projected threshold steps always start with Grafana's implicit null
// base step so hand-set and generated thresholds render identically.
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
// (gridPos.y, gridPos.x) assembly order each service's panels landed in
// (content and row panels alike, in final marshal order) and the index
// every assigned id landed in.
type built struct {
	panels map[string]map[string]*orderedMap // service -> title -> panel; a row's title never enters this map, so panel_ref can never resolve to one
	order  map[string][]stagedPanel          // service -> panels (content + rows) in final marshal order
	idx    PanelIndex                        // content panels only; a row never gets a PanelIndex entry
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

// stagedPanel is one service's panel mid-layout: parsed into a mutable
// orderedMap, with its title and final (already anchor-offset, for a
// block panel) gridPos already known, but not yet assigned a generator
// id - that happens only after every one of a service's panels is
// staged and sorted, see buildAllPanels. isRow marks a section row
// panel: it goes through the same (gridPos.y, gridPos.x) sort and id
// assignment as any other staged panel, but its title is never recorded
// in the panel index or the panel_ref-resolution map (see
// buildAllPanels' post-sort loop) - a row is a layout element, not
// addressable content.
type stagedPanel struct {
	om    *orderedMap
	title string
	pos   manifest.GridPos
	isRow bool
}

// sectionRow is a section row panel's fixed field set, marshaled in the
// exact key order Grafana itself uses for a row panel: alphabetical -
// collapsed, gridPos, id, panels, title, type - distinct from every
// other emitted panel, which lists id first because id is prepended
// onto an author-provided fragment that never carries one of its own
// (see orderedMap.prepend). ID here starts as a placeholder (Go's zero
// value); the same (gridPos.y, gridPos.x)-ordered id-assignment pass
// every other staged panel goes through overwrites it in place once the
// combined sort is known, since prepend updates an already-present key's
// value without moving its position.
type sectionRow struct {
	Collapsed bool              `json:"collapsed"`
	GridPos   manifest.GridPos  `json:"gridPos"`
	ID        int               `json:"id"`
	Panels    []json.RawMessage `json:"panels"`
	Title     string            `json:"title"`
	Type      string            `json:"type"`
}

// buildSectionRow constructs one section's row panel: full-width, one
// grid row tall, no children, at the manifest's literal anchor. title is
// never substituted - the {service}/{Service} substitution rule applies
// to golden block content, not to section titles, which are authored
// per service already concrete.
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

// buildAllPanels parses every service's golden-block instances (with
// {service}/{Service} substituted and each panel's block-relative
// gridPos.y offset by that block's anchor for this service), custom
// panels, and section row panels (one per sections entry, see
// buildSectionRow) into mutable orderedMaps, then lays each service's
// combined panel set out by (gridPos.y, gridPos.x), stable - the order
// both the emitted panel array and generator-assigned ids (1, 2, 3, ...
// per service, in that laid-out order) follow; a row's id is assigned
// the same way, but its title never enters the panel index or the
// panel_ref-resolution map (see stagedPanel.isRow). After layout, every
// panel's rect is checked for grid violations (out-of-bounds or
// overlapping) via internal/grid, since that is the first point every
// service's full, final geometry is known; a service with at least one
// section also gets its rects checked for compaction stability (see the
// len(sd.Sections) gate below).
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
				// A silent overwrite here would leave staged carrying the
				// title twice while byTitle keeps only the later panel:
				// the emitted array would contain that one panel object
				// twice under two different ids, and the earlier panel's
				// content would simply vanish. Titles are the stable
				// identifier panel_ref and PanelIndex both key on, so a
				// collision fails loudly instead.
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
		// Stability enforcement is opt-in per dashboard: a service that
		// has not yet adopted section rows keeps the original
		// overlap/bounds-only gate, so its existing custom-panel
		// arrangement is not suddenly rejected by a check that postdates
		// it. A service that declares at least one section has opted in,
		// and every one of its panels - block, custom, and row alike -
		// must be compaction-stable.
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

// resolveGridPos reads a panel's gridPos (h, w, x, y) out of its
// orderedMap-decoded fragment. anchor is non-nil only for a golden block
// panel: its gridPos.y is block-relative on entry, so resolveGridPos
// adds *anchor to it and writes the new value back into om's own
// gridPos.y key - through the same read-modify-write-back pattern
// appendThresholdStep already uses for fieldConfig, so every other key
// (and its own position) inside gridPos survives untouched. A custom
// panel's gridPos (anchor nil) is read only, never rewritten: it is
// already the absolute position the manifest authored.
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
// recorded (never by ranging b.panels[service], a Go map, which also
// would not carry a row - see built.order's own doc comment).
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
