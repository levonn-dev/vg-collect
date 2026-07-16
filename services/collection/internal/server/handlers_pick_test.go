package server

import (
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vg-collect/services/collection/internal/gen/enrichapi"
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
