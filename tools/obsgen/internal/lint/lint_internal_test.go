package lint

import "testing"

// This file is package lint (white-box), unlike its sibling
// lint_test.go (package lint_test): substitute/capitalize are
// unexported, and Run's own Finding output never echoes a rule's
// title/summary text back on success (checkPlaceholders only reports
// when a {word}-shaped token survives substitution, which a
// placeholder always fully replaces - see substitute's doc comment in
// lint.go). The service display-name rule below therefore has no
// black-box surface to pin it against; internal/alerts and
// internal/dashboards pin their identical mirrored helper through
// their own public Emit/Assemble output instead, where the substituted
// text is directly observable.

// TestSubstitute_ServiceDisplayNameAcronym pins the owner-ruled
// exception to capitalize's plain first-letter-uppercase fallback:
// "bff" is an acronym, so its {Service} form must render "BFF", not
// "Bff" - "auth" stands in for the general case, which still gets
// plain capitalize ("Auth"). The {service} lowercase form is checked
// in the same pass to prove the acronym exception is scoped to
// {Service} only - substitute's lowercase ReplaceAll is untouched by
// this change.
func TestSubstitute_ServiceDisplayNameAcronym(t *testing.T) {
	cases := []struct {
		service string
		want    string
	}{
		{"auth", "Auth disruption budget exhausted"},
		{"bff", "BFF disruption budget exhausted"},
	}
	for _, tc := range cases {
		if got := substitute("{Service} disruption budget exhausted", tc.service); got != tc.want {
			t.Errorf("substitute(%q, %q) = %q, want %q", "{Service} disruption budget exhausted", tc.service, got, tc.want)
		}
	}

	if got := substitute("{service} service down", "bff"); got != "bff service down" {
		t.Errorf(`substitute("{service} service down", "bff") = %q, want %q (lowercase form untouched)`, got, "bff service down")
	}
}
