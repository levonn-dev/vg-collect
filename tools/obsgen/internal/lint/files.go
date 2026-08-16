package lint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/grid"
)

// Dashboard file walk: structural and expr/metric checks over the
// shipped Grafana dashboard JSON files under deploy/charts/platform/
// files/dashboards - twelve files, six assembled by this module's own
// gen path from the manifest content checkPanelQuery already walks, six
// hand-authored and, until this file, never checked by anything. Both
// are in scope and checked identically: a defect in golden/custom panel
// content can therefore surface twice (once from the manifest walk
// quoting authored text, once from here quoting the generated file),
// which is accepted - either walk finding it is the point, and
// internal/dashboards.Assemble separately fails the same generator run
// at gen time on a bounds/overlap violation in its own output (see
// grid.Check, shared by both) and, for a dashboard that has adopted
// section rows, a compaction-stability violation too (see
// grid.CheckStability, likewise shared - this file walk runs it
// unconditionally on every file, while Assemble only enforces it once a
// dashboard declares at least one section).
//
// Unlike every other check in this package, which reads a
// manifest.Model internal/manifest already decoded, this walk reads
// real files off disk directly, with its own JSON decode and its own
// datasource/expr routing - reusing the same building blocks the rest
// of the package already established (grid.Check, grid.CheckStability,
// resolvePanelDatasource, checkPanelExpr, checkQueryTokens, the Finding
// shape) rather than duplicating any of them.

// fileDashboard and filePanel decode just enough of one shipped
// dashboard file to run every check below. GridPos is a map rather than
// a struct so a panel that omits h, w, x, or y (or sets one to JSON
// null) is distinguishable from one that sets it to a real zero -
// gridPosComplete reads the key set, not zero-valuedness, the same
// distinction internal/manifest/load.go's validateBlockPanelGeometry
// already draws for a golden block panel at load time. Panels (a
// panel's own nested panels, e.g. a collapsed row's children) is never
// decoded past json.RawMessage: this walk is deliberately flat (see the
// row-container finding below), so a container's children are never
// inspected, only the fact that they exist.
type fileDashboard struct {
	Panels []filePanel `json:"panels"`
}

type filePanel struct {
	ID         *int              `json:"id"`
	Type       string            `json:"type"`
	Title      *string           `json:"title"`
	GridPos    map[string]*int   `json:"gridPos"`
	Panels     []json.RawMessage `json:"panels"`
	Collapsed  bool              `json:"collapsed"`
	Datasource datasourceRef     `json:"datasource"`
	Targets    []fileTarget      `json:"targets"`
}

type fileTarget struct {
	Datasource datasourceRef `json:"datasource"`
	Expr       string        `json:"expr"`
}

// checkDashboardFiles walks every *.json file directly under dir (not
// recursively - the real directory is flat) in sorted order and returns
// every finding across all of them. dir not existing, or existing but
// unreadable, is itself one finding rather than a silent skip: every
// repoRoot this package is ever pointed at is expected to carry this
// directory, so its absence is exactly as real a problem as a bad panel
// inside one of the files in it.
func checkDashboardFiles(dir string, p parser.Parser, known map[string]struct{}, prefixes []string) []Finding {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []Finding{{Path: dir, Rule: "dashboard-file-scan-error", Message: err.Error()}}
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)

	var findings []Finding
	for _, name := range names {
		findings = append(findings, checkDashboardFile(filepath.Join(dir, name), name, p, known, prefixes)...)
	}
	return findings
}

// idOccurrence records where a panel id was first seen: its position in
// the panels array and its title, so a later duplicate can name that
// earlier panel unambiguously. Title alone cannot do this - it may be
// empty, or itself a duplicate-panel-title collision - but the index
// always identifies exactly one panel.
type idOccurrence struct {
	index int
	title string
}

// checkDashboardFile decodes and checks one dashboard file. Every
// finding's Path is fileCtx ("files/dashboards/<name>.json") rather than
// path - path only locates the file to read, so a finding reads the
// same regardless of which repoRoot this package was pointed at, the
// same shorthand-path convention checkRuleQuery/checkPanelQuery already
// use for a manifest source file (e.g. "alerts/widget.yaml"). A
// per-panel finding's Message additionally carries that panel's own
// context ("files/dashboards/<name>.json panel \"<title>\""), built once
// per panel below and reused for both the structural and the expr/
// metric findings that panel can produce.
func checkDashboardFile(path, name string, p parser.Parser, known map[string]struct{}, prefixes []string) []Finding {
	fileCtx := "files/dashboards/" + name

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is filepath.Join(dir, name) over os.ReadDir's own listing of dir, a fixed, repo-relative directory - never external input, the same trust boundary internal/manifest/load.go's decodeFile already documents for its own file reads.
	if err != nil {
		return []Finding{{Path: fileCtx, Rule: "dashboard-file-scan-error", Message: err.Error()}}
	}

	var dash fileDashboard
	if err := json.Unmarshal(data, &dash); err != nil {
		return []Finding{{Path: fileCtx, Rule: "dashboard-file-scan-error", Message: fmt.Sprintf("not valid json: %v", err)}}
	}

	var (
		findings  []Finding
		rects     []grid.Rect
		ids       = map[int]idOccurrence{}
		titles    = map[string]bool{}
		rowTitles = map[string]bool{}
	)

	for i, panel := range dash.Panels {
		title := ""
		if panel.Title != nil {
			title = *panel.Title
		}
		context := fmt.Sprintf("%s panel %q", fileCtx, title)
		isRow := panel.Type == "row"

		// Any panel carrying its own nested panels array is flagged on
		// sight rather than walked into: the rest of this loop and
		// grid.Check both operate on the flat top-level panels slice
		// only, so a container this check did not exist would silently
		// exempt every one of its children from every check below. An
		// expanded row - type "row", panels absent or empty - is the one
		// container shape that is NOT flagged: it carries no children to
		// exempt, and it is how a dashboard pins a section (see
		// internal/dashboards' row emission).
		if len(panel.Panels) > 0 {
			findings = append(findings, Finding{
				Path: fileCtx, Rule: "row-container",
				Message: fmt.Sprintf("%s: type %q carries a nested panels array; the file walk is flat and never descends into a container, which would silently exempt its children from every check", context, panel.Type),
			})
		}

		// A row this package's own generator emits is always expanded
		// ("collapsed": false - see internal/dashboards' row emission),
		// so a row hand-edited to "collapsed": true is never something
		// task gen produced. Flagged regardless of whether it also
		// carries a nested panels array (the row-container finding
		// above already covers that shape on its own): a collapsed row
		// hides its own content behind a click-to-expand header in the
		// Grafana UI, so the "expanded, no children" shape the
		// row-container check above deliberately does NOT flag - see
		// this loop's own comment on that check - would otherwise lint
		// clean with collapsed flipped true too, even though it now
		// renders as a stub collapsed header with nothing behind it.
		if isRow && panel.Collapsed {
			findings = append(findings, Finding{
				Path: fileCtx, Rule: "collapsed-row",
				Message: fmt.Sprintf("%s: row is collapsed; the generator only emits expanded rows and a collapsed row hides its content behind a click-to-expand header in the Grafana UI", context),
			})
		}

		if panel.Title == nil || *panel.Title == "" {
			findings = append(findings, Finding{
				Path: fileCtx, Rule: "empty-panel-title",
				Message: fmt.Sprintf("%s: panel has no title", context),
			})
		}

		if gridPosComplete(panel.GridPos) {
			rects = append(rects, grid.Rect{
				Title: title,
				X:     *panel.GridPos["x"], Y: *panel.GridPos["y"],
				W: *panel.GridPos["w"], H: *panel.GridPos["h"],
			})
		} else {
			findings = append(findings, Finding{
				Path: fileCtx, Rule: "incomplete-gridpos",
				Message: fmt.Sprintf("%s: gridPos is missing or incomplete (want h, w, x, y all present)", context),
			})
		}

		// A nil id is fine - Grafana assigns one at save time for a panel
		// nothing else already numbered - so only a real, repeated id
		// value is ever tracked or flagged. Both panels are named by their
		// zero-based array index as well as their title: a title alone
		// does not locate anything when it is empty (or itself the
		// duplicate-panel-title finding's own duplicate), so the index is
		// the one identifier that always does.
		if panel.ID != nil {
			if prev, ok := ids[*panel.ID]; ok {
				findings = append(findings, Finding{
					Path: fileCtx, Rule: "duplicate-panel-id",
					Message: fmt.Sprintf("%s: panel %d %q reuses id %d of panel %d %q", fileCtx, i, title, *panel.ID, prev.index, prev.title),
				})
			} else {
				ids[*panel.ID] = idOccurrence{index: i, title: title}
			}
		}

		// An empty title already has its own finding above; only a real,
		// repeated title is a second, distinct problem worth its own
		// finding. A row's title is a section header - a separate
		// namespace from every content panel's own title, so the two
		// never collide with each other, only within their own kind
		// (mirroring internal/dashboards.Assemble, which never indexes a
		// row's title alongside a content panel's).
		if title != "" {
			if isRow {
				if rowTitles[title] {
					findings = append(findings, Finding{
						Path: fileCtx, Rule: "duplicate-row-title",
						Message: fmt.Sprintf("%s: this section title is also used by another row in this dashboard", context),
					})
				} else {
					rowTitles[title] = true
				}
			} else if titles[title] {
				findings = append(findings, Finding{
					Path: fileCtx, Rule: "duplicate-panel-title",
					Message: fmt.Sprintf("%s: this title is also used by another panel in this dashboard", context),
				})
			} else {
				titles[title] = true
			}
		}

		// A row carries no targets of its own - Grafana's row panel has
		// no query, only content panels do - so expr/metric checks are
		// skipped for it entirely.
		if !isRow {
			findings = append(findings, checkFilePanelExprs(p, panel, fileCtx, context, known, prefixes)...)
		}
	}

	for _, v := range grid.Check(rects) {
		findings = append(findings, Finding{Path: fileCtx, Rule: v.Kind, Message: v.Detail})
	}
	for _, v := range grid.CheckStability(rects) {
		findings = append(findings, Finding{Path: fileCtx, Rule: v.Kind, Message: v.Detail})
	}

	return findings
}

// gridPosComplete reports whether gp carries a non-null value for all
// four required keys.
func gridPosComplete(gp map[string]*int) bool {
	for _, k := range [...]string{"h", "w", "x", "y"} {
		if v, ok := gp[k]; !ok || v == nil {
			return false
		}
	}
	return true
}

// checkFilePanelExprs routes each of panel's target queries by its
// resolved datasource, exactly as checkPanelQuery does for a manifest-
// sourced panel: Grafana-variable-tolerant AST parse (checkPanelExpr)
// for a target that resolves to prometheus, vg_ token scan
// (checkQueryTokens) for anything else - every real dashboard's closing
// logs panel is loki-datasourced with a LogQL expr, not PromQL, and a
// target may itself name a datasource its panel does not.
func checkFilePanelExprs(p parser.Parser, panel filePanel, path, context string, known map[string]struct{}, prefixes []string) []Finding {
	var findings []Finding
	for _, t := range panel.Targets {
		if t.Expr == "" {
			continue
		}
		ds := resolvePanelDatasource(t.Datasource.typ, panel.Datasource.typ)
		if ds == promDatasource {
			findings = append(findings, checkPanelExpr(p, t.Expr, path, context, known, prefixes)...)
			continue
		}
		findings = append(findings, checkQueryTokens(t.Expr, path, context+" (datasource "+ds+", token-scanned)", known, prefixes)...)
	}
	return findings
}
