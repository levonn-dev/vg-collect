package grid

import (
	"strings"
	"testing"
)

func TestCheckBounds(t *testing.T) {
	v := Check([]Rect{{Title: "wide", X: 20, Y: 0, W: 8, H: 4}})
	if len(v) != 1 || v[0].Kind != "bounds" {
		t.Fatalf("want one bounds violation, got %v", v)
	}
}

func TestCheckOverlap(t *testing.T) {
	v := Check([]Rect{{Title: "a", X: 0, Y: 0, W: 12, H: 8}, {Title: "b", X: 6, Y: 4, W: 12, H: 8}})
	if len(v) != 1 || v[0].Kind != "overlap" {
		t.Fatalf("want one overlap violation, got %v", v)
	}
}

func TestCheckCleanAndTouching(t *testing.T) {
	// Edge-adjacent rectangles do not overlap.
	v := Check([]Rect{{Title: "a", X: 0, Y: 0, W: 12, H: 8}, {Title: "b", X: 12, Y: 0, W: 12, H: 8}, {Title: "c", X: 0, Y: 8, W: 24, H: 8}})
	if len(v) != 0 {
		t.Fatalf("want clean, got %v", v)
	}
}

// TestCheckStability_FloaterBesideHole mirrors a live bff defect: an
// 8-column hole between two panels lets the panel below shift up one row without colliding.
func TestCheckStability_FloaterBesideHole(t *testing.T) {
	v := CheckStability([]Rect{
		{Title: "Left", X: 0, Y: 0, W: 8, H: 8},
		{Title: "Right", X: 16, Y: 0, W: 8, H: 8},
		{Title: "Floater", X: 8, Y: 8, W: 8, H: 8},
	})
	if len(v) != 1 || v[0].Kind != "float" {
		t.Fatalf("want one float violation, got %v", v)
	}
	if !strings.Contains(v[0].Detail, `"Floater" (x8 y8 w8 h8)`) {
		t.Errorf("violation detail = %q, want it to name the floating panel and its authored position", v[0].Detail)
	}
}

// TestCheckStability_FullyPackedClean proves a fully packed grid (no
// holes) reports no floaters: every one-row-up trial collides.
func TestCheckStability_FullyPackedClean(t *testing.T) {
	v := CheckStability([]Rect{
		{Title: "a", X: 0, Y: 0, W: 12, H: 8},
		{Title: "b", X: 12, Y: 0, W: 12, H: 8},
		{Title: "c", X: 0, Y: 8, W: 12, H: 8},
		{Title: "d", X: 12, Y: 8, W: 12, H: 8},
	})
	if len(v) != 0 {
		t.Fatalf("want clean, got %v", v)
	}
}

// TestCheckStability_RowAbovePackedRegionClean proves a full-width row
// blocks both halves of a packed region below it from floating.
func TestCheckStability_RowAbovePackedRegionClean(t *testing.T) {
	v := CheckStability([]Rect{
		{Title: "Section", X: 0, Y: 0, W: 24, H: 1},
		{Title: "Left", X: 0, Y: 1, W: 12, H: 8},
		{Title: "Right", X: 12, Y: 1, W: 12, H: 8},
	})
	if len(v) != 0 {
		t.Fatalf("want clean (the row blocks both halves of the packed region below it), got %v", v)
	}
}

// TestCheckStability_PanelDirectlyUnderRowClean proves a narrower panel
// is still blocked by the full-width row above it, not by matching width.
func TestCheckStability_PanelDirectlyUnderRowClean(t *testing.T) {
	v := CheckStability([]Rect{
		{Title: "Section", X: 0, Y: 0, W: 24, H: 1},
		{Title: "Panel", X: 0, Y: 1, W: 12, H: 8},
	})
	if len(v) != 0 {
		t.Fatalf("want clean (the row itself is the collision), got %v", v)
	}
}
