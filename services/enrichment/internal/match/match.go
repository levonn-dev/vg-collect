// Package match scores PriceCharting candidates against a product's
// name and platform; below Threshold, products stay unmatched, never guessed.
package match

import (
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Threshold is the minimum similarity an auto-match must clear.
const Threshold = 0.75

// consoleAliases maps normalized IGDB platform names to normalized
// PriceCharting console-name spellings; unmapped platforms fall back to name equality.
var consoleAliases = map[string][]string{
	"nintendo entertainment system":       {"nes"},
	"super nintendo entertainment system": {"super nintendo"},
	"sega mega drive genesis":             {"sega genesis"},
	"nintendo 64":                         {"nintendo 64"},
	"playstation":                         {"playstation"},
	"playstation 2":                       {"playstation 2"},
	"nintendo gamecube":                   {"gamecube"},
	"xbox":                                {"xbox"},
	"game boy advance":                    {"gameboy advance"},
	"xbox 360":                            {"xbox 360"},
	"playstation 3":                       {"playstation 3"},
	"wii":                                 {"wii"},
	"nintendo ds":                         {"nintendo ds"},
	"wii u":                               {"wii u"},
	"playstation 4":                       {"playstation 4"},
	"nintendo switch":                     {"nintendo switch"},
	"nintendo 3ds":                        {"nintendo 3ds"},
}

// jpConsoleAliases maps normalized IGDB platform names to the
// distinct-name JP spellings PriceCharting uses instead of a "jp "
// prefix (Famicom-family markets); others take the prefix rule.
var jpConsoleAliases = map[string][]string{
	"nintendo entertainment system":       {"famicom"},
	"family computer":                     {"famicom"},
	"super nintendo entertainment system": {"super famicom"},
	"super famicom":                       {"super famicom"},
	"family computer disk system":         {"famicom disk system"},
}

// acceptedConsoles is the region-parameterized acceptance set for a
// platform. Base regions (ntsc_u, region_free, empty/unknown) keep
// pre-region behavior; acceptance is strict, so a JP resolve never accepts NA.
func acceptedConsoles(normPlatform, region string) []string {
	base, ok := consoleAliases[normPlatform]
	if !ok {
		base = []string{normPlatform}
	}
	switch region {
	case "ntsc_j":
		if jp, ok := jpConsoleAliases[normPlatform]; ok {
			return jp
		}
		return prefixed("jp ", base)
	case "pal":
		return prefixed("pal ", base)
	default:
		return base
	}
}

func prefixed(p string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = p + n
	}
	return out
}

// romanNumerals canonicalizes numeral tokens so "Final Fantasy VII"
// and "Final Fantasy 7" agree. Known tradeoff: "x" conflates with "10"
// (Mega Man X vs 10); the per-console filter and threshold absorb it.
var romanNumerals = map[string]string{
	"i": "1", "ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6",
	"vii": "7", "viii": "8", "ix": "9", "x": "10", "xi": "11",
	"xii": "12", "xiii": "13", "xiv": "14", "xv": "15", "xvi": "16",
}

// Normalize lowercases, strips bracketed segments and punctuation
// (apostrophes join rather than space-fold: "Demon's" -> "demons"),
// drops a leading article, and canonicalizes roman numerals.
func Normalize(name string) string {
	s := stripBrackets(strings.ToLower(name))
	var b strings.Builder
	for _, r := range s {
		if r == '\'' || r == '\u2019' {
			// Apostrophes join their surrounding letters instead of
			// breaking a token, unlike every other punctuation rune.
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	tokens := strings.Fields(b.String())
	if len(tokens) > 1 && tokens[0] == "the" {
		tokens = tokens[1:]
	}
	for i, t := range tokens {
		if n, ok := romanNumerals[t]; ok {
			tokens[i] = n
		}
	}
	return strings.Join(tokens, " ")
}

// dropPossessive removes apostrophe-s suffixes ("Jackson's" ->
// "Jackson"). PriceCharting is inconsistent about possessives across
// listings, so both the scorer and the provider query need this form.
func dropPossessive(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i := 0; i < len(r); i++ {
		if (r[i] == '\'' || r[i] == '’') && i+1 < len(r) && (r[i+1] == 's' || r[i+1] == 'S') &&
			(i+2 == len(r) || (!unicode.IsLetter(r[i+2]) && !unicode.IsDigit(r[i+2]))) {
			i++
			continue
		}
		b.WriteRune(r[i])
	}
	return b.String()
}

// ProviderQuery rewrites a catalog name for the provider's search:
// possessive suffixes drop to the bare token, since a possessive query
// misses listings named without one (the bare query returns the superset).
func ProviderQuery(name string) string {
	return dropPossessive(name)
}

// forms returns the normalized name, plus its possessive-dropped form
// when the raw text carries one, so scoring can meet either provider convention.
func forms(s string) []string {
	joined := Normalize(s)
	if dropped := Normalize(dropPossessive(s)); dropped != joined {
		return []string{joined, dropped}
	}
	return []string{joined}
}

// SameName reports whether two raw names agree after normalization,
// counting a possessive and its dropped form as the same name.
func SameName(a, b string) bool {
	for _, x := range forms(a) {
		if slices.Contains(forms(b), x) {
			return true
		}
	}
	return false
}

// maxDice scores every form pair and keeps the best: either side may
// carry the possessive the other dropped.
func maxDice(as, bs []string) float64 {
	m := 0.0
	for _, a := range as {
		for _, b := range bs {
			if d := dice(a, b); d > m {
				m = d
			}
		}
	}
	return m
}

// keepBracketContent turns bracket characters into spaces so bracketed
// segments survive Normalize as plain tokens instead of being stripped.
func keepBracketContent(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '(', ')', '[', ']':
			return ' '
		}
		return r
	}, s)
}

func stripBrackets(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for t := range strings.FieldsSeq(s) {
		out[t] = true
	}
	return out
}

// dice is the Sorensen-Dice coefficient over token sets.
func dice(a, b string) float64 {
	as, bs := tokenSet(a), tokenSet(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	inter := 0
	for t := range as {
		if bs[t] {
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(as)+len(bs))
}

// ConsoleMatches reports whether a PriceCharting console-name can
// price the IGDB platform for the entry region (hard filter: a
// perfect name on the wrong console never matches). Region "" is the base class.
func ConsoleMatches(platformName, consoleName, region string) bool {
	p, c := Normalize(platformName), Normalize(consoleName)
	return slices.Contains(acceptedConsoles(p, region), c)
}

// Candidate is one provider search hit under consideration.
type Candidate struct {
	PCProductID int64
	Name        string
	ConsoleName string
}

// Result is the scored outcome; OK=false means nothing cleared the
// Threshold (Confidence still carries the best score for logging).
type Result struct {
	PCProductID int64
	PCName      string
	ConsoleName string
	Confidence  float64
	OK          bool
}

// Best picks the highest-scoring candidate the region's console
// acceptance admits. names carries target forms, primary first (e.g.
// JP transliteration, then canonical). A non-empty hint qualifies
// every target ("name hint", strict) and keeps candidate brackets as
// tokens (PriceCharting brackets variants), so a hinted listing can
// win but an unmatched hint stays unmatched rather than guessing.
// Ties break on lower pc id.
func Best(names []string, hint, platformName, region string, cands []Candidate) Result {
	hinted := Normalize(keepBracketContent(hint)) != ""
	var targets []string
	for _, name := range names {
		raw := name
		if hinted {
			raw = keepBracketContent(name + " " + hint)
		}
		targets = append(targets, forms(raw)...)
	}
	best := Result{}
	for _, c := range cands {
		if !ConsoleMatches(platformName, c.ConsoleName, region) {
			continue
		}
		cn := c.Name
		if hinted {
			cn = keepBracketContent(cn)
		}
		score := maxDice(targets, forms(cn))
		better := score > best.Confidence ||
			(score == best.Confidence && best.OK && c.PCProductID < best.PCProductID)
		if score > 0 && better {
			best = Result{PCProductID: c.PCProductID, PCName: c.Name, ConsoleName: c.ConsoleName, Confidence: score, OK: true}
		}
	}
	if best.Confidence < Threshold {
		return Result{Confidence: best.Confidence}
	}
	return best
}

// FilterConsole returns the candidates the region's console
// acceptance admits; an empty result gates the auto-match fallback search.
func FilterConsole(platformName, region string, cands []Candidate) []Candidate {
	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		if ConsoleMatches(platformName, c.ConsoleName, region) {
			out = append(out, c)
		}
	}
	return out
}

// Score is the plain name-similarity score, with the same
// normalization Best applies. Exposed for the community candidate
// sweep, which has no listing/console context; Threshold applies at the call site.
func Score(a, b string) float64 {
	return maxDice(forms(a), forms(b))
}

// platformAbbreviations maps normalized IGDB platform names to the
// short forms collectors actually type; it complements consoleAliases,
// both feeding PlatformAliases so no call site hard-codes an abbreviation.
var platformAbbreviations = map[string][]string{
	"nintendo entertainment system":       {"nes"},
	"super nintendo entertainment system": {"snes"},
	"super famicom":                       {"sfc"},
	"nintendo 64":                         {"n64"},
	"nintendo gamecube":                   {"gcn", "gc"},
	"game boy":                            {"gb"},
	"game boy color":                      {"gbc"},
	"game boy advance":                    {"gba"},
	"nintendo ds":                         {"nds"},
	"nintendo 3ds":                        {"3ds"},
	"playstation":                         {"ps1", "psx"},
	"playstation 2":                       {"ps2"},
	"playstation 3":                       {"ps3"},
	"playstation 4":                       {"ps4"},
	"playstation 5":                       {"ps5"},
	"playstation portable":                {"psp"},
	"playstation vita":                    {"vita", "ps vita"},
	"sega mega drive genesis":             {"genesis", "mega drive"},
}

// PlatformAliases returns known alternate spellings and abbreviations
// for an IGDB platform name (caller does case-insensitive comparison).
// Unions consoleAliases with the abbreviation table.
func PlatformAliases(name string) []string {
	n := Normalize(name)
	seen := map[string]bool{}
	var out []string
	add := func(a string) {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			return
		}
		seen[a] = true
		out = append(out, a)
	}
	for _, a := range consoleAliases[n] {
		add(a)
	}
	for _, a := range platformAbbreviations[n] {
		add(a)
	}
	sort.Strings(out)
	return out
}
