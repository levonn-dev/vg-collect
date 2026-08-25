// Package manifest loads and validates the observability source files
// under deploy/observability (alert rules and Grafana dashboard panels)
// into a single in-memory model. Generation and lint steps consume that
// model; they never read the source files themselves.
package manifest

import "encoding/json"

// Model is the fully-validated result of Load: everything the alert and
// dashboard sides need, assembled from every file under one
// deploy/observability tree.
type Model struct {
	Alerts     AlertTree
	Dashboards DashTree
}

// AlertTree is the alert half of the model, in source-file order.
// Services is additionally guaranteed alphabetical: Load rejects a
// non-alphabetical roster (DashTree.Services shares the same order).
type AlertTree struct {
	Group                  AlertGroup
	Datasource             string
	ExternalMetricPrefixes []string
	Templates              map[string]Template
	Cluster                []Rule
	Services               []ServiceAlerts
	Retired                []RetiredUID
}

// DashTree is the dashboard half of the model: the shared golden blocks
// and every service's dashboard file.
type DashTree struct {
	Blocks   map[string]Block
	Services []ServiceDash
}

// AlertGroup is the single Grafana alert rule group every generated rule
// belongs to.
type AlertGroup struct {
	Name     string `yaml:"name"`
	Folder   string `yaml:"folder"`
	Interval string `yaml:"interval"`
}

// Template is a golden alert definition keyed by name in the golden
// templates map, with {service}/{Service} left for generation to substitute.
type Template struct {
	UID          string `yaml:"uid"`
	Title        string `yaml:"title"`
	Expr         string `yaml:"expr"`
	Condition    string `yaml:"condition"`
	Instant      bool   `yaml:"instant"`
	For          string `yaml:"for"`
	NoDataState  string `yaml:"noDataState"`
	ExecErrState string `yaml:"execErrState"`
	Severity     string `yaml:"severity"`
	Summary      string `yaml:"summary"`
	Runbook      string `yaml:"runbook"`
	PanelRef     string `yaml:"panel_ref"`
}

// Overrides holds the only fields a service may change instantiating a
// template; zero-value means "use the template's value". uid/expr are
// deliberately absent: a different expr needs a custom rule instead.
type Overrides struct {
	For       string `yaml:"for"`
	Condition string `yaml:"condition"`
	Severity  string `yaml:"severity"`
	Summary   string `yaml:"summary"`
}

// ServiceAlerts is one service's alert file: which golden templates it
// instantiates (with optional overrides, keyed by template name) plus its
// own fully custom rules.
type ServiceAlerts struct {
	Service string               `yaml:"service"`
	Golden  map[string]Overrides `yaml:"golden"`
	Alerts  []Rule               `yaml:"alerts"`
}

// Rule is a fully custom alert rule (cluster-scoped or per-service).
// Range and Datasource are the two fields Template never needs: Range is
// the relativeTimeRange window; Datasource overrides AlertTree.Datasource
// when set, else inherits it.
type Rule struct {
	UID          string `yaml:"uid"`
	Title        string `yaml:"title"`
	Expr         string `yaml:"expr"`
	Condition    string `yaml:"condition"`
	Instant      bool   `yaml:"instant"`
	Range        string `yaml:"range"`
	For          string `yaml:"for"`
	NoDataState  string `yaml:"noDataState"`
	ExecErrState string `yaml:"execErrState"`
	Severity     string `yaml:"severity"`
	Summary      string `yaml:"summary"`
	Runbook      string `yaml:"runbook"`
	PanelRef     string `yaml:"panel_ref"`
	Datasource   string `yaml:"datasource"`
}

// RetiredUID is one entry in alerts/retired.yaml: a uid that used to be a
// live rule and now only needs a deleteRules entry in the generated output.
type RetiredUID struct {
	UID    string `yaml:"uid"`
	Date   string `yaml:"date"`
	Reason string `yaml:"reason"`
}

// GridPos is a Grafana panel's position/size, using Grafana's own field
// names. Always parsed from a panel fragment's JSON, never yaml directly (json tags only).
type GridPos struct {
	H int `json:"h"`
	W int `json:"w"`
	X int `json:"x"`
	Y int `json:"y"`
}

// Block is one named entry in dashboards/golden.yaml's blocks map:
// verbatim panel JSON fragments sharing one gridPos.y space. Each
// fragment's own gridPos.y is block-relative, an offset from a
// service's golden_blocks anchor.
type Block struct {
	Panels []string `yaml:"panels"`
}

// ServiceDash is one service's dashboard file: GoldenBlocks maps block
// name -> y anchor, Sections maps section title -> literal y anchor. Nil
// Sections is a valid, ordinary dashboard; declaring a section is a
// service's only opt-in into compaction-stable layout (see internal/dashboards.Assemble).
type ServiceDash struct {
	Service      string
	UID          string
	Title        string
	GoldenBlocks map[string]int
	Sections     map[string]int
	CustomPanels []json.RawMessage
}
