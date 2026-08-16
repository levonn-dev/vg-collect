package lint

import (
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
)

// fixtureKnown and fixturePrefixes are the known-metric set and
// external prefixes shared by every case in
// TestCheckDashboardFiles_Fixtures: none of testdata/files/*.json's
// panels reference a name that should resolve as known, and the
// prefixes list is a representative subset of the repo's external
// prefixes, sufficient for these test cases.
var (
	fixtureKnown    = map[string]struct{}{}
	fixturePrefixes = []string{"kube_", "node_", "up"}
)

// findingsForFile filters findings to the ones whose Path names file
// (e.g. "overlap.json") and, when rule is non-empty, whose Rule also
// matches - mirroring lint_test.go's own findingsWithRule helper. The
// rule filter matters once a single fixture legitimately produces more
// than one finding (dup-id-empty-title.json triggers both
// empty-panel-title and duplicate-panel-id - the first panel's blank
// title is its own real, separate problem): filtering on file alone
// would leave which finding is "the" one under test to slice order. An
// empty rule matches every finding in the file, for the one case
// (infra.json) asserting there are none at all, of any rule.
func findingsForFile(findings []Finding, file, rule string) []Finding {
	want := "files/dashboards/" + file
	var out []Finding
	for _, f := range findings {
		if f.Path != want {
			continue
		}
		if rule != "" && f.Rule != rule {
			continue
		}
		out = append(out, f)
	}
	return out
}

// TestCheckDashboardFiles_Fixtures tables every structural and expr/
// metric check checkDashboardFiles owns, against fixtures under
// testdata/files/ (all walked together in a single call, proving the
// directory walk itself as well as each individual check -
// checkDashboardFile resets its id/title/rect tracking per file, so the
// fixtures cannot interfere with each other's counts). Most fixtures
// carry one case each - exactly one (file, rule) finding, matched on a
// message substring - except dup-id-empty-title.json, which carries two
// (the id collision and the earlier panel's own blank title both fire,
// independently - see that case's own comment), and infra.json, which
// asserts zero findings of any rule, proving the vg_ jurisdiction and
// the datasource routing both stay quiet on real, legitimately-external
// content.
func TestCheckDashboardFiles_Fixtures(t *testing.T) {
	p := parser.NewParser(parser.Options{})
	findings := checkDashboardFiles("testdata/files", p, fixtureKnown, fixturePrefixes)

	cases := []struct {
		name       string
		file       string
		wantRule   string
		wantCount  int
		wantSubstr string
	}{
		{
			name:       "two panels overlap",
			file:       "overlap.json",
			wantRule:   "overlap",
			wantCount:  1,
			wantSubstr: `"Alpha" (x0 y0 w12 h8) overlaps "Beta" (x6 y4 w12 h8)`,
		},
		{
			name:       "a panel exceeds the 24-column grid",
			file:       "bounds.json",
			wantRule:   "bounds",
			wantCount:  1,
			wantSubstr: "x=20",
		},
		{
			name:       "two panels share an id",
			file:       "dup-id.json",
			wantRule:   "duplicate-panel-id",
			wantCount:  1,
			wantSubstr: `panel 1 "Second" reuses id 1 of panel 0 "First"`,
		},
		{
			// The earlier (colliding) panel's title is empty - proves the
			// duplicate-panel-id message still locates it, by index, when
			// its title alone could not (see files.go's idOccurrence).
			name:       "an id collision where the earlier panel has an empty title is still locatable by index",
			file:       "dup-id-empty-title.json",
			wantRule:   "duplicate-panel-id",
			wantCount:  1,
			wantSubstr: `panel 1 "Second" reuses id 1 of panel 0 ""`,
		},
		{
			// Same file as above: the earlier panel's blank title is also
			// its own, separate, independently-firing finding - proving
			// the two checks coexist rather than one masking the other.
			name:       "that same earlier panel's own blank title is still its own separate finding",
			file:       "dup-id-empty-title.json",
			wantRule:   "empty-panel-title",
			wantCount:  1,
			wantSubstr: "panel has no title",
		},
		{
			name:       "two panels share a title",
			file:       "dup-title.json",
			wantRule:   "duplicate-panel-title",
			wantCount:  1,
			wantSubstr: "also used by another panel in this dashboard",
		},
		{
			name:       "a panel's title is empty",
			file:       "empty-title.json",
			wantRule:   "empty-panel-title",
			wantCount:  1,
			wantSubstr: "panel has no title",
		},
		{
			name:       "a row panel carries nested panels the flat walk never descends into",
			file:       "row.json",
			wantRule:   "row-container",
			wantCount:  1,
			wantSubstr: `type "row" carries a nested panels array`,
		},
		{
			name:      "an expanded row (absent nested panels) plus packed panels below it is entirely clean",
			file:      "expanded-row.json",
			wantCount: 0,
		},
		{
			// The generator only ever emits "collapsed": false (see
			// internal/dashboards' row emission); a hand-edited
			// "collapsed": true on a row with no nested panels array at
			// all - the same "expanded" shape expanded-row.json above
			// proves is otherwise clean - renders as a stub collapsed
			// header in the Grafana UI with nothing behind it to expand.
			name:       "a hand-edited collapsed row with no children is a stub collapsed header",
			file:       "collapsed-row.json",
			wantRule:   "collapsed-row",
			wantCount:  1,
			wantSubstr: "row is collapsed",
		},
		{
			name:       "a non-row panel carrying a nested panels array is still a finding",
			file:       "nested-non-row.json",
			wantRule:   "row-container",
			wantCount:  1,
			wantSubstr: `type "timeseries" carries a nested panels array`,
		},
		{
			name:       "a panel floats up into an unoccupied hole beside a packed row",
			file:       "float.json",
			wantRule:   "float",
			wantCount:  1,
			wantSubstr: `"Floater" (x8 y9 w8 h8) would render above its authored row`,
		},
		{
			name:       "two expanded rows share a section title",
			file:       "dup-row-title.json",
			wantRule:   "duplicate-row-title",
			wantCount:  1,
			wantSubstr: "this section title is also used by another row in this dashboard",
		},
		{
			name:       "a panel has no gridPos at all",
			file:       "missing-gridpos.json",
			wantRule:   "incomplete-gridpos",
			wantCount:  1,
			wantSubstr: "gridPos is missing or incomplete",
		},
		{
			name:       "an unsubstituted {service} placeholder breaks PromQL parsing",
			file:       "unsub.json",
			wantRule:   "expr-parse-error",
			wantCount:  1,
			wantSubstr: "vg_{service}_cache_fail_open_total",
		},
		{
			name:       "a vg_ metric nothing registers",
			file:       "badmetric.json",
			wantRule:   "unknown-metric",
			wantCount:  1,
			wantSubstr: `vg_nonexistent_series_total" is not a known registration or external prefix`,
		},
		{
			// Also carries a second, empty-expr target on the same panel
			// (real dashboards never emit one, but a target's expr is
			// still optional per the decode shape) proving that branch is
			// silently skipped rather than fed to either check path. No
			// wantRule: zero findings of any rule at all.
			name:      "a non-vg_ prometheus name and a loki-datasourced LogQL panel both stay clean",
			file:      "infra.json",
			wantCount: 0,
		},
		{
			name:       "a file that is not valid json at all",
			file:       "malformed.json",
			wantRule:   "dashboard-file-scan-error",
			wantCount:  1,
			wantSubstr: "not valid json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches := findingsForFile(findings, tc.file, tc.wantRule)
			if len(matches) != tc.wantCount {
				t.Fatalf("file %s rule %q: got %d finding(s), want %d; got %+v", tc.file, tc.wantRule, len(matches), tc.wantCount, matches)
			}
			if tc.wantCount == 0 {
				return
			}
			if !strings.Contains(matches[0].Message, tc.wantSubstr) {
				t.Errorf("file %s finding Message = %q, want it to contain %q", tc.file, matches[0].Message, tc.wantSubstr)
			}
		})
	}
}

// TestCheckDashboardFiles_MissingDirectory proves a dir that does not
// exist is itself one dashboard-file-scan-error finding rather than a
// silent skip - the shipped dashboard tree is expected to always carry
// this directory, so its absence is exactly as real a problem as a bad
// panel inside one of its files.
func TestCheckDashboardFiles_MissingDirectory(t *testing.T) {
	const dir = "testdata/files-does-not-exist"
	p := parser.NewParser(parser.Options{})

	findings := checkDashboardFiles(dir, p, fixtureKnown, fixturePrefixes)
	if len(findings) != 1 {
		t.Fatalf("checkDashboardFiles(%q) = %d finding(s), want exactly 1; got %+v", dir, len(findings), findings)
	}
	if got := findings[0].Rule; got != "dashboard-file-scan-error" {
		t.Errorf("Rule = %q, want %q", got, "dashboard-file-scan-error")
	}
	if got := findings[0].Path; got != dir {
		t.Errorf("Path = %q, want %q", got, dir)
	}
	// The message itself is os.ReadDir's own platform-dependent error
	// text (e.g. "no such file or directory") - not this package's own
	// wording, so only Rule and Path are asserted, same discipline
	// lint_test.go's own "runbook file does not exist" case already
	// documents for os.ReadFile.
}

// TestCheckDashboardFiles_EmptyDirectory proves a real, existing
// directory with no *.json files in it (as opposed to a missing one) is
// simply zero findings, not an error - the distinction
// TestCheckDashboardFiles_MissingDirectory exists to prove the other
// side of.
func TestCheckDashboardFiles_EmptyDirectory(t *testing.T) {
	p := parser.NewParser(parser.Options{})

	findings := checkDashboardFiles(t.TempDir(), p, fixtureKnown, fixturePrefixes)
	if len(findings) != 0 {
		t.Fatalf("checkDashboardFiles(empty dir) = %+v, want no findings", findings)
	}
}
