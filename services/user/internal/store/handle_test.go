package store

import "testing"

// The fold is the identity rule: decoration (case, underscores) never
// distinguishes handles. This pin must match the SQL generated column
// lower(replace(handle, '_', empty)) in migration 000003 exactly.
func TestNormalizeHandle_FoldEquivalence(t *testing.T) {
	cases := []string{"alice_prime", "Alice_Prime", "AlicePrime", "ALICEPRIME", "a_l_i_c_e_p_r_i_m_e"}
	want := "aliceprime"
	for _, c := range cases {
		if got := NormalizeHandle(c); got != want {
			t.Errorf("NormalizeHandle(%q) = %q, want %q", c, got, want)
		}
	}
}

func TestValidHandle(t *testing.T) {
	valid := []string{"ab", "Alice_Prime", "a2", "A_1", "x_y_z", "a__b", "Levonnlaw", "abcdefghijklmnopqrstuvwxyz1234"}
	for _, h := range valid {
		if !ValidHandle(h) {
			t.Errorf("ValidHandle(%q) = false, want true", h)
		}
	}
	invalid := []string{"", "a", "_ab", "ab_", "a-b", "a b", "a.b", "abcdefghijklmnopqrstuvwxyz12345", "_", "__"}
	for _, h := range invalid {
		if ValidHandle(h) {
			t.Errorf("ValidHandle(%q) = true, want false", h)
		}
	}
}

func TestDeriveHandle(t *testing.T) {
	cases := []struct{ seed, email, want string }{
		{"Alice Prime", "alice@example.com", "Alice_Prime"},
		{"  Alice   Prime  ", "alice@example.com", "Alice_Prime"},
		{"McDonald's Games", "m@example.com", "McDonald_s_Games"},
		{"", "levonnlaw@example.com", "levonnlaw"},
		{"!!!", "bob.f@example.com", "bob_f"},
		{"", "a@example.com", "collector"},
		{"This Name Is Far Too Long To Fit In Thirty Chars", "x@example.com", "This_Name_Is_Far_Too_Long_To_F"},
	}
	for _, c := range cases {
		if got := DeriveHandle(c.seed, localPart(c.email)); got != c.want {
			t.Errorf("DeriveHandle(%q, %q) = %q, want %q", c.seed, c.email, got, c.want)
		}
	}
}

func TestReservedHandles_CheckedOnFold(t *testing.T) {
	if !ReservedHandles[NormalizeHandle("a_dmin")] {
		t.Fatal("a_dmin must fold to the reserved key admin")
	}
	if !ReservedHandles[NormalizeHandle("Ad_Min")] {
		t.Fatal("Ad_Min must fold to the reserved key admin")
	}
	// search is reserved because the /shared/profiles/search route is a
	// literal path segment that Go's ServeMux always prefers over the
	// /shared/profiles/{handle} wildcard; a claimable "search" handle
	// could never be resolved.
	if !ReservedHandles[NormalizeHandle("search")] {
		t.Fatal("search must be reserved")
	}
	if !ReservedHandles[NormalizeHandle("Se_Arch")] {
		t.Fatal("Se_Arch must fold to the reserved key search")
	}
}
