package match

import (
	"encoding/json"
	"os"
	"testing"
)

// The alias table bridges two vocabularies it doesn't own: IGDB
// platform names (keys) and PriceCharting console spellings (values).
// Every key/value must appear in the fixtures, or it's an invented spelling that never matches.
func loadCorpus(t *testing.T) (platformNames, consoleNames []string) {
	t.Helper()
	var ig struct {
		Platforms []struct {
			Name string `json:"name"`
		} `json:"platforms"`
	}
	b, err := os.ReadFile("../igdb/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &ig); err != nil {
		t.Fatal(err)
	}
	for _, p := range ig.Platforms {
		platformNames = append(platformNames, p.Name)
	}

	var pc struct {
		Products []struct {
			ConsoleName string `json:"console-name"`
		} `json:"products"`
	}
	b, err = os.ReadFile("../pricecharting/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pc); err != nil {
		t.Fatal(err)
	}
	for _, p := range pc.Products {
		consoleNames = append(consoleNames, p.ConsoleName)
	}
	return platformNames, consoleNames
}

func TestConsoleAliases_MatchFixtureCorpora(t *testing.T) {
	platformNames, consoleNames := loadCorpus(t)
	normPlatforms := map[string]bool{}
	for _, n := range platformNames {
		normPlatforms[Normalize(n)] = true
	}
	normConsoles := map[string]bool{}
	for _, n := range consoleNames {
		normConsoles[Normalize(n)] = true
	}

	for key, values := range consoleAliases {
		if !normPlatforms[key] {
			t.Errorf("alias key %q is not a fixture IGDB platform name", key)
		}
		for _, v := range values {
			if !normConsoles[v] {
				t.Errorf("alias %q -> %q: no fixture product uses that console-name", key, v)
			}
		}
	}

	for key, values := range jpConsoleAliases {
		if !normPlatforms[key] {
			t.Errorf("jp alias key %q is not a fixture IGDB platform name", key)
		}
		for _, v := range values {
			if !normConsoles[v] {
				t.Errorf("jp alias %q -> %q: no fixture product uses that console-name", key, v)
			}
		}
	}

	// Reverse direction: every corpus platform must reach at least one
	// console spelling in some region class (a JP-market platform prices
	// nothing in the base class by design, so reachability is per-class).
	for _, pn := range platformNames {
		matched := false
		for _, cn := range consoleNames {
			for _, region := range []string{"", "ntsc_j", "pal"} {
				if ConsoleMatches(pn, cn, region) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			t.Errorf("fixture platform %q matches no fixture console-name in any region class", pn)
		}
	}
}
