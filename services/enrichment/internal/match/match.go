// Package match scores PriceCharting candidates against a product's
// name and platform. Pure string logic; the confidence threshold gates
// auto-matching (below it, products stay unmatched - never guessed).
package match

import (
	"strings"
	"unicode"
)

// Threshold is the minimum similarity an auto-match must clear.
const Threshold = 0.75

// consoleAliases maps normalized IGDB platform names to normalized
// PriceCharting console-name spellings. Platforms without an entry
// fall back to name equality.
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

// romanNumerals canonicalizes numeral tokens so "Final Fantasy VII"
// and "Final Fantasy 7" agree. Known tradeoff: "x" conflates with
// "10" (Mega Man X vs Mega Man 10); the per-console filter and the
// threshold absorb it in practice.
var romanNumerals = map[string]string{
	"i": "1", "ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6",
	"vii": "7", "viii": "8", "ix": "9", "x": "10", "xi": "11",
	"xii": "12", "xiii": "13", "xiv": "14", "xv": "15", "xvi": "16",
}

// Normalize lowercases, strips bracketed segments and punctuation
// (apostrophes are removed rather than space-folded, so a possessive's
// letters join: "Demon's" -> "demons"), drops a leading article, and
// canonicalizes roman numerals.
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
	for _, t := range strings.Fields(s) {
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

// ConsoleMatches reports whether a PriceCharting console-name belongs
// to the IGDB platform (hard filter: a perfect name on the wrong
// console is never a match).
func ConsoleMatches(platformName, consoleName string) bool {
	p, c := Normalize(platformName), Normalize(consoleName)
	if aliases, ok := consoleAliases[p]; ok {
		for _, a := range aliases {
			if a == c {
				return true
			}
		}
		return false
	}
	return p == c
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

// Best picks the highest-scoring same-console candidate. A non-empty
// hint is variant text qualifying the target: scoring compares
// "name hint" strictly, so candidates without the hinted tokens lose
// score, and a hint nothing carries keeps the product unmatched
// rather than guessing the plain listing (bracketed candidate
// segments are stripped by Normalize, so bracket-only variants stay
// reachable through the picker, not the hint). Deterministic
// tie-break: lower pc id.
func Best(name, hint, platformName string, cands []Candidate) Result {
	target := Normalize(name)
	if hint != "" {
		target = Normalize(name + " " + hint)
	}
	best := Result{}
	for _, c := range cands {
		if !ConsoleMatches(platformName, c.ConsoleName) {
			continue
		}
		score := dice(target, Normalize(c.Name))
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
