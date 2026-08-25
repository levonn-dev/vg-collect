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

// This file walks the shipped dashboard JSON under
// deploy/charts/platform/files/dashboards (generated and hand-authored
// alike) directly off disk, reusing checkPanelExpr/checkQueryTokens/
// grid.Check/grid.CheckStability rather than duplicating them. Unlike
// Assemble, which enforces compaction stability only once a dashboard
// declares a section, this walk runs CheckStability unconditionally on
// every file.

// fileDashboard and filePanel decode just enough of one dashboard file.
// GridPos is a map, not a struct, so a missing/null h/w/x/y is
// distinguishable from a real zero (gridPosComplete checks key
// presence). Panels never decodes past json.RawMessage: this walk is deliberately flat.
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
// recursive), sorted; a missing/unreadable dir is itself one finding.
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

// idOccurrence records where a panel id was first seen (index + title),
// so a later duplicate can name it unambiguously; title alone can be empty or itself duplicated.
type idOccurrence struct {
	index int
	title string
}

// checkDashboardFile decodes and checks one dashboard file. Every
// finding's Path is fileCtx ("files/dashboards/<name>.json"), not path,
// so it reads the same regardless of repoRoot.
func checkDashboardFile(path, name string, p parser.Parser, known map[string]struct{}, prefixes []string) []Finding {
	fileCtx := "files/dashboards/" + name

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is filepath.Join(dir, name) over os.ReadDir's own listing, not external input.
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

		// a panel with nested panels is flagged on sight rather than walked
		// into (this walk is flat); an expanded row (no children) is the
		// one container shape NOT flagged, since it pins a section, not hides content.
		if len(panel.Panels) > 0 {
			findings = append(findings, Finding{
				Path: fileCtx, Rule: "row-container",
				Message: fmt.Sprintf("%s: type %q carries a nested panels array; the file walk is flat and never descends into a container, which would silently exempt its children from every check", context, panel.Type),
			})
		}

		// the generator only emits expanded rows ("collapsed": false); a
		// collapsed row is always a hand edit, hiding content behind a
		// click-to-expand header the row-container check above wouldn't catch.
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

		// a nil id is fine (Grafana assigns one at save time); only a real,
		// repeated id is tracked. Panels are named by index too, since title alone can be empty or duplicated.
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

		// a row's title is a section header, a separate namespace from
		// content panel titles - they never collide with each other, only within their own kind.
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

		// a row carries no targets (Grafana's row panel has no query), so expr/metric checks are skipped for it.
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

// checkFilePanelExprs routes each target by its resolved datasource,
// like checkPanelQuery: AST parse for prometheus, vg_ token scan
// otherwise (e.g. a loki-datasourced LogQL panel).
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
