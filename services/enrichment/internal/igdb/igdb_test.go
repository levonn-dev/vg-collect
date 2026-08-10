package igdb

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestGameDecode_LocalizationFields(t *testing.T) {
	payload := `{"id": 11227, "name": "Trials of Mana",
		"alternative_names": [{"name": "Seiken Densetsu 3", "comment": "Japanese title - romanization"}],
		"game_localizations": [{"name": "聖剣伝説 3", "region": {"identifier": "ja-JP"}, "cover": {"image_id": "co57gj"}}]}`
	var g Game
	if err := json.Unmarshal([]byte(payload), &g); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(g.AlternativeNames) != 1 || g.AlternativeNames[0].Comment != "Japanese title - romanization" {
		t.Fatalf("alternative_names = %+v", g.AlternativeNames)
	}
	loc := g.GameLocalizations[0]
	if loc.Name != "聖剣伝説 3" || loc.Region.Identifier != "ja-JP" {
		t.Fatalf("localization = %+v", loc)
	}
	if got := loc.CoverURL(); got != "https://images.igdb.com/igdb/image/upload/t_cover_big/co57gj.jpg" {
		t.Fatalf("cover url = %q", got)
	}
}

func TestBundleLocalizations(t *testing.T) {
	jp := func(name, cover string) GameLocalization {
		var c *Cover
		if cover != "" {
			c = &Cover{ImageID: cover}
		}
		return GameLocalization{Name: name, Region: LocalizationRegion{Identifier: "ja-JP"}, Cover: c}
	}
	cases := []struct {
		name string
		g    Game
		want []LocalizationBundle
	}{
		{"row plus romanization alt", Game{
			GameLocalizations: []GameLocalization{jp("聖剣伝説 3", "co57gj")},
			AlternativeNames: []AlternativeName{
				{Name: "Seiken Densetsu 3", Comment: "Japanese title - romanization"},
				{Name: "The Legend of the Sacred Sword 3", Comment: "Japanese title - translated"},
			}},
			[]LocalizationBundle{{Region: "ja-JP", Name: "聖剣伝説 3", Translit: "Seiken Densetsu 3",
				CoverURL: "https://images.igdb.com/igdb/image/upload/t_cover_big/co57gj.jpg"}}},
		{"alt-only native fallback", Game{
			AlternativeNames: []AlternativeName{{Name: "ロックマン", Comment: "Japanese title - original"}}},
			[]LocalizationBundle{{Region: "ja-JP", Name: "ロックマン"}}},
		{"eu cover only", Game{
			GameLocalizations: []GameLocalization{{Region: LocalizationRegion{Identifier: "EU"}, Cover: &Cover{ImageID: "cob0z2"}}}},
			[]LocalizationBundle{{Region: "EU",
				CoverURL: "https://images.igdb.com/igdb/image/upload/t_cover_big/cob0z2.jpg"}}},
		{"korea row unchanged", Game{
			GameLocalizations: []GameLocalization{{Name: "성검전설 3", Region: LocalizationRegion{Identifier: "ko-KR"}}}},
			[]LocalizationBundle{{Region: "ko-KR", Name: "성검전설 3"}}},
		{"simplified chinese alt mines zh-CN", Game{
			AlternativeNames: []AlternativeName{{Name: "黑神话：悟空", Comment: "Simplified Chinese title"}}},
			[]LocalizationBundle{{Region: "zh-CN", Name: "黑神话：悟空"}}},
		{"traditional chinese alt mines zh-TW", Game{
			AlternativeNames: []AlternativeName{{Name: "黑神話：悟空", Comment: "Traditional Chinese title"}}},
			[]LocalizationBundle{{Region: "zh-TW", Name: "黑神話：悟空"}}},
		{"portuguese alt mines pt-BR", Game{
			AlternativeNames: []AlternativeName{{Name: "Mônica no Castelo do Dragão", Comment: "Portuguese title"}}},
			[]LocalizationBundle{{Region: "pt-BR", Name: "Mônica no Castelo do Dragão"}}},
		{"translated-only alt yields nothing", Game{
			AlternativeNames: []AlternativeName{{Name: "The Legend of the Sacred Sword 3", Comment: "Japanese title - translated"}}},
			nil},
		{"untagged alt yields nothing", Game{
			AlternativeNames: []AlternativeName{{Name: "Secret of Mana 2", Comment: "Alternative title"}}},
			nil},
		{"empty localization row dropped", Game{
			GameLocalizations: []GameLocalization{{Region: LocalizationRegion{Identifier: "ja-JP"}}}},
			nil},
		{"unknown region passthrough", Game{
			GameLocalizations: []GameLocalization{{Name: "x", Region: LocalizationRegion{Identifier: "pt-BR"}}}},
			[]LocalizationBundle{{Region: "pt-BR", Name: "x"}}},
		{"regions sorted", Game{
			GameLocalizations: []GameLocalization{
				{Name: "b", Region: LocalizationRegion{Identifier: "ko-KR"}},
				{Name: "a", Region: LocalizationRegion{Identifier: "EU"}}}},
			[]LocalizationBundle{{Region: "EU", Name: "a"}, {Region: "ko-KR", Name: "b"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BundleLocalizations(tc.g); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

// A ja-JP row's Name is authoritative even when an alt in the same tag
// family also carries a native-script (non-romanized) name: the row
// wins and the alt is dropped rather than overwriting it.
func TestBundleLocalizations_RowBeatsAlt(t *testing.T) {
	g := Game{
		GameLocalizations: []GameLocalization{{Name: "ロックマン", Region: LocalizationRegion{Identifier: "ja-JP"}}},
		AlternativeNames:  []AlternativeName{{Name: "ロックマンエグゼ", Comment: "Japanese title - original"}},
	}
	want := []LocalizationBundle{{Region: "ja-JP", Name: "ロックマン"}}
	if got := BundleLocalizations(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}
