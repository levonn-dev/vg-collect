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
	Platforms         []Named           `json:"platforms,omitempty" bson:"platforms,omitempty"`
	TotalRating       float64           `json:"total_rating,omitempty" bson:"total_rating,omitempty"`
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

// LogoURL builds the t_logo_med image URL, or "" without a logo.
func (p Platform) LogoURL() string {
	if p.PlatformLogo == nil || p.PlatformLogo.ImageID == "" {
		return ""
	}
	return imageBase + "t_logo_med/" + p.PlatformLogo.ImageID + ".jpg"
}
