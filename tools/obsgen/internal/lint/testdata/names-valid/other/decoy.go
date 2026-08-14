// Package other sits outside services/ and libs/go/ on purpose: Known
// must never scan it, so its metric name must never appear in the
// result. Package name deliberately differs from the directory name
// (a real Go source file's package clause need not match its
// directory) - Known parses by path, never by package identity.
package other

import vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"

func setup(m any) {
	decoy, err := vgotel.Counter(m, "vg.decoy.should.not.appear", "must never be scanned", "{never}")
	if err != nil {
		panic(err)
	}
	_ = decoy
}
