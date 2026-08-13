package server

import (
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
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
		return enrichapi.ReleaseDate{Region: enrichapi.ReleaseDateRegion(region), Date: date(s)}
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
		{"korea never backs ntsc_j", meta(rd("korea", "1996-01-01")), "ntsc_j", ptr(day("1995-03-11"))},
		{"korea takes its own row", meta(rd("korea", "1996-01-01"), rd("japan", "1995-03-11")), "korea", ptr(day("1996-01-01"))},
		{"asia backs korea", meta(rd("asia", "1995-04-01"), rd("japan", "1995-03-11")), "korea", ptr(day("1995-04-01"))},
		{"china takes its own row", meta(rd("china", "2004-06-01"), rd("worldwide", "1995-06-01")), "china", ptr(day("2004-06-01"))},
		{"brazil takes its own row", meta(rd("brazil", "1994-05-01"), rd("north_america", "1993-08-01")), "brazil", ptr(day("1994-05-01"))},
		{"north_america never backs brazil", meta(rd("north_america", "1993-08-01")), "brazil", ptr(day("1995-03-11"))},
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

// TestPickLocalization pins the region-picked presentation trio the
// same way TestPickReleaseDate pins the date: chain hits per region,
// sparse bundles (a region that ships only box art), the regions with
// no chain at all, and the empty-string guard - a provider's empty
// string is "no localized form", never a stored empty.
func TestPickLocalization(t *testing.T) {
	name, translit, cover := "聖剣伝説3", "Seiken Densetsu 3", "https://x/jp.jpg"
	meta := &enrichapi.IgdbMeta{Localizations: &[]enrichapi.Localization{
		{Region: "ja-JP", Name: &name, Translit: &translit, CoverUrl: &cover},
		{Region: "EU", CoverUrl: new("https://x/eu.jpg")},
		{Region: "ko-KR", Name: new("성검전설 3")},
	}}
	empty := &enrichapi.IgdbMeta{Localizations: &[]enrichapi.Localization{
		{Region: "ja-JP", Name: new(""), Translit: new(""), CoverUrl: new("")},
	}}

	zhMeta := &enrichapi.IgdbMeta{Localizations: &[]enrichapi.Localization{
		{Region: "zh-TW", Name: new("黑神話：悟空")},
		{Region: "zh-CN", Name: new("黑神话：悟空")},
	}}
	zhTWOnly := &enrichapi.IgdbMeta{Localizations: &[]enrichapi.Localization{
		{Region: "zh-TW", Name: new("黑神話：悟空")},
	}}
	ptMeta := &enrichapi.IgdbMeta{Localizations: &[]enrichapi.Localization{
		{Region: "pt-BR", Name: new("Mônica no Castelo do Dragão")},
	}}

	cases := []struct {
		name                      string
		meta                      *enrichapi.IgdbMeta
		region                    string
		wantName, wantTr, wantCov *string
	}{
		{"ntsc_j takes the ja-JP bundle whole", meta, "ntsc_j", &name, &translit, &cover},
		{"pal takes the EU bundle, sparse fields stay nil", meta, "pal", nil, nil, new("https://x/eu.jpg")},
		{"ntsc_u has no chain", meta, "ntsc_u", nil, nil, nil},
		{"region_free has no chain", meta, "region_free", nil, nil, nil},
		{"korea takes the ko-KR bundle, sparse fields stay nil", meta, "korea", new("성검전설 3"), nil, nil},
		{"china prefers zh-CN over zh-TW", zhMeta, "china", new("黑神话：悟空"), nil, nil},
		{"china falls back to zh-TW", zhTWOnly, "china", new("黑神話：悟空"), nil, nil},
		{"brazil takes the pt-BR bundle", ptMeta, "brazil", new("Mônica no Castelo do Dragão"), nil, nil},
		{"ko-KR is in no chain", meta, "ko-KR", nil, nil, nil},
		{"nil meta", nil, "ntsc_j", nil, nil, nil},
		{"nil localizations", &enrichapi.IgdbMeta{}, "ntsc_j", nil, nil, nil},
		{"empty localizations", &enrichapi.IgdbMeta{Localizations: &[]enrichapi.Localization{}}, "ntsc_j", nil, nil, nil},
		{"empty strings never store", empty, "ntsc_j", nil, nil, nil},
	}
	strEq := func(got, want *string) bool {
		if got == nil || want == nil {
			return got == want
		}
		return *got == *want
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotTr, gotCov := pickLocalization(tc.meta, tc.region)
			if !strEq(gotName, tc.wantName) || !strEq(gotTr, tc.wantTr) || !strEq(gotCov, tc.wantCov) {
				t.Fatalf("got %v/%v/%v want %v/%v/%v",
					gotName, gotTr, gotCov, tc.wantName, tc.wantTr, tc.wantCov)
			}
		})
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

// TestUnitCatalogSnapshot_Credits pins the credit derive: IGDB
// company credits split by role in wire order, community curated
// lists as per-field gap-fill, and no credits anywhere staying nil.
func TestUnitCatalogSnapshot_Credits(t *testing.T) {
	igdbProduct := enrichapi.Product{
		Type: "game", Name: "Metroid Prime",
		Igdb: &enrichapi.IgdbMeta{GameId: 99, Companies: []enrichapi.CompanyCredit{
			{Name: "Retro Studios", Developer: true},
			{Name: "Nintendo", Developer: true, Publisher: true},
		}},
	}
	snap := catalogSnapshot(igdbProduct, "ntsc_u")
	if len(snap.Developers) != 2 || snap.Developers[0] != "Retro Studios" || snap.Developers[1] != "Nintendo" {
		t.Fatalf("developers = %v, want the role-filtered wire order", snap.Developers)
	}
	if len(snap.Publishers) != 1 || snap.Publishers[0] != "Nintendo" {
		t.Fatalf("publishers = %v, want [Nintendo]", snap.Publishers)
	}

	community := enrichapi.Product{
		Type: "game", Name: "Repro Alpha",
		Community: &enrichapi.CommunityMeta{
			Developers: &[]string{"Garage Team"},
			Publishers: &[]string{"Repro House"},
		},
	}
	snap = catalogSnapshot(community, "ntsc_u")
	if len(snap.Developers) != 1 || snap.Developers[0] != "Garage Team" ||
		len(snap.Publishers) != 1 || snap.Publishers[0] != "Repro House" {
		t.Fatalf("community credits must fill: %v/%v", snap.Developers, snap.Publishers)
	}

	// Per-field precedence: the provider's publisher credit wins while
	// the community developers still fill the role the provider left
	// empty (same rule as the cover chain).
	mixed := enrichapi.Product{
		Type: "game", Name: "Repro Beta",
		Igdb:      &enrichapi.IgdbMeta{GameId: 99, Companies: []enrichapi.CompanyCredit{{Name: "Nintendo", Publisher: true}}},
		Community: &enrichapi.CommunityMeta{Developers: &[]string{"Garage Team"}, Publishers: &[]string{"Repro House"}},
	}
	snap = catalogSnapshot(mixed, "ntsc_u")
	if len(snap.Developers) != 1 || snap.Developers[0] != "Garage Team" {
		t.Fatalf("developers = %v, want the community gap-fill", snap.Developers)
	}
	if len(snap.Publishers) != 1 || snap.Publishers[0] != "Nintendo" {
		t.Fatalf("publishers = %v, want the provider credit winning", snap.Publishers)
	}

	uncredited := enrichapi.Product{Type: "console", Name: "Super NES Console"}
	snap = catalogSnapshot(uncredited, "ntsc_u")
	if snap.Developers != nil || snap.Publishers != nil {
		t.Fatalf("no credits anywhere must stay nil, got %v/%v", snap.Developers, snap.Publishers)
	}
}

// TestUnitListParams_CreditFilters pins the query-param mapping:
// developer/publisher ride into the filter matrix verbatim, the same
// open-world posture as region.
func TestUnitListParams_CreditFilters(t *testing.T) {
	dev := []string{"Nintendo", "Square"}
	pub := []string{"Capcom"}
	f, _, _, _, detail := listParams(api.ListEntriesParams{Developer: &dev, Publisher: &pub})
	if detail != "" {
		t.Fatalf("detail = %q, want none", detail)
	}
	if len(f.Developers) != 2 || f.Developers[0] != "Nintendo" || f.Developers[1] != "Square" {
		t.Fatalf("developers filter = %v", f.Developers)
	}
	if len(f.Publishers) != 1 || f.Publishers[0] != "Capcom" {
		t.Fatalf("publishers filter = %v", f.Publishers)
	}
}

// TestUnitFiltersFromViewParams_Credits pins the stored-view replay:
// credit filters survive the params JSON round trip verbatim.
func TestUnitFiltersFromViewParams_Credits(t *testing.T) {
	f, _ := filtersFromViewParams([]byte(`{"v":1,"developer":["Nintendo"],"publisher":["Capcom","Square"]}`))
	if len(f.Developers) != 1 || f.Developers[0] != "Nintendo" {
		t.Fatalf("developers = %v", f.Developers)
	}
	if len(f.Publishers) != 2 || f.Publishers[0] != "Capcom" || f.Publishers[1] != "Square" {
		t.Fatalf("publishers = %v", f.Publishers)
	}
}

// TestConsoleRegionClassification pins the console-class guard direct
// unit test (this file's package server, same rationale as the other
// direct-call tests above): the region correctness check that decides
// whether a region edit needs to hop to enrichment's resolve at all.
func TestConsoleRegionClassification(t *testing.T) {
	cases := []struct {
		console, region string
		correct         bool
	}{
		{"Super Nintendo", "ntsc_u", true},
		{"Super Nintendo", "region_free", true},
		{"Super Nintendo", "korea", true}, // base is korea's pricing proxy (no PC axis)
		{"Super Nintendo", "china", true}, // same proxy rule
		{"Super Famicom", "korea", false}, // a JP listing still misprices a korea copy
		{"PAL Playstation 4", "brazil", false},
		{"Super Nintendo", "ntsc_j", false},
		{"Super Famicom", "ntsc_j", true},
		{"Super Famicom", "ntsc_u", false},
		{"Famicom Disk System", "ntsc_j", true},
		{"JP Sega Saturn", "ntsc_j", true},
		{"PAL Playstation 4", "pal", true},
		{"PAL Playstation 4", "ntsc_u", false},
		{"Someday Console", "ntsc_j", false}, // unknown JP name classifies base: stale-safe, triggers a no-op re-resolve
	}
	for _, tc := range cases {
		prod := &enrichapi.Product{Pricecharting: &enrichapi.PricechartingMeta{ConsoleName: tc.console}}
		if got := regionCorrectMember(prod, tc.region); got != tc.correct {
			t.Errorf("regionCorrectMember(%q, %q) = %v, want %v", tc.console, tc.region, got, tc.correct)
		}
	}
	if regionCorrectMember(&enrichapi.Product{}, "ntsc_u") {
		t.Error("an unmatched member is never region-correct: it must stay re-resolve eligible")
	}
}
