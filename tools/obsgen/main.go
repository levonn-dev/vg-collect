// Command obsgen turns deploy/observability manifests into Grafana alert
// rules and per-service dashboards, and lints the tree (and the runbook
// anchors and metric names it references) for drift.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/alerts"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/dashboards"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/lint"
	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "gen":
		args := os.Args[2:]
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: obsgen gen <manifest-dir> <out-root>")
			os.Exit(2)
		}
		if err := runGen(args[0], args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "obsgen gen:", err)
			os.Exit(1)
		}
	case "lint":
		args := os.Args[2:]
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: obsgen lint <manifest-dir> <repo-root>")
			os.Exit(2)
		}
		if err := runLint(args[0], args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "obsgen lint:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "obsgen: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: obsgen <subcommand>")
	fmt.Fprintln(os.Stderr, "  gen <manifest-dir> <out-root>    load manifests, write vg-rules.yaml and dashboard json under out-root")
	fmt.Fprintln(os.Stderr, "  lint <manifest-dir> <repo-root>  load manifests, report every lint finding against repo-root")
}

// rulesRelPath and dashboardsRelDir are fixed paths relative to out-root
// (deploy/charts/platform in production, a scratch dir in tests).
const (
	rulesRelPath     = "deploy/charts/platform/files/alerting/vg-rules.yaml"
	dashboardsRelDir = "deploy/charts/platform/files/dashboards"
)

// rulesHeader shares dashboards.GeneratedHeader's sentence, so
// vg-rules.yaml and every dashboard say the identical generated-by line.
var rulesHeader = "# " + dashboards.GeneratedHeader + "\n"

// runGen loads manifestDir, assembles dashboards and rules, and writes
// both under outRoot (a parameter so tests can use a scratch dir).
func runGen(manifestDir, outRoot string) error {
	m, err := manifest.Load(manifestDir)
	if err != nil {
		return fmt.Errorf("loading manifests: %w", err)
	}

	dashFiles, idx, err := dashboards.Assemble(m)
	if err != nil {
		return fmt.Errorf("assembling dashboards: %w", err)
	}

	rulesYAML, err := alerts.Emit(m, idx)
	if err != nil {
		return fmt.Errorf("emitting alerts: %w", err)
	}

	if err := writeOutputs(outRoot, rulesYAML, dashFiles); err != nil {
		return fmt.Errorf("writing outputs: %w", err)
	}
	return nil
}

// writeOutputs writes vg-rules.yaml and every dashboard json under
// outRoot, creating directories as needed (mode 0o600: local, reviewed files).
func writeOutputs(outRoot string, rulesYAML []byte, dashFiles map[string][]byte) error {
	// G703 flags every path below as tainted; outRoot is a CLI argument and
	// dashFiles' keys are internal output, neither is external input.
	rulesPath := filepath.Join(outRoot, rulesRelPath)
	if err := os.MkdirAll(filepath.Dir(rulesPath), 0o750); err != nil { //nolint:gosec // G703: see above.
		return err
	}
	body := append([]byte(rulesHeader), rulesYAML...)
	if err := os.WriteFile(rulesPath, body, 0o600); err != nil { //nolint:gosec // G703: see above.
		return err
	}

	dashDir := filepath.Join(outRoot, dashboardsRelDir)
	if err := os.MkdirAll(dashDir, 0o750); err != nil { //nolint:gosec // G703: see above.
		return err
	}
	names := make([]string, 0, len(dashFiles))
	for name := range dashFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dashDir, name), dashFiles[name], 0o600); err != nil { //nolint:gosec // G703: see above.
			return err
		}
	}
	return nil
}

// runLint reports every lint.Run finding to stdout, then errors if any
// were found; a load failure short-circuits first, same as runGen.
func runLint(manifestDir, repoRoot string) error {
	m, err := manifest.Load(manifestDir)
	if err != nil {
		return fmt.Errorf("loading manifests: %w", err)
	}

	findings := lint.Run(m, repoRoot)
	if len(findings) == 0 {
		return nil
	}
	for _, f := range findings {
		fmt.Printf("%s: %s: %s\n", f.Path, f.Rule, f.Message)
	}
	return fmt.Errorf("%d lint finding(s)", len(findings))
}
