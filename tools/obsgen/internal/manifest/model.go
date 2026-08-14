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

// AlertTree is the alert half of the model: the shared group and golden
// template definitions, cluster-scoped rules, every service's alert file,
// and the retired-uid list, each in the order its source file declared it.
// Services carries one further guarantee beyond source order: Load
// rejects a tree whose services roster is not alphabetically ordered, so
// any AlertTree a caller actually receives has Services (and, since both
// sides share the same roster, DashTree.Services) in alphabetical order
// too.
type AlertTree struct {
	Group                  AlertGroup
	Datasource             string
	ExternalMetricPrefixes []string
	Templates              map[string]Template
	Cluster                []Rule
	Services               []ServiceAlerts
	Retired                []RetiredUID
}

// DashTree is the dashboard half of the model: the shared golden panel
// block and every service's dashboard file.
type DashTree struct {
	Golden   []GoldenPanel
	Services []ServiceDash
}

// AlertGroup is the single Grafana alert rule group every generated rule
// belongs to.
type AlertGroup struct {
	Name     string `yaml:"name"`
	Folder   string `yaml:"folder"`
	Interval string `yaml:"interval"`
}

// Template is a golden alert definition keyed by name (e.g. "availability")
// in the golden templates map. Every service that opts into a template gets
// one instance of it, with {service}/{Service} left for generation to
// substitute.
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

// Overrides holds the only fields a service may change when it instantiates
// a golden template; a zero-value field means "use the template's own
// value". uid and expr are deliberately absent - a service needing a
// different expr writes a custom rule instead of overriding one.
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

// Rule is a fully custom alert rule: a cluster-scoped entry in
// alerts/cluster.yaml, or a per-service entry in a service's own alerts
// list. Its fields duplicate Template's rather than embedding it, matching
// every other manifest type's flat shape and keeping Rule decodable
// directly as a list element. Range is one field a golden Template never
// needs: the relativeTimeRange window for a range-vector query (e.g.
// increase(...[26h])). Datasource is the other: a rule's data source is
// the same Prometheus instance for every rule except one (a Loki-backed
// log query), so a rule only sets it to override AlertTree.Datasource,
// leaving it empty to inherit that tree-wide default. A golden Template
// has no equivalent field, and Overrides has no equivalent slot - every
// template query today is the same Prometheus instance the tree default
// already names.
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

// GridPos is a Grafana panel's position and size on its dashboard grid,
// using the same field names Grafana's own panel JSON does. It is always
// populated by parsing a panel fragment's own JSON (see loadDashGolden),
// never decoded from yaml directly, so it carries json tags only.
type GridPos struct {
	H int `json:"h"`
	W int `json:"w"`
	X int `json:"x"`
	Y int `json:"y"`
}

// GoldenPanel is one entry in dashboards/golden.yaml's shared panel block:
// a verbatim Grafana panel JSON fragment (with {service}/{Service}
// placeholders for generation to substitute) and the fixed grid position
// every instantiation of it uses.
type GoldenPanel struct {
	Fragment json.RawMessage
	GridPos  GridPos
}

// ServiceDash is one service's dashboard file: its historical uid and
// title (flattened here from the manifest's nested dashboard: block) plus
// whatever custom panels it defines beyond the golden block.
type ServiceDash struct {
	Service      string
	UID          string
	Title        string
	CustomPanels []json.RawMessage
}
