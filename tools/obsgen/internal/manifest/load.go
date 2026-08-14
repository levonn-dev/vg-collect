package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
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

// dashGoldenFile is dashboards/golden.yaml's exact shape: a bare list of
// verbatim Grafana panel JSON fragments, the same shape a service file's
// own custom_panels list uses (real Grafana panel JSON carries gridPos as
// one of its own keys, not a sibling manifest field - see e.g. any panel in
// deploy/charts/platform/files/dashboards/*.json).
type dashGoldenFile struct {
	Panels []string `yaml:"panels"`
}

// serviceDashFile is dashboards/<service>.yaml's exact shape; its nested
// dashboard.uid/dashboard.title flatten onto ServiceDash once decoded.
// CustomPanels decodes as []string rather than []json.RawMessage directly:
// yaml.v3 has no built-in conversion from a scalar node to a []byte-shaped
// type, so Load converts each entry once the strict decode succeeds.
type serviceDashFile struct {
	Service   string `yaml:"service"`
	Dashboard struct {
		UID   string `yaml:"uid"`
		Title string `yaml:"title"`
	} `yaml:"dashboard"`
	CustomPanels []string `yaml:"custom_panels"`
}

// Load reads every manifest file under dir (deploy/observability by
// convention) and assembles the fully-validated Model. It collects every
// problem it finds - a strict-decode failure, a missing per-service file, a
// uid collision - into one joined error instead of stopping at the first
// one, so a single Load call surfaces everything a fix needs to address. A
// non-nil error always means a nil Model: callers never see a partially
// valid tree.
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
			Golden:   dashGolden,
			Services: dashServices,
		},
	}, nil
}

// decodeFile reads rel (relative to dir) and strictly decodes it as yaml
// into out. KnownFields rejects any key out's type does not declare, so a
// typo'd manifest field fails Load instead of silently vanishing; every
// error names rel so the offending file is never in doubt.
func decodeFile(dir, rel string, out any) error {
	path := filepath.Join(dir, rel)
	data, err := os.ReadFile(path) //nolint:gosec // G304: rel is always one of this package's own fixed or roster-derived manifest filenames, never external input.
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

// loadedServiceAlerts pairs one loaded per-service alerts file with the
// relative path Load actually read it from. The path is kept separate from
// alerts.Service (the file's own decoded service: field) because nothing
// checks the two agree - a uid-collision error must cite the file that was
// really parsed, not a path reconstructed from content that could be
// stale, copy-pasted, or simply wrong.
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

// checkServiceRosterOrder validates that alerts/golden.yaml's services
// list is alphabetically ordered. Neither internal/dashboards.Assemble
// nor internal/alerts.Emit re-sorts m.Alerts.Services/m.Dashboards.Services
// themselves - both walk the roster in the order Load preserved it, so
// enforcing the order once here, at the single source both sides read
// their own per-service ordering from, is what keeps generated output
// ordered without either downstream package needing its own check.
func checkServiceRosterOrder(services []string) error {
	for i := 1; i < len(services); i++ {
		if services[i] < services[i-1] {
			return fmt.Errorf("alerts/golden.yaml: services roster is not alphabetically ordered: %q comes after %q", services[i], services[i-1])
		}
	}
	return nil
}

// checkUIDs enforces the two cross-file uid rules the loader owns: no two
// live (non-retired) rules share a uid, and no retired uid collides with
// one still live. Golden-template uids carry an unexpanded {service}
// placeholder and are excluded - they are unique per template key, not per
// literal string, and expanding them is generation's job, not the loader's.
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

// loadDashGolden reads dashboards/golden.yaml and, for each panel fragment,
// pulls its gridPos out into GoldenPanel's own field alongside the verbatim
// fragment - a convenience the loader can offer once here rather than every
// downstream reader re-parsing the same shared fragment's JSON on its own.
func loadDashGolden(dir string) ([]GoldenPanel, error) {
	rel := filepath.Join("dashboards", "golden.yaml")
	var f dashGoldenFile
	if err := decodeFile(dir, rel, &f); err != nil {
		return nil, err
	}

	var errs []error
	panels := make([]GoldenPanel, len(f.Panels))
	for i, raw := range f.Panels {
		var shape struct {
			GridPos GridPos `json:"gridPos"`
		}
		if err := json.Unmarshal([]byte(raw), &shape); err != nil {
			errs = append(errs, fmt.Errorf("parsing %s: panel %d: %w", rel, i, err))
			continue
		}
		panels[i] = GoldenPanel{
			Fragment: json.RawMessage(raw),
			GridPos:  shape.GridPos,
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return panels, nil
}

// loadServiceDash reads dashboards/<service>.yaml for every service the
// golden roster declares - the same roster-driven discovery
// loadServiceAlerts uses for the alert side, so a service missing its
// dashboard file fails Load the same way one missing its alerts file does.
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
			CustomPanels: panels,
		})
	}
	return out, errs
}
