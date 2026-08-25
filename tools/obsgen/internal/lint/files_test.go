package lint

import (
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
)

// fixtureKnown and fixturePrefixes are the known-metric set and
// external prefixes shared by every TestCheckDashboardFiles_Fixtures case.
var (
	fixtureKnown    = map[string]struct{}{}
	fixturePrefixes = []string{"kube_", "node_", "up"}
)

// findingsForFile filters findings to Path == file and, if rule is
// non-empty, Rule == rule; an empty rule matches every finding (for
// asserting zero findings of any rule).
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

// TestCheckDashboardFiles_Fixtures tables every check checkDashboardFiles
// owns, against testdata/files/ fixtures walked together in one call
// (per-file tracking resets, so fixtures can't interfere).
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
			// the earlier panel's title is empty, proving the message still locates it by index.
			name:       "an id collision where the earlier panel has an empty title is still locatable by index",
			file:       "dup-id-empty-title.json",
			wantRule:   "duplicate-panel-id",
			wantCount:  1,
			wantSubstr: `panel 1 "Second" reuses id 1 of panel 0 ""`,
		},
		{
			// same file: the earlier panel's blank title is its own separate finding, proving the two checks coexist.
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
			// the generator only emits "collapsed": false; this row (otherwise
			// the clean "expanded" shape) is hand-edited true, a stub header with nothing behind it.
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
			// also carries a second, empty-expr target on the same panel,
			// proving that branch is silently skipped rather than checked.
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

// TestCheckDashboardFiles_MissingDirectory proves a missing dir is
// itself one dashboard-file-scan-error finding, not a silent skip.
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
	// the message is os.ReadDir's own platform-dependent text, so only Rule and Path are asserted.
}

// TestCheckDashboardFiles_EmptyDirectory proves an existing directory
// with no *.json files is zero findings, not an error.
func TestCheckDashboardFiles_EmptyDirectory(t *testing.T) {
	p := parser.NewParser(parser.Options{})

	findings := checkDashboardFiles(t.TempDir(), p, fixtureKnown, fixturePrefixes)
	if len(findings) != 0 {
		t.Fatalf("checkDashboardFiles(empty dir) = %+v, want no findings", findings)
	}
}
