package store

import "testing"

func TestNormalizeSlug_FoldEquivalence(t *testing.T) {
	for _, c := range []string{"Full_Collection", "full_collection", "FullCollection", "F_u_l_l_Collection"} {
		if got := NormalizeSlug(c); got != "fullcollection" {
			t.Errorf("NormalizeSlug(%q) = %q, want fullcollection", c, got)
		}
	}
}

func TestDeriveSlug(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Full collection", "Full_collection"},
		{"Backlog", "Backlog"},
		{"SNES * Favorites", "SNES_Favorites"},
		{"!!!", "shelf"},
		{"This Shelf Name Is Way Too Long For A Slug", "This_Shelf_Name_Is_Way_Too_Lon"},
	}
	for _, c := range cases {
		if got := DeriveSlug(c.name); got != c.want {
			t.Errorf("DeriveSlug(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
