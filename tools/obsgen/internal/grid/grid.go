// Package grid validates dashboard panel geometry on Grafana's 24-column
// grid: per-panel bounds, exhaustive pairwise overlap, and render-time
// compaction stability (see CheckStability).
package grid

import "fmt"

type Rect struct {
	Title      string
	X, Y, W, H int
}

type Violation struct {
	Kind   string // "bounds", "overlap", or "float"
	Detail string
}

func Check(rects []Rect) []Violation {
	var out []Violation
	for _, r := range rects {
		if r.X < 0 || r.Y < 0 || r.W <= 0 || r.H <= 0 || r.X+r.W > 24 {
			out = append(out, Violation{Kind: "bounds", Detail: fmt.Sprintf("%q: x=%d y=%d w=%d h=%d", r.Title, r.X, r.Y, r.W, r.H)})
		}
	}
	for i := 0; i < len(rects); i++ {
		for j := i + 1; j < len(rects); j++ {
			a, b := rects[i], rects[j]
			if overlaps(a, b) {
				out = append(out, Violation{Kind: "overlap", Detail: fmt.Sprintf("%q (x%d y%d w%d h%d) overlaps %q (x%d y%d w%d h%d)", a.Title, a.X, a.Y, a.W, a.H, b.Title, b.X, b.Y, b.W, b.H)})
			}
		}
	}
	return out
}

// CheckStability reports panels that Grafana's render-time vertical
// compaction would move: any rect that could shift up one row without
// colliding renders above its authored position. A clean result means
// the authored layout is exactly what operators see.
func CheckStability(rects []Rect) []Violation {
	var out []Violation
	for i, r := range rects {
		if r.Y <= 0 {
			continue
		}
		trial := Rect{Title: r.Title, X: r.X, Y: r.Y - 1, W: r.W, H: r.H}
		blocked := false
		for j, other := range rects {
			if j == i {
				continue
			}
			if overlaps(trial, other) {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, Violation{Kind: "float", Detail: fmt.Sprintf("%q (x%d y%d w%d h%d) would render above its authored row: the space above it is unoccupied", r.Title, r.X, r.Y, r.W, r.H)})
		}
	}
	return out
}

// overlaps reports whether a and b's rectangles share any grid area;
// edge-adjacent rectangles (touching but not covering any shared cell)
// do not overlap. Shared by Check's own pairwise pass and
// CheckStability's one-row-up trial so the two can never define
// collision differently.
func overlaps(a, b Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}
