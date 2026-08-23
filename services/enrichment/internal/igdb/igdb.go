// Package igdb speaks the IGDB v4 API: shared response types, a real
// client (Twitch app token + APICalypse + client-side rate limit), and
// a stub client over embedded fixtures. The mode switch (IGDB_MODE)
// picks which one main.go wires in; both expose the same method set.
package igdb

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/levonn-dev/vgkeep/libs/go/regionkit"
)

// Named is any IGDB reference expanded to {id, name} (genres, themes,
// franchises, platforms, companies).
type Named struct {
	ID   int64  `json:"id" bson:"id"`
	Name string `json:"name" bson:"name"`
}

// Cover carries the image id used to build image URLs.
type Cover struct {
	ImageID string `json:"image_id" bson:"image_id"`
}

// InvolvedCompany links a company with its role flags.
type InvolvedCompany struct {
	Company   Named `json:"company" bson:"company"`
	Developer bool  `json:"developer" bson:"developer"`
	Publisher bool  `json:"publisher" bson:"publisher"`
}

// ReleaseDate is one row of the per-platform, per-region release
// table. Platform and Region arrive unexpanded (raw IGDB ids; Region
// is the release_region catalog id, mapped to a name via RegionName).
type ReleaseDate struct {
	Date     int64 `json:"date,omitempty" bson:"date,omitempty"`
	Platform int64 `json:"platform,omitempty" bson:"platform,omitempty"`
	Region   int   `json:"release_region,omitempty" bson:"release_region,omitempty"`
}

// AlternativeName is one alternate title with IGDB's free-text
// comment taxonomy ("Japanese title - romanization", "Acronym", ...).
type AlternativeName struct {
	Name    string `json:"name" bson:"name"`
	Comment string `json:"comment,omitempty" bson:"comment,omitempty"`
}

// LocalizationRegion arrives identifier-expanded (ja-JP, EU, ko-KR):
// self-describing, so no enum map can go stale when IGDB adds rows.
type LocalizationRegion struct {
	Identifier string `json:"identifier" bson:"identifier"`
}

// GameLocalization is one region's presentation of a game; name and
// cover are independently optional (rows are sparse).
type GameLocalization struct {
	Name   string             `json:"name,omitempty" bson:"name,omitempty"`
	Region LocalizationRegion `json:"region" bson:"region"`
	Cover  *Cover             `json:"cover,omitempty" bson:"cover,omitempty"`
}

// CoverURL builds the t_cover_big URL for the localization's own box
// art, or "" without one.
func (l GameLocalization) CoverURL() string {
	if l.Cover == nil || l.Cover.ImageID == "" {
		return ""
	}
	return imageBase + "t_cover_big/" + l.Cover.ImageID + ".jpg"
}

// Game is the projection this service requests from /v4/games (and the
// shape igdb_raw persists). Expanded references always include ids.
type Game struct {
	ID                int64              `json:"id" bson:"id"`
	Name              string             `json:"name" bson:"name"`
	Cover             *Cover             `json:"cover,omitempty" bson:"cover,omitempty"`
	Genres            []Named            `json:"genres,omitempty" bson:"genres,omitempty"`
	Themes            []Named            `json:"themes,omitempty" bson:"themes,omitempty"`
	Franchises        []Named            `json:"franchises,omitempty" bson:"franchises,omitempty"`
	SimilarGames      []int64            `json:"similar_games,omitempty" bson:"similar_games,omitempty"`
	InvolvedCompanies []InvolvedCompany  `json:"involved_companies,omitempty" bson:"involved_companies,omitempty"`
	FirstReleaseDate  int64              `json:"first_release_date,omitempty" bson:"first_release_date,omitempty"`
	ReleaseDates      []ReleaseDate      `json:"release_dates" bson:"release_dates"`
	Platforms         []Named            `json:"platforms,omitempty" bson:"platforms,omitempty"`
	AlternativeNames  []AlternativeName  `json:"alternative_names,omitempty" bson:"alternative_names,omitempty"`
	GameLocalizations []GameLocalization `json:"game_localizations,omitempty" bson:"game_localizations,omitempty"`
	TotalRating       float64            `json:"total_rating,omitempty" bson:"total_rating,omitempty"`
	TotalRatingCount  int                `json:"total_rating_count,omitempty" bson:"total_rating_count,omitempty"`
}

// Platform is the /v4/platforms projection, persisted as-is into the
// platforms collection (bson _id = the IGDB platform id).
type Platform struct {
	ID           int64  `json:"id" bson:"_id"`
	Name         string `json:"name" bson:"name"`
	Abbreviation string `json:"abbreviation,omitempty" bson:"abbreviation,omitempty"`
	Generation   int    `json:"generation,omitempty" bson:"generation,omitempty"`
	PlatformLogo *Cover `json:"platform_logo,omitempty" bson:"platform_logo,omitempty"`
}

const imageBase = "https://images.igdb.com/igdb/image/upload/"

// CoverURL builds the t_cover_big image URL, or "" without a cover.
func (g Game) CoverURL() string {
	if g.Cover == nil || g.Cover.ImageID == "" {
		return ""
	}
	return imageBase + "t_cover_big/" + g.Cover.ImageID + ".jpg"
}

// ReleaseDate returns the first release date as a UTC calendar date;
// the zero time when IGDB lists none.
func (g Game) ReleaseDate() time.Time {
	if g.FirstReleaseDate == 0 {
		return time.Time{}
	}
	return time.Unix(g.FirstReleaseDate, 0).UTC().Truncate(24 * time.Hour)
}

// RegionName resolves the region enum against regionkit.ReleaseRegionNames
// (generated from api/domain.yaml's release_regions rows); ok=false
// means an enum value this service does not know (skip the row).
func RegionName(r int) (string, bool) {
	name, ok := regionkit.ReleaseRegionNames[r]
	return name, ok
}

// altTagRule mines a region's titles out of the free-text
// alternative_names comment taxonomy: comments starting with prefix
// feed the region's bundle; the exclude substring drops English
// translations ("Japanese title - translated"), which are neither the
// native form nor a transliteration.
type altTagRule struct {
	prefix  string
	exclude string
}

// altTagFamilies is the per-region mining table; regions without a
// family (EU) mine nothing and use their row fields alone. IGDB files
// Chinese titles under script-explicit comments, so the two scripts
// mine into separate identifiers and an entry region's chain decides
// their precedence.
var altTagFamilies = map[string]altTagRule{
	"ja-JP": {prefix: "japanese title", exclude: "translat"},
	"ko-KR": {prefix: "korean title", exclude: "translat"},
	"zh-CN": {prefix: "simplified chinese title", exclude: "translat"},
	"zh-TW": {prefix: "traditional chinese title", exclude: "translat"},
	"pt-BR": {prefix: "portuguese title", exclude: "translat"},
}

// LocalizationBundle is one region's presentation of a game, merged
// from its game_localizations row (authoritative for the native name
// and cover) and tag-family alternative names (ASCII names fill the
// transliteration slot; non-Latin names fall back into the native
// slot when the row has none).
type LocalizationBundle struct {
	Region   string
	Name     string
	Translit string
	CoverURL string
}

func matchesFamily(comment string, rule altTagRule) bool {
	c := strings.ToLower(comment)
	return strings.HasPrefix(c, rule.prefix) && !strings.Contains(c, rule.exclude)
}

// asciiOnly classifies the transliteration slot; anything carrying a
// non-ASCII letter is a native-script form.
func asciiOnly(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// BundleLocalizations derives the per-region bundles, sorted by
// region; only bundles with at least one non-empty field survive.
func BundleLocalizations(g Game) []LocalizationBundle {
	byRegion := map[string]*LocalizationBundle{}
	get := func(region string) *LocalizationBundle {
		if b := byRegion[region]; b != nil {
			return b
		}
		b := &LocalizationBundle{Region: region}
		byRegion[region] = b
		return b
	}
	for _, loc := range g.GameLocalizations {
		if loc.Region.Identifier == "" {
			continue
		}
		b := get(loc.Region.Identifier)
		if loc.Name != "" {
			b.Name = loc.Name
		}
		if cu := loc.CoverURL(); cu != "" {
			b.CoverURL = cu
		}
	}
	for region, rule := range altTagFamilies {
		for _, alt := range g.AlternativeNames {
			if alt.Name == "" || !matchesFamily(alt.Comment, rule) {
				continue
			}
			b := get(region)
			if asciiOnly(alt.Name) {
				if b.Translit == "" {
					b.Translit = alt.Name
				}
			} else if b.Name == "" {
				b.Name = alt.Name
			}
		}
	}
	var out []LocalizationBundle
	for _, b := range byRegion {
		if b.Name != "" || b.Translit != "" || b.CoverURL != "" {
			out = append(out, *b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Region < out[j].Region })
	return out
}

// TwinPlatformID returns the JP regional twin of a platform id, or 0
// when the platform has none (regionkit.TwinPlatformIDs, generated
// from api/domain.yaml platforms[].twin_igdb_id). The store folds a
// twin's release rows into its sibling's projection so a SNES product
// still shows its japan (Super Famicom) date.
//
// The pairing is symmetric (a lookup from either side yields the
// other): a Super Nintendo product (19) and its Super Famicom (58)
// counterpart are the same console to a collector, but the japan
// release row rides the Famicom platform id; same for NES (18) and
// Family Computer (99). These two pairs are the complete set (full
// 220-row catalog swept 2026-07-16): IGDB keeps every other
// regional-name pair as ONE combined entry (Sega Mega Drive/Genesis
// 29, Master System/Mark III 64, TurboGrafx-16/PC Engine 86 and its
// CD 150), and the remaining JP-flavored entries are different
// devices with their own libraries (Famicom Disk System 51,
// SuperGrafx 128, Satellaview 306, 64DD 416), which must NOT fold.
func TwinPlatformID(id int64) int64 {
	return regionkit.TwinPlatformIDs[id]
}

// LogoURL builds the t_logo_med image URL, or "" without a logo.
func (p Platform) LogoURL() string {
	if p.PlatformLogo == nil || p.PlatformLogo.ImageID == "" {
		return ""
	}
	return imageBase + "t_logo_med/" + p.PlatformLogo.ImageID + ".jpg"
}
