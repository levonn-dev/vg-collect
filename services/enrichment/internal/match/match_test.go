package match

import (
	"reflect"
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"The Legend of Zelda: Ocarina of Time": "legend of zelda ocarina of time",
		"Final Fantasy VII":                    "final fantasy 7",
		"Final Fantasy 7":                      "final fantasy 7",
		"Super Castlevania IV":                 "super castlevania 4",
		"Demon's Souls":                        "demons souls",
		"Chrono Trigger (USA) [cart only]":     "chrono trigger",
		"Okami":                                "okami",
		"THE THING":                            "thing",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConsoleMatches(t *testing.T) {
	yes := [][2]string{
		{"Super Nintendo Entertainment System", "Super Nintendo"},
		{"Sega Mega Drive/Genesis", "Sega Genesis"},
		{"Game Boy Advance", "GameBoy Advance"},
		{"Nintendo GameCube", "Gamecube"},
		{"PlayStation", "Playstation"},
		{"Some Future Platform", "Some Future Platform"}, // fallback equality
	}
	for _, c := range yes {
		if !ConsoleMatches(c[0], c[1], "") {
			t.Errorf("ConsoleMatches(%q, %q) = false, want true", c[0], c[1])
		}
	}
	no := [][2]string{
		{"Xbox 360", "Xbox"},
		{"Xbox", "Xbox 360"},
		{"PlayStation", "Playstation 2"},
		{"Super Nintendo Entertainment System", "NES"},
	}
	for _, c := range no {
		if ConsoleMatches(c[0], c[1], "") {
			t.Errorf("ConsoleMatches(%q, %q) = true, want false", c[0], c[1])
		}
	}
}

func TestBest(t *testing.T) {
	cands := []Candidate{
		{PCProductID: 1, Name: "Chrono Trigger", ConsoleName: "Super Nintendo"},
		{PCProductID: 2, Name: "Chrono Cross", ConsoleName: "Playstation"},
		{PCProductID: 3, Name: "Chrono Trigger", ConsoleName: "Playstation"},
	}
	got := Best([]string{"Chrono Trigger"}, "", "Super Nintendo Entertainment System", "", cands)
	if !got.OK || got.PCProductID != 1 || got.Confidence != 1.0 {
		t.Fatalf("exact same-console match: %+v", got)
	}

	// The console filter is hard: a perfect name on the wrong console
	// never matches.
	got = Best([]string{"Chrono Trigger"}, "", "Nintendo 64", "", cands)
	if got.OK {
		t.Fatalf("wrong console must not match: %+v", got)
	}

	// Near-name above threshold: {pokemon, firered, version} vs
	// {pokemon, firered} = 0.8.
	got = Best([]string{"Pokemon FireRed Version"}, "", "Game Boy Advance", "",
		[]Candidate{{PCProductID: 9, Name: "Pokemon FireRed", ConsoleName: "GameBoy Advance"}})
	if !got.OK || got.Confidence < 0.79 || got.Confidence > 0.81 {
		t.Fatalf("near-name: %+v", got)
	}

	// Different game below threshold: {chrono, trigger} vs
	// {chrono, cross} = 0.5 - unmatched, but the score is reported.
	got = Best([]string{"Chrono Trigger"}, "", "PlayStation", "",
		[]Candidate{{PCProductID: 3, Name: "Chrono Cross", ConsoleName: "Playstation"}})
	if got.OK || got.Confidence != 0.5 {
		t.Fatalf("below threshold: %+v", got)
	}

	// Roman numeral canonicalization crosses spellings.
	got = Best([]string{"Final Fantasy VII"}, "", "PlayStation", "",
		[]Candidate{{PCProductID: 7, Name: "Final Fantasy 7", ConsoleName: "Playstation"}})
	if !got.OK || got.Confidence != 1.0 {
		t.Fatalf("roman numerals: %+v", got)
	}

	// An edition is part of the identity: the edition listing wins and
	// a plain listing alone stays unmatched.
	edCands := []Candidate{
		{PCProductID: 11, Name: "Zelda", ConsoleName: "Nintendo 64"},
		{PCProductID: 12, Name: "Zelda Collectors Edition", ConsoleName: "Nintendo 64"},
	}
	got = Best([]string{"Zelda"}, "Collectors Edition", "Nintendo 64", "", edCands)
	if !got.OK || got.PCProductID != 12 {
		t.Fatalf("edition listing must win: %+v", got)
	}
	got = Best([]string{"Zelda"}, "Collectors Edition", "Nintendo 64", "", edCands[:1])
	if got.OK {
		t.Fatalf("plain listing must not price an edition: %+v", got)
	}

	// Deterministic tie-break: lower pc id.
	got = Best([]string{"Ico"}, "", "PlayStation 2", "", []Candidate{
		{PCProductID: 22, Name: "Ico", ConsoleName: "Playstation 2"},
		{PCProductID: 21, Name: "Ico", ConsoleName: "Playstation 2"},
	})
	if got.PCProductID != 21 {
		t.Fatalf("tie-break: %+v", got)
	}

	// No candidates at all.
	got = Best([]string{"Terranigma"}, "", "Super Nintendo Entertainment System", "", nil)
	if got.OK || got.Confidence != 0 {
		t.Fatalf("empty candidates: %+v", got)
	}
}

// TestBest_EmptyNames is the defensive counterpart: no name form to
// score against at all (nil names, no hint) must stay unmatched
// rather than panic, even with a console-eligible candidate present.
func TestBest_EmptyNames(t *testing.T) {
	cands := []Candidate{{PCProductID: 1, Name: "Chrono Trigger", ConsoleName: "Super Nintendo"}}
	got := Best(nil, "", "Super Nintendo Entertainment System", "", cands)
	if got.OK {
		t.Fatalf("empty names must not match: %+v", got)
	}
}

func TestBest_ApostropheVariantsMatchThroughNormalize(t *testing.T) {
	// The lower-level Normalize coverage proves straight/curly
	// apostrophes and a dropped apostrophe all fold to the same token;
	// this drives that through Best() itself, with an unrelated
	// neighbor present so the win is non-vacuous.
	cands := []Candidate{
		{PCProductID: 41, Name: "Demon\u2019s Souls", ConsoleName: "PlayStation"}, // curly apostrophe (Go unicode escape keeps the source ASCII)
		{PCProductID: 42, Name: "Chrono Cross", ConsoleName: "PlayStation"},       // unrelated neighbor
	}
	got := Best([]string{"Demon's Souls"}, "", "PlayStation", "", cands) // straight apostrophe
	if !got.OK || got.PCProductID != 41 || got.Confidence != 1.0 {
		t.Fatalf("curly-apostrophe candidate must win: %+v", got)
	}

	got = Best([]string{"Demon's Souls"}, "", "PlayStation", "",
		[]Candidate{{PCProductID: 43, Name: "Demons Souls", ConsoleName: "PlayStation"}}) // no apostrophe
	if !got.OK || got.PCProductID != 43 || got.Confidence != 1.0 {
		t.Fatalf("no-apostrophe candidate must win: %+v", got)
	}
}

func TestBest_BaseListingBeatsVariantRow(t *testing.T) {
	cands := []Candidate{
		{PCProductID: 5005, Name: "Super Mario 64", ConsoleName: "Nintendo 64"},
		{PCProductID: 5099, Name: "Super Mario 64 [Player's Choice]", ConsoleName: "Nintendo 64"},
	}
	res := Best([]string{"Super Mario 64"}, "", "Nintendo 64", "", cands)
	if !res.OK || res.PCProductID != 5005 {
		t.Fatalf("base listing must win for a plain resolve, got %+v", res)
	}
}

func TestBest_HintFlipsAnUnbracketedVariant(t *testing.T) {
	cands := []Candidate{
		{PCProductID: 901, Name: "Super Mario 64", ConsoleName: "Nintendo 64"},
		{PCProductID: 902, Name: "Super Mario 64 Players Choice", ConsoleName: "Nintendo 64"},
	}
	plain := Best([]string{"Super Mario 64"}, "", "Nintendo 64", "", cands)
	if !plain.OK || plain.PCProductID != 901 {
		t.Fatalf("plain name must pick the base listing: %+v", plain)
	}
	hinted := Best([]string{"Super Mario 64"}, "players choice", "Nintendo 64", "", cands)
	if !hinted.OK || hinted.PCProductID != 902 {
		t.Fatalf("hint must flip to the variant listing: %+v", hinted)
	}
	// A hint no candidate carries makes the match conservative.
	junk := Best([]string{"Super Mario 64"}, "grey cart brick", "Nintendo 64", "", cands)
	if junk.OK {
		t.Fatalf("unmatched hint must stay below threshold: %+v", junk)
	}
}

func TestBest_HintReachesBracketedVariant(t *testing.T) {
	cands := []Candidate{
		{PCProductID: 5005, Name: "Super Mario 64", ConsoleName: "Nintendo 64"},
		{PCProductID: 5099, Name: "Super Mario 64 [Not for Resale]", ConsoleName: "Nintendo 64"},
	}
	hinted := Best([]string{"Super Mario 64"}, "not for resale", "Nintendo 64", "", cands)
	if !hinted.OK || hinted.PCProductID != 5099 || hinted.Confidence != 1.0 {
		t.Fatalf("hint must reach the bracketed variant listing: %+v", hinted)
	}
	// Brackets in the hint read as their words, not as a segment to
	// strip: "[not for resale]" must not degrade to a plain resolve.
	bracketed := Best([]string{"Super Mario 64"}, "[not for resale]", "Nintendo 64", "", cands)
	if !bracketed.OK || bracketed.PCProductID != 5099 || bracketed.Confidence != 1.0 {
		t.Fatalf("bracketed hint must reach the variant listing: %+v", bracketed)
	}
	// A hint no listing carries still lands unmatched, bracketed or not.
	junk := Best([]string{"Super Mario 64"}, "[gray cart brick]", "Nintendo 64", "", cands)
	if junk.OK {
		t.Fatalf("unmatched bracketed hint must stay below threshold: %+v", junk)
	}
	// A bracket-only hint normalizes to nothing and reads as no hint:
	// the plain resolve behavior, base listing wins.
	empty := Best([]string{"Super Mario 64"}, "[]", "Nintendo 64", "", cands)
	if !empty.OK || empty.PCProductID != 5005 {
		t.Fatalf("empty bracket hint must behave as no hint: %+v", empty)
	}
}

func TestBest_PossessiveMatchesDroppedForm(t *testing.T) {
	// PriceCharting's NA listing drops the possessive entirely
	// (evidence: /api/products, 2026-07-15 - "Michael Jackson
	// Moonwalker" on Sega Genesis, id 9334).
	got := Best([]string{"Michael Jackson's Moonwalker"}, "", "Sega Mega Drive/Genesis", "",
		[]Candidate{{PCProductID: 9334, Name: "Michael Jackson Moonwalker", ConsoleName: "Sega Genesis"}})
	if !got.OK || got.PCProductID != 9334 || got.Confidence != 1.0 {
		t.Fatalf("possessive name must match the dropped-form listing: %+v", got)
	}
	// The reverse convention: bare name, possessive listing.
	got = Best([]string{"Michael Jackson Moonwalker"}, "", "Sega Mega Drive/Genesis", "",
		[]Candidate{{PCProductID: 46074, Name: "Michael Jackson's Moonwalker", ConsoleName: "Sega Genesis"}})
	if !got.OK || got.Confidence != 1.0 {
		t.Fatalf("bare name must match the possessive listing: %+v", got)
	}
	// The joined convention keeps working (pinned above in TestBest);
	// a possessive must not fold further than its own s.
	got = Best([]string{"Demon's Souls"}, "", "PlayStation", "",
		[]Candidate{{PCProductID: 43, Name: "Demons Souls", ConsoleName: "PlayStation"}})
	if !got.OK || got.Confidence != 1.0 {
		t.Fatalf("joined-form listing must still match: %+v", got)
	}
}

func TestProviderQuery_DropsPossessives(t *testing.T) {
	cases := map[string]string{
		"Michael Jackson's Moonwalker": "Michael Jackson Moonwalker",
		"Michael Jackson’s Moonwalker": "Michael Jackson Moonwalker",
		"Demon's Souls":                "Demon Souls",
		"Super Mario 64":               "Super Mario 64",
		"Let's Go Pikachu":             "Let Go Pikachu",
	}
	for in, want := range cases {
		if got := ProviderQuery(in); got != want {
			t.Errorf("ProviderQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameName_FoldsPossessives(t *testing.T) {
	if !SameName("Michael Jackson's Moonwalker", "michael jackson moonwalker") {
		t.Fatal("possessive and bare form must count as the same name")
	}
	if !SameName("Demon's Souls", "Demons Souls") {
		t.Fatal("possessive and joined form must count as the same name")
	}
	if SameName("Mega Man X", "Mega Man") {
		t.Fatal("distinct names must not fold")
	}
}

func TestScore(t *testing.T) {
	if s := Score("Chrono Trigger", "Chrono Trigger"); s < Threshold {
		t.Fatalf("identical names score %v, want >= Threshold", s)
	}
	if s := Score("Chrono Trigger", "The Legend of Zelda"); s >= Threshold {
		t.Fatalf("unrelated names score %v, want < Threshold", s)
	}
}

func TestPlatformAliases(t *testing.T) {
	// SNES unions the curated abbreviation and the PriceCharting spelling.
	got := PlatformAliases("Super Nintendo Entertainment System")
	want := []string{"snes", "super nintendo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snes aliases = %v, want %v", got, want)
	}
	// Case and punctuation fold through Normalize.
	ps := PlatformAliases("PlayStation")
	if !slices.Contains(ps, "ps1") || !slices.Contains(ps, "psx") {
		t.Fatalf("playstation aliases = %v, want ps1 and psx", ps)
	}
	// An unknown platform has no aliases and never panics.
	if a := PlatformAliases("Fairchild Channel F"); len(a) != 0 {
		t.Fatalf("unknown aliases = %v, want empty", a)
	}
}

func TestConsoleMatches_RegionClasses(t *testing.T) {
	cases := []struct {
		name                      string
		platform, console, region string
		want                      bool
	}{
		{"base alias unchanged", "Super Nintendo Entertainment System", "Super Nintendo", "", true},
		{"base rejects jp listing", "Super Nintendo Entertainment System", "Super Famicom", "", false},
		{"ntsc_u is base", "PlayStation 4", "Playstation 4", "ntsc_u", true},
		{"region_free is base", "PlayStation 4", "Playstation 4", "region_free", true},
		{"unknown region is base", "PlayStation 4", "Playstation 4", "someday_region", true},
		{"pal prefixes the alias", "PlayStation 4", "PAL Playstation 4", "pal", true},
		{"pal rejects the base listing", "PlayStation 4", "Playstation 4", "pal", false},
		{"pal prefixes equality platforms", "Sega Saturn", "PAL Sega Saturn", "pal", true},
		{"pal prefixes a jp-distinct platform", "Super Nintendo Entertainment System", "PAL Super Nintendo", "pal", true},
		{"pal rejects the jp-distinct name", "Super Nintendo Entertainment System", "Super Famicom", "pal", false},
		{"jp prefixes equality platforms", "Sega Saturn", "JP Sega Saturn", "ntsc_j", true},
		{"jp rejects the base listing", "Sega Saturn", "Sega Saturn", "ntsc_j", false},
		{"jp distinct name for snes", "Super Nintendo Entertainment System", "Super Famicom", "ntsc_j", true},
		{"jp distinct excludes prefix form", "Super Nintendo Entertainment System", "JP Super Nintendo", "ntsc_j", false},
		{"jp distinct name for nes", "Nintendo Entertainment System", "Famicom", "ntsc_j", true},
		{"jp twin platform by its own key", "Family Computer", "Famicom", "ntsc_j", true},
		{"jp distinct name for fds", "Family Computer Disk System", "Famicom Disk System", "ntsc_j", true},
		{"twin platform stays base-matchable by equality", "Super Famicom", "Super Famicom", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConsoleMatches(tc.platform, tc.console, tc.region); got != tc.want {
				t.Errorf("ConsoleMatches(%q, %q, %q) = %v, want %v", tc.platform, tc.console, tc.region, got, tc.want)
			}
		})
	}
}

func TestBest_MultiNameScoresBestForm(t *testing.T) {
	cands := []Candidate{
		{PCProductID: 5101, Name: "Seiken Densetsu 2", ConsoleName: "Super Famicom"},
	}
	res := Best([]string{"Seiken Densetsu 2", "Secret of Mana"}, "", "Super Nintendo Entertainment System", "ntsc_j", cands)
	if !res.OK || res.PCProductID != 5101 {
		t.Fatalf("want the translit form to win the JP listing, got %+v", res)
	}
	if res.Confidence != 1.0 {
		t.Errorf("want 1.0 from the exact translit, got %v", res.Confidence)
	}
}

func TestBest_RegionGateExcludesCrossClassListings(t *testing.T) {
	cands := []Candidate{
		{PCProductID: 1, Name: "Secret of Mana", ConsoleName: "Super Nintendo"},
		{PCProductID: 2, Name: "Secret of Mana", ConsoleName: "PAL Super Nintendo"},
	}
	if res := Best([]string{"Secret of Mana"}, "", "Super Nintendo Entertainment System", "pal", cands); !res.OK || res.PCProductID != 2 {
		t.Errorf("pal resolve must land the PAL listing only, got %+v", res)
	}
	if res := Best([]string{"Secret of Mana"}, "", "Super Nintendo Entertainment System", "ntsc_j", cands); res.OK {
		t.Errorf("ntsc_j resolve with no JP listing must stay unmatched, got %+v", res)
	}
}

func TestFilterConsole(t *testing.T) {
	cands := []Candidate{
		{PCProductID: 1, Name: "A", ConsoleName: "Super Nintendo"},
		{PCProductID: 2, Name: "B", ConsoleName: "Super Famicom"},
	}
	if got := FilterConsole("Super Nintendo Entertainment System", "ntsc_j", cands); len(got) != 1 || got[0].PCProductID != 2 {
		t.Errorf("ntsc_j filter: want only the Super Famicom row, got %+v", got)
	}
	if got := FilterConsole("Super Nintendo Entertainment System", "", cands); len(got) != 1 || got[0].PCProductID != 1 {
		t.Errorf("base filter: want only the Super Nintendo row, got %+v", got)
	}
}
