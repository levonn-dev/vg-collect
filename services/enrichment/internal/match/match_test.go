package match

import "testing"

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
		if !ConsoleMatches(c[0], c[1]) {
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
		if ConsoleMatches(c[0], c[1]) {
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
	got := Best("Chrono Trigger", "", "Super Nintendo Entertainment System", cands)
	if !got.OK || got.PCProductID != 1 || got.Confidence != 1.0 {
		t.Fatalf("exact same-console match: %+v", got)
	}

	// The console filter is hard: a perfect name on the wrong console
	// never matches.
	got = Best("Chrono Trigger", "", "Nintendo 64", cands)
	if got.OK {
		t.Fatalf("wrong console must not match: %+v", got)
	}

	// Near-name above threshold: {pokemon, firered, version} vs
	// {pokemon, firered} = 0.8.
	got = Best("Pokemon FireRed Version", "", "Game Boy Advance",
		[]Candidate{{PCProductID: 9, Name: "Pokemon FireRed", ConsoleName: "GameBoy Advance"}})
	if !got.OK || got.Confidence < 0.79 || got.Confidence > 0.81 {
		t.Fatalf("near-name: %+v", got)
	}

	// Different game below threshold: {chrono, trigger} vs
	// {chrono, cross} = 0.5 - unmatched, but the score is reported.
	got = Best("Chrono Trigger", "", "PlayStation",
		[]Candidate{{PCProductID: 3, Name: "Chrono Cross", ConsoleName: "Playstation"}})
	if got.OK || got.Confidence != 0.5 {
		t.Fatalf("below threshold: %+v", got)
	}

	// Roman numeral canonicalization crosses spellings.
	got = Best("Final Fantasy VII", "", "PlayStation",
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
	got = Best("Zelda", "Collectors Edition", "Nintendo 64", edCands)
	if !got.OK || got.PCProductID != 12 {
		t.Fatalf("edition listing must win: %+v", got)
	}
	got = Best("Zelda", "Collectors Edition", "Nintendo 64", edCands[:1])
	if got.OK {
		t.Fatalf("plain listing must not price an edition: %+v", got)
	}

	// Deterministic tie-break: lower pc id.
	got = Best("Ico", "", "PlayStation 2", []Candidate{
		{PCProductID: 22, Name: "Ico", ConsoleName: "Playstation 2"},
		{PCProductID: 21, Name: "Ico", ConsoleName: "Playstation 2"},
	})
	if got.PCProductID != 21 {
		t.Fatalf("tie-break: %+v", got)
	}

	// No candidates at all.
	got = Best("Terranigma", "", "Super Nintendo Entertainment System", nil)
	if got.OK || got.Confidence != 0 {
		t.Fatalf("empty candidates: %+v", got)
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
	got := Best("Demon's Souls", "", "PlayStation", cands) // straight apostrophe
	if !got.OK || got.PCProductID != 41 || got.Confidence != 1.0 {
		t.Fatalf("curly-apostrophe candidate must win: %+v", got)
	}

	got = Best("Demon's Souls", "", "PlayStation",
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
	res := Best("Super Mario 64", "", "Nintendo 64", cands)
	if !res.OK || res.PCProductID != 5005 {
		t.Fatalf("base listing must win for a plain resolve, got %+v", res)
	}
}
