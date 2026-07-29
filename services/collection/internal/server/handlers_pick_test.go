package server

import (
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/enrichapi"
)

func TestPickReleaseDate(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	date := func(s string) openapi_types.Date { return openapi_types.Date{Time: day(s)} }
	rd := func(region, s string) enrichapi.ReleaseDate {
		return enrichapi.ReleaseDate{Region: region, Date: date(s)}
	}
	scalar := date("1995-03-11")
	meta := func(rows ...enrichapi.ReleaseDate) *enrichapi.IgdbMeta {
		return &enrichapi.IgdbMeta{FirstReleaseDate: &scalar, ReleaseDates: &rows}
	}
	ptr := func(tt time.Time) *time.Time { return &tt }

	cases := []struct {
		name   string
		meta   *enrichapi.IgdbMeta
		region string
		want   *time.Time
	}{
		{"exact region wins", meta(rd("japan", "1995-03-11"), rd("north_america", "1995-08-22")), "ntsc_u", ptr(day("1995-08-22"))},
		{"worldwide backs ntsc_u", meta(rd("japan", "1995-03-11"), rd("worldwide", "1995-06-01")), "ntsc_u", ptr(day("1995-06-01"))},
		{"asia backs ntsc_j", meta(rd("asia", "1995-04-01"), rd("north_america", "1995-08-22")), "ntsc_j", ptr(day("1995-04-01"))},
		{"australia backs pal", meta(rd("australia", "1995-09-01"), rd("japan", "1995-03-11")), "pal", ptr(day("1995-09-01"))},
		{"korea never auto-picked", meta(rd("korea", "1996-01-01")), "ntsc_j", ptr(day("1995-03-11"))},
		{"region_free takes scalar", meta(rd("north_america", "1995-08-22")), "region_free", ptr(day("1995-03-11"))},
		{"no chain hit falls back to scalar", meta(rd("brazil", "1996-01-01")), "pal", ptr(day("1995-03-11"))},
		{"unknown payload region ignored", meta(rd("moon", "1990-01-01"), rd("europe", "1995-12-01")), "pal", ptr(day("1995-12-01"))},
		{"nil meta", nil, "pal", nil},
		{"nil rows falls back to scalar", &enrichapi.IgdbMeta{FirstReleaseDate: &scalar}, "pal", ptr(day("1995-03-11"))},
		{"nothing known", &enrichapi.IgdbMeta{}, "pal", nil},
		{"duplicate region rows keep the earliest", meta(rd("north_america", "1995-08-22"), rd("north_america", "2000-01-01")), "ntsc_u", ptr(day("1995-08-22"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickReleaseDate(tc.meta, tc.region)
			if !datesEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// TestUnitValidCoverURL_LengthBoundary pins the 512-char boundary
// directly, same direct-unit style as TestPickReleaseDate above (this
// file's tests live in package server, unlike handlers_test.go's
// black-box package server_test, so it is the sibling home for a
// direct call against an unexported helper).
func TestUnitValidCoverURL_LengthBoundary(t *testing.T) {
	const prefix = "https://img.example/"
	url512 := prefix + strings.Repeat("a", 512-len(prefix))
	if len(url512) != 512 {
		t.Fatalf("fixture length = %d, want 512", len(url512))
	}
	if !validCoverURL(url512) {
		t.Fatalf("512-char https URL must pass")
	}
	url513 := url512 + "a"
	if validCoverURL(url513) {
		t.Fatalf("513-char https URL must fail")
	}
}

// TestUnitCatalogSnapshot_CoverPrecedence is a direct 3-case pin of
// catalogSnapshot's cover choice: provider (Igdb) cover wins where
// present; absent that, the platform logo; absent both, the community
// product's own cover fills. TestApproveNew_ForwardsCoverToMint
// (handlers_test.go) discards the snapshot argument entirely, so this
// precedence was inspection-only before this test.
func TestUnitCatalogSnapshot_CoverPrecedence(t *testing.T) {
	igdbCover := "https://img.example/igdb.jpg"
	logoCover := "https://img.example/logo.jpg"
	communityCover := "https://img.example/community.jpg"

	full := func() enrichapi.Product {
		return enrichapi.Product{
			Type:      "game",
			Name:      "Chrono Trigger",
			Platform:  &enrichapi.PlatformRef{IgdbPlatformId: 6, Name: "SNES", LogoUrl: &logoCover},
			Igdb:      &enrichapi.IgdbMeta{GameId: 1010, CoverUrl: &igdbCover},
			Community: &enrichapi.CommunityMeta{CoverUrl: &communityCover},
		}
	}

	cases := []struct {
		name    string
		product func() enrichapi.Product
		want    string
	}{
		{
			name:    "provider cover wins over platform logo and community",
			product: full,
			want:    igdbCover,
		},
		{
			name: "no provider cover: platform logo wins over community",
			product: func() enrichapi.Product {
				p := full()
				p.Igdb.CoverUrl = nil
				return p
			},
			want: logoCover,
		},
		{
			name: "neither provider cover nor logo: community cover fills",
			product: func() enrichapi.Product {
				p := full()
				p.Igdb.CoverUrl = nil
				p.Platform.LogoUrl = nil
				return p
			},
			want: communityCover,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := catalogSnapshot(tc.product(), "ntsc_u")
			if snap.CoverURL == nil || *snap.CoverURL != tc.want {
				t.Fatalf("cover = %v, want %v", snap.CoverURL, tc.want)
			}
		})
	}
}
