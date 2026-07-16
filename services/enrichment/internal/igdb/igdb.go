// Package igdb speaks the IGDB v4 API: shared response types, a real
// client (Twitch app token + APICalypse + client-side rate limit), and
// a stub client over embedded fixtures. The mode switch (IGDB_MODE)
// picks which one main.go wires in; both expose the same method set.
package igdb

import "time"

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
// is the release_region catalog id, mapped to a name via RegionName -
// the live catalog is pinned in the probe ledger).
type ReleaseDate struct {
	Date     int64 `json:"date,omitempty" bson:"date,omitempty"`
	Platform int64 `json:"platform,omitempty" bson:"platform,omitempty"`
	Region   int   `json:"release_region,omitempty" bson:"release_region,omitempty"`
}

// Game is the projection this service requests from /v4/games (and the
// shape igdb_raw persists). Expanded references always include ids.
type Game struct {
	ID                int64             `json:"id" bson:"id"`
	Name              string            `json:"name" bson:"name"`
	Cover             *Cover            `json:"cover,omitempty" bson:"cover,omitempty"`
	Genres            []Named           `json:"genres,omitempty" bson:"genres,omitempty"`
	Themes            []Named           `json:"themes,omitempty" bson:"themes,omitempty"`
	Franchises        []Named           `json:"franchises,omitempty" bson:"franchises,omitempty"`
	SimilarGames      []int64           `json:"similar_games,omitempty" bson:"similar_games,omitempty"`
	InvolvedCompanies []InvolvedCompany `json:"involved_companies,omitempty" bson:"involved_companies,omitempty"`
	FirstReleaseDate  int64             `json:"first_release_date,omitempty" bson:"first_release_date,omitempty"`
	ReleaseDates      []ReleaseDate     `json:"release_dates" bson:"release_dates"`
	Platforms         []Named           `json:"platforms,omitempty" bson:"platforms,omitempty"`
	TotalRating       float64           `json:"total_rating,omitempty" bson:"total_rating,omitempty"`
	TotalRatingCount  int               `json:"total_rating_count,omitempty" bson:"total_rating_count,omitempty"`
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

// regionNames maps the IGDB region enum onto canonical names; unknown
// values drop the row (nothing downstream can pick them).
var regionNames = map[int]string{
	1: "europe", 2: "north_america", 3: "australia", 4: "new_zealand",
	5: "japan", 6: "china", 7: "asia", 8: "worldwide", 9: "korea", 10: "brazil",
}

// RegionName resolves the region enum; ok=false means an enum value
// this service does not know (skip the row).
func RegionName(r int) (string, bool) {
	name, ok := regionNames[r]
	return name, ok
}

// twinPlatforms pairs the JP regional twins IGDB models as separate
// platforms: a Super Nintendo product (19) and its Super Famicom (58)
// counterpart are the same console to a collector, but the japan
// release row rides the Famicom platform id. Same for NES (18) and
// Family Computer (99). The pairing is symmetric so a lookup from
// either side yields the other.
//
// These two pairs are the complete set (full 220-row catalog swept
// 2026-07-16): IGDB keeps every other regional-name pair as ONE
// combined entry (Sega Mega Drive/Genesis 29, Master System/Mark III
// 64, TurboGrafx-16/PC Engine 86 and its CD 150), and the remaining
// JP-flavored entries are different devices with their own libraries
// (Famicom Disk System 51, SuperGrafx 128, Satellaview 306, 64DD 416),
// which must NOT fold.
var twinPlatforms = map[int64]int64{
	19: 58, 58: 19, // Super Nintendo <-> Super Famicom
	18: 99, 99: 18, // Nintendo Entertainment System <-> Family Computer
}

// TwinPlatformID returns the JP regional twin of a platform id, or 0
// when the platform has none. The store folds a twin's release rows
// into its sibling's projection so a SNES product still shows its
// japan (Super Famicom) date.
func TwinPlatformID(id int64) int64 {
	return twinPlatforms[id]
}

// LogoURL builds the t_logo_med image URL, or "" without a logo.
func (p Platform) LogoURL() string {
	if p.PlatformLogo == nil || p.PlatformLogo.ImageID == "" {
		return ""
	}
	return imageBase + "t_logo_med/" + p.PlatformLogo.ImageID + ".jpg"
}
