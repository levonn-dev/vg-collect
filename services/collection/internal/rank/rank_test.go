package rank

import (
	"math/rand"
	"sort"
	"testing"
)

func between(t *testing.T, prev, next string) string {
	t.Helper()
	k, err := Between(prev, next)
	if err != nil {
		t.Fatalf("Between(%q, %q): %v", prev, next, err)
	}
	if prev != "" && k <= prev {
		t.Fatalf("Between(%q, %q) = %q, not above prev", prev, next, k)
	}
	if next != "" && k >= next {
		t.Fatalf("Between(%q, %q) = %q, not below next", prev, next, k)
	}
	if err := validate(k); err != nil {
		t.Fatalf("Between(%q, %q) = %q is not a valid key: %v", prev, next, k, err)
	}
	return k
}

func TestBetweenKnownCases(t *testing.T) {
	if k := between(t, "", ""); k != "n" {
		t.Fatalf("initial key: got %q, want n", k)
	}
	between(t, "n", "")     // append at end
	between(t, "", "n")     // insert at start
	between(t, "ab", "ac")  // adjacent short keys
	between(t, "az", "b")   // digit boundary
	between(t, "n", "nb")   // prefix pair with minimal gap
	between(t, "b", "baab") // shared-prefix walk across minimum digits
	between(t, "ay", "bb")  // consecutive first digits, longer next
	between(t, "zz", "")    // extend past the maximum digit
}

func TestBetweenRejectsBadInput(t *testing.T) {
	cases := []struct{ prev, next string }{
		{"n", "n"}, // equal
		{"x", "b"}, // reversed
		{"A", ""},  // outside the alphabet
		{"", "n1"}, // outside the alphabet
		{"na", ""}, // trailing minimum digit
	}
	for _, c := range cases {
		if _, err := Between(c.prev, c.next); err == nil {
			t.Fatalf("Between(%q, %q) must error", c.prev, c.next)
		}
	}
}

func TestBetweenPropertyRandomInsertions(t *testing.T) {
	// Deterministic seed: this is a property proof, not a fuzzer.
	rng := rand.New(rand.NewSource(42)) //nolint:gosec
	keys := []string{}
	for range 3000 {
		pos := 0
		if len(keys) > 0 {
			pos = rng.Intn(len(keys) + 1)
		}
		prev, next := "", ""
		if pos > 0 {
			prev = keys[pos-1]
		}
		if pos < len(keys) {
			next = keys[pos]
		}
		k := between(t, prev, next)
		keys = append(keys[:pos], append([]string{k}, keys[pos:]...)...)
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatal("insertion sequence lost its total order")
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] == keys[i-1] {
			t.Fatalf("duplicate key %q at %d", keys[i], i)
		}
	}
}
