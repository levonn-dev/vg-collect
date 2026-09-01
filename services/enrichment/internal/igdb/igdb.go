// Package igdb speaks the IGDB v4 API: shared response types, a real
// client (Twitch app token + APICalypse + rate limit), and a stub over
// embedded fixtures; IGDB_MODE picks which main.go wires in.
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
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Cover carries the image id used to build image URLs.
type Cover struct {
	ImageID string `json:"image_id"`
}

// InvolvedCompany links a company with its role flags.
type InvolvedCompany struct {
	Company   Named `json:"company"`
	Developer bool  `json:"developer"`
	Publisher bool  `json:"publisher"`
}

// ReleaseDate is one row of the per-platform, per-region release
// table; Platform and Region arrive as raw IGDB ids (Region via RegionName).
type ReleaseDate struct {
	Date     int64 `json:"date,omitempty"`
	Platform int64 `json:"platform,omitempty"`
	Region   int   `json:"release_region,omitempty"`
}

// AlternativeName is one alternate title with IGDB's free-text
// comment taxonomy ("Japanese title - romanization", "Acronym", ...).
type AlternativeName struct {
	Name    string `json:"name"`
	Comment string `json:"comment,omitempty"`
}

// LocalizationRegion arrives identifier-expanded (ja-JP, EU, ko-KR):
// self-describing, so no enum map can go stale when IGDB adds rows.
type LocalizationRegion struct {
	Identifier string `json:"identifier"`
}

// GameLocalization is one region's presentation of a game; name and
// cover are independently optional (rows are sparse).
type GameLocalization struct {
	Name   string             `json:"name,omitempty"`
	Region LocalizationRegion `json:"region"`
	Cover  *Cover             `json:"cover,omitempty"`
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
	ID                int64              `json:"id"`
	Name              string             `json:"name"`
	Cover             *Cover             `json:"cover,omitempty"`
	Genres            []Named            `json:"genres,omitempty"`
	Themes            []Named            `json:"themes,omitempty"`
	Franchises        []Named            `json:"franchises,omitempty"`
	SimilarGames      []int64            `json:"similar_games,omitempty"`
	InvolvedCompanies []InvolvedCompany  `json:"involved_companies,omitempty"`
	FirstReleaseDate  int64              `json:"first_release_date,omitempty"`
	ReleaseDates      []ReleaseDate      `json:"release_dates"`
	Platforms         []Named            `json:"platforms,omitempty"`
	AlternativeNames  []AlternativeName  `json:"alternative_names,omitempty"`
	GameLocalizations []GameLocalization `json:"game_localizations,omitempty"`
	TotalRating       float64            `json:"total_rating,omitempty"`
	TotalRatingCount  int                `json:"total_rating_count,omitempty"`
}

// Platform is the /v4/platforms projection, persisted as-is into the
// platforms table (ID is the IGDB platform id).
type Platform struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Abbreviation string `json:"abbreviation,omitempty"`
	Generation   int    `json:"generation,omitempty"`
	PlatformLogo *Cover `json:"platform_logo,omitempty"`
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

// RegionName resolves the region enum against regionkit.ReleaseRegionNames;
// ok=false means an unknown enum value (skip the row).
func RegionName(r int) (string, bool) {
	name, ok := regionkit.ReleaseRegionNames[r]
	return name, ok
}

// altTagRule mines a region's titles from the free-text
// alternative_names comment taxonomy: prefix selects the region;
// exclude drops English translations ("...- translated").
type altTagRule struct {
	prefix  string
	exclude string
}

// altTagFamilies is the per-region mining table; regions without one
// (EU) use their row fields alone. Chinese titles split into two
// script-explicit identifiers (zh-CN/zh-TW); the entry region's chain decides precedence.
var altTagFamilies = map[string]altTagRule{
	"ja-JP": {prefix: "japanese title", exclude: "translat"},
	"ko-KR": {prefix: "korean title", exclude: "translat"},
	"zh-CN": {prefix: "simplified chinese title", exclude: "translat"},
	"zh-TW": {prefix: "traditional chinese title", exclude: "translat"},
	"pt-BR": {prefix: "portuguese title", exclude: "translat"},
}

// LocalizationBundle is one region's presentation, merged from its
// game_localizations row (authoritative for name/cover) and tag-family
// alternative names (ASCII fills translit; non-Latin backfills name).
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
// (regionkit.TwinPlatformIDs, from api/domain.yaml twin_igdb_id).
// Symmetric pairing folds a twin's release rows into its sibling's
// projection (a SNES product shows its Super Famicom japan date).
//
// The complete set: SNES(19)/Super Famicom(58) and NES(18)/Family
// Computer(99). Every other regional-name pair is already ONE combined
// IGDB entry (Genesis 29, Master System 64, TurboGrafx-16/PC Engine 86
// +CD 150); the remaining JP-flavored ids are different devices with
// their own libraries and must NOT fold (Famicom Disk System 51,
// SuperGrafx 128, Satellaview 306, 64DD 416).
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
