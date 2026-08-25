// Package regionkit holds the region tables shared by services that resolve free-text region
// strings against a reviewed known set, plus the fold map promoting a string to its canonical region.
package regionkit

// KnownRegions is not a validation gate (region is open-world); it keys the machinery tables
// and each normalize lever's promotion target set. Derived from RegionNames so it never drifts from api/domain.yaml.
var KnownRegions = func() map[string]bool {
	m := make(map[string]bool, len(RegionNames))
	for _, r := range RegionNames {
		m[r] = true
	}
	return m
}()

// RegionSynonyms maps each known region to the reviewed free-text forms each normalize lever
// promotes. Fold-matched (lowercase, trim), exact-or-synonym, never fuzzy.
var RegionSynonyms = map[string][]string{
	"ntsc_u":      {"usa", "us", "ntsc", "ntsc-u", "north america"},
	"ntsc_j":      {"japan", "jp", "ntsc-j"},
	"pal":         {"europe", "eu"},
	"region_free": {"world", "worldwide", "region free"},
	"korea":       {"kr", "south korea"},
	"brazil":      {"br", "brasil"},
	"china":       {"cn"},
}

// RegionFoldMap builds fold -> canonical from the known values'
// identity folds plus the synonyms rows.
func RegionFoldMap() map[string]string {
	m := make(map[string]string, len(KnownRegions)+8)
	for k := range KnownRegions {
		m[k] = k
	}
	for canon, syns := range RegionSynonyms {
		for _, s := range syns {
			m[s] = canon
		}
	}
	return m
}
