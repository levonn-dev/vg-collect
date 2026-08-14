// Command obsgen turns the manifests under deploy/observability into the
// Grafana alert provisioning file and per-service dashboards, and lints
// that same tree (plus the repo content it references: runbook anchors,
// registered metric names) for the drift class that motivated it - see
// internal/lint's package doc.
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

// rulesRelPath and dashboardsRelDir are deploy/observability/'s two
// generated destinations, fixed paths relative to out-root regardless
// of where the manifest tree itself lives - the real deploy/charts/
// platform files in production, a scratch directory in a test.
const (
	rulesRelPath     = "deploy/charts/platform/files/alerting/vg-rules.yaml"
	dashboardsRelDir = "deploy/charts/platform/files/dashboards"
)

// rulesHeader is vg-rules.yaml's leading comment line - plain YAML's
// only comment placement that does not disturb the document. It
// carries the exact same sentence dashboards.GeneratedHeader embeds in
// each dashboard's own description field (JSON has no comment syntax),
// so the two generated output kinds say the identical thing without
// that sentence being copied by hand into a second place.
var rulesHeader = "# " + dashboards.GeneratedHeader + "\n"

// runGen loads the manifest tree at manifestDir, assembles the
// dashboards and rules, and writes both to their canonical locations
// under outRoot. outRoot is a parameter, not a hardcoded ".", so a test
// can point it at a scratch directory instead of a real checkout - see
// TestRunGen_WritesOutputs.
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

// writeOutputs writes vg-rules.yaml (with its leading generated-by
// comment prepended) and every dashboard json file (each already
// carrying its own generated-by description field) under outRoot,
// creating whatever directories do not exist yet. File mode 0o600
// matches every golden-file write already in this module (see e.g.
// internal/dashboards/assemble_test.go's own -update path) - these are
// files the local user regenerates and reviews before committing, not
// ones any other local account needs to read.
func writeOutputs(outRoot string, rulesYAML []byte, dashFiles map[string][]byte) error {
	// G703 (gosec) flags every path below as tainted, since each traces
	// back through filepath.Join to outRoot - main's own <out-root> CLI
	// argument, never external input; dashFiles' keys are internal/
	// dashboards.Assemble's own "<service>.json" output, likewise never
	// external. Same trust boundary internal/manifest/load.go's
	// decodeFile already documents (there tagged G304) for its own file
	// reads.
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

// runLint loads the manifest tree at manifestDir and reports every
// lint.Run finding against repoRoot to stdout, one per line, before
// returning a plain error (triggering main's nonzero exit) if there
// were any. A load failure short-circuits before lint ever runs, same
// as runGen: a tree that does not even parse has nothing for lint to
// meaningfully check.
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
