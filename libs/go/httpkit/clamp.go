package httpkit

// ClampOrReject validates a caller-supplied limit or offset against a
// lower bound of lo and, when max is given, an upper bound too. A nil
// v yields def with ok=true (the param was absent). A supplied value
// outside the bound(s) yields ok=false and the zero value; the caller
// owns the detail text and writes the 400 itself, since wording
// varies by call site even within this family.
//
// Omitting max leaves the upper side unbounded here - the collection
// and social reject-with-400 family passes it; bff's hybrid family
// (reject below the floor, silently clamp above) does not, and clamps
// the result itself afterward.
func ClampOrReject(v *int, def, lo int, max ...int) (effective int, ok bool) {
	if v == nil {
		return def, true
	}
	if *v < lo {
		return 0, false
	}
	if len(max) > 0 && *v > max[0] {
		return 0, false
	}
	return *v, true
}

// ClampSilent clamps a caller-supplied limit or offset into [lo, hi]
// (or just a floor of lo when hi is omitted), returning def when v is
// nil. It never rejects - an out-of-range value is silently pulled
// back into bounds - the enrichment paging family's behavior.
func ClampSilent(v *int, def, lo int, hi ...int) int {
	if v == nil {
		return def
	}
	x := *v
	if x < lo {
		x = lo
	}
	if len(hi) > 0 && x > hi[0] {
		x = hi[0]
	}
	return x
}
