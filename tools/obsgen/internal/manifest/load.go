package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/grid"
)

// alertsGoldenFile is alerts/golden.yaml's exact shape: the shared group,
// the service roster that drives which other files Load reads, and the
// golden template definitions services instantiate from.
type alertsGoldenFile struct {
	Group                  AlertGroup          `yaml:"group"`
	Datasource             string              `yaml:"datasource"`
	Services               []string            `yaml:"services"`
	ExternalMetricPrefixes []string            `yaml:"external_metric_prefixes"`
	Templates              map[string]Template `yaml:"templates"`
}

// clusterFile is alerts/cluster.yaml's exact shape: a bare rule list, the
// same shape a service file's own alerts: list uses.
type clusterFile struct {
	Alerts []Rule `yaml:"alerts"`
}

// dashGoldenFile is dashboards/golden.yaml's exact shape: named blocks
// of verbatim Grafana panel JSON fragments (gridPos is a panel JSON key, not a sibling manifest field).
type dashGoldenFile struct {
	Blocks map[string]Block `yaml:"blocks"`
}

// serviceDashFile is dashboards/<service>.yaml's exact shape; nested
// dashboard.uid/title flatten onto ServiceDash. GoldenBlocks/Sections are
// optional (nil map, not an error, if omitted). CustomPanels decodes as
// []string since yaml.v3 has no scalar-to-[]byte conversion; Load
// converts each entry after decode.
type serviceDashFile struct {
	Service   string `yaml:"service"`
	Dashboard struct {
		UID   string `yaml:"uid"`
		Title string `yaml:"title"`
	} `yaml:"dashboard"`
	GoldenBlocks map[string]int `yaml:"golden_blocks"`
	Sections     map[string]int `yaml:"sections"`
	CustomPanels []string       `yaml:"custom_panels"`
}

// Load reads every manifest file under dir and assembles the
// fully-validated Model, collecting every problem into one joined error
// instead of stopping at the first. A non-nil error always means a nil Model.
func Load(dir string) (*Model, error) {
	var errs []error

	golden, err := loadAlertsGolden(dir)
	if err != nil {
		errs = append(errs, err)
		golden = &alertsGoldenFile{}
	} else if err := checkServiceRosterOrder(golden.Services); err != nil {
		errs = append(errs, err)
	}

	cluster, err := loadCluster(dir)
	if err != nil {
		errs = append(errs, err)
	}

	loadedServices, serviceErrs := loadServiceAlerts(dir, golden.Services)
	errs = append(errs, serviceErrs...)

	retired, err := loadRetired(dir)
	if err != nil {
		errs = append(errs, err)
	}

	errs = append(errs, checkUIDs(cluster, loadedServices, retired)...)

	services := make([]ServiceAlerts, len(loadedServices))
	for i, ls := range loadedServices {
		services[i] = ls.alerts
	}

	dashGolden, err := loadDashGolden(dir)
	if err != nil {
		errs = append(errs, err)
	}

	dashServices, dashErrs := loadServiceDash(dir, golden.Services)
	errs = append(errs, dashErrs...)

	if len(dashErrs) == 0 {
		errs = append(errs, validateSections(dashServices)...)
		if err == nil {
			errs = append(errs, validateGoldenBlockRefs(dashGolden, dashServices)...)
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &Model{
		Alerts: AlertTree{
			Group:                  golden.Group,
			Datasource:             golden.Datasource,
			ExternalMetricPrefixes: golden.ExternalMetricPrefixes,
			Templates:              golden.Templates,
			Cluster:                cluster,
			Services:               services,
			Retired:                retired,
		},
		Dashboards: DashTree{
			Blocks:   dashGolden,
			Services: dashServices,
		},
	}, nil
}

// decodeFile reads rel (relative to dir) and strictly decodes it as yaml
// into out; KnownFields fails a typo'd field instead of silently dropping it.
func decodeFile(dir, rel string, out any) error {
	path := filepath.Join(dir, rel)
	data, err := os.ReadFile(path) //nolint:gosec // G304: rel is always one of this package's own fixed or roster-derived manifest filenames, not external input.
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("parsing %s: %w", rel, err)
	}
	return nil
}

func loadAlertsGolden(dir string) (*alertsGoldenFile, error) {
	var f alertsGoldenFile
	if err := decodeFile(dir, filepath.Join("alerts", "golden.yaml"), &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func loadCluster(dir string) ([]Rule, error) {
	var f clusterFile
	if err := decodeFile(dir, filepath.Join("alerts", "cluster.yaml"), &f); err != nil {
		return nil, err
	}
	return f.Alerts, nil
}

// loadedServiceAlerts pairs a loaded alerts file with the path it was
// read from (kept separate from the decoded service: field, which nothing checks against it).
type loadedServiceAlerts struct {
	path   string
	alerts ServiceAlerts
}

// loadServiceAlerts reads alerts/<service>.yaml for every service the
// golden roster declares, in roster order - the loader preserves whatever
// order the source file declares rather than imposing its own.
func loadServiceAlerts(dir string, roster []string) ([]loadedServiceAlerts, []error) {
	var (
		out  []loadedServiceAlerts
		errs []error
	)
	for _, name := range roster {
		rel := filepath.Join("alerts", name+".yaml")
		var sa ServiceAlerts
		if err := decodeFile(dir, rel, &sa); err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, loadedServiceAlerts{path: rel, alerts: sa})
	}
	return out, errs
}

func loadRetired(dir string) ([]RetiredUID, error) {
	var r []RetiredUID
	if err := decodeFile(dir, filepath.Join("alerts", "retired.yaml"), &r); err != nil {
		return nil, err
	}
	return r, nil
}

// checkServiceRosterOrder validates alerts/golden.yaml's services list
// is alphabetically ordered; neither Assemble nor Emit re-sorts it
// themselves, so enforcing it once here keeps generated output ordered.
func checkServiceRosterOrder(services []string) error {
	for i := 1; i < len(services); i++ {
		if services[i] < services[i-1] {
			return fmt.Errorf("alerts/golden.yaml: services roster is not alphabetically ordered: %q comes after %q", services[i], services[i-1])
		}
	}
	return nil
}

// checkUIDs enforces two rules: no two live rules share a uid, and no
// retired uid collides with a live one. Golden-template uids (unexpanded
// {service}) are excluded - unique per template key, expansion is generation's job.
func checkUIDs(cluster []Rule, services []loadedServiceAlerts, retired []RetiredUID) []error {
	var errs []error
	live := make(map[string]string) // uid -> the file it first appeared in

	record := func(uid, source string) {
		if prev, ok := live[uid]; ok {
			errs = append(errs, fmt.Errorf("duplicate alert uid %q: used in both %s and %s", uid, prev, source))
			return
		}
		live[uid] = source
	}

	for _, r := range cluster {
		record(r.UID, filepath.Join("alerts", "cluster.yaml"))
	}
	for _, svc := range services {
		for _, r := range svc.alerts.Alerts {
			record(r.UID, svc.path)
		}
	}

	for _, ret := range retired {
		if source, ok := live[ret.UID]; ok {
			errs = append(errs, fmt.Errorf("retired uid %q (alerts/retired.yaml) collides with the live rule in %s", ret.UID, source))
		}
	}

	return errs
}

// loadDashGolden reads golden.yaml and validates every block: at least
// one panel, each with a complete, in-bounds gridPos (Grafana's grid is
// 24 columns). Blocks are validated in sorted order for deterministic error output.
func loadDashGolden(dir string) (map[string]Block, error) {
	rel := filepath.Join("dashboards", "golden.yaml")
	var f dashGoldenFile
	if err := decodeFile(dir, rel, &f); err != nil {
		return nil, err
	}

	var errs []error
	for _, name := range slices.Sorted(maps.Keys(f.Blocks)) {
		block := f.Blocks[name]
		if len(block.Panels) == 0 {
			errs = append(errs, fmt.Errorf("parsing %s: block %q has no panels", rel, name))
			continue
		}
		for i, raw := range block.Panels {
			if err := validateBlockPanelGeometry(raw); err != nil {
				errs = append(errs, fmt.Errorf("parsing %s: block %q panel %d: %w", rel, name, i, err))
			}
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return f.Blocks, nil
}

// validateBlockPanelGeometry checks gridPos is present, complete (h/w/x/y
// all set, checked as a raw key set so omitted differs from explicit
// zero), and in-bounds via internal/grid.Check (reused, not duplicated).
func validateBlockPanelGeometry(raw string) error {
	var shape struct {
		GridPos json.RawMessage `json:"gridPos"`
	}
	if err := json.Unmarshal([]byte(raw), &shape); err != nil {
		return fmt.Errorf("not valid json: %w", err)
	}
	if len(shape.GridPos) == 0 {
		return errors.New("missing gridPos")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(shape.GridPos, &fields); err != nil {
		return fmt.Errorf("gridPos: %w", err)
	}
	for _, k := range [...]string{"h", "w", "x", "y"} {
		if _, ok := fields[k]; !ok {
			return fmt.Errorf("gridPos missing %q", k)
		}
	}

	var gp GridPos
	if err := json.Unmarshal(shape.GridPos, &gp); err != nil {
		return fmt.Errorf("gridPos: %w", err)
	}
	if v := grid.Check([]grid.Rect{{X: gp.X, Y: gp.Y, W: gp.W, H: gp.H}}); len(v) > 0 {
		return fmt.Errorf("gridPos out of bounds: %s", v[0].Detail)
	}
	return nil
}

// loadServiceDash reads dashboards/<service>.yaml for every roster
// service, the same roster-driven discovery loadServiceAlerts uses.
func loadServiceDash(dir string, roster []string) ([]ServiceDash, []error) {
	var (
		out  []ServiceDash
		errs []error
	)
	for _, name := range roster {
		var f serviceDashFile
		if err := decodeFile(dir, filepath.Join("dashboards", name+".yaml"), &f); err != nil {
			errs = append(errs, err)
			continue
		}
		panels := make([]json.RawMessage, len(f.CustomPanels))
		for i, s := range f.CustomPanels {
			panels[i] = json.RawMessage(s)
		}
		out = append(out, ServiceDash{
			Service:      f.Service,
			UID:          f.Dashboard.UID,
			Title:        f.Dashboard.Title,
			GoldenBlocks: f.GoldenBlocks,
			Sections:     f.Sections,
			CustomPanels: panels,
		})
	}
	return out, errs
}

// validateSections checks each title is non-empty and each anchor is
// non-negative, independently (a single entry can fail both). Uniqueness
// needs no check: Sections is a Go map keyed on title. Sorted title order
// keeps a multi-error joined output deterministic.
func validateSections(services []ServiceDash) []error {
	var errs []error
	for _, sd := range services {
		rel := filepath.Join("dashboards", sd.Service+".yaml")
		for _, title := range slices.Sorted(maps.Keys(sd.Sections)) {
			if title == "" {
				errs = append(errs, fmt.Errorf("%s: sections: empty section title", rel))
			}
			if anchor := sd.Sections[title]; anchor < 0 {
				errs = append(errs, fmt.Errorf("%s: sections: section %q: negative anchor %d", rel, title, anchor))
			}
		}
	}
	return errs
}

// validateGoldenBlockRefs checks each golden_blocks key names a defined
// block and each anchor is non-negative - the opposite direction of
// internal/lint's checkGoldenBlocks (a block being referenced).
func validateGoldenBlockRefs(golden map[string]Block, services []ServiceDash) []error {
	var errs []error
	for _, sd := range services {
		rel := filepath.Join("dashboards", sd.Service+".yaml")
		for _, name := range slices.Sorted(maps.Keys(sd.GoldenBlocks)) {
			if _, ok := golden[name]; !ok {
				errs = append(errs, fmt.Errorf("%s: golden_blocks: undefined block %q", rel, name))
				continue
			}
			if anchor := sd.GoldenBlocks[name]; anchor < 0 {
				errs = append(errs, fmt.Errorf("%s: golden_blocks: block %q: negative anchor %d", rel, name, anchor))
			}
		}
	}
	return errs
}
