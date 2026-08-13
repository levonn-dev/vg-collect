package httpkit_test

import (
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

func intp(v int) *int { return &v }

func TestClampOrReject_NilUsesDefault(t *testing.T) {
	got, ok := httpkit.ClampOrReject(nil, 20, 1, 50)
	if !ok || got != 20 {
		t.Fatalf("got %d, %v; want 20, true", got, ok)
	}
}

func TestClampOrReject_InRangeStaysAsGiven(t *testing.T) {
	got, ok := httpkit.ClampOrReject(intp(35), 20, 1, 50)
	if !ok || got != 35 {
		t.Fatalf("got %d, %v; want 35, true", got, ok)
	}
}

func TestClampOrReject_BelowLoRejects(t *testing.T) {
	if _, ok := httpkit.ClampOrReject(intp(0), 20, 1, 50); ok {
		t.Fatal("ok = true, want false (below lo)")
	}
}

func TestClampOrReject_AboveMaxRejects(t *testing.T) {
	if _, ok := httpkit.ClampOrReject(intp(51), 20, 1, 50); ok {
		t.Fatal("ok = true, want false (above max)")
	}
}

func TestClampOrReject_BoundaryValuesAccepted(t *testing.T) {
	if got, ok := httpkit.ClampOrReject(intp(1), 20, 1, 50); !ok || got != 1 {
		t.Fatalf("lo boundary: got %d, %v", got, ok)
	}
	if got, ok := httpkit.ClampOrReject(intp(50), 20, 1, 50); !ok || got != 50 {
		t.Fatalf("hi boundary: got %d, %v", got, ok)
	}
}

// TestClampOrReject_NoMaxIsUnbounded covers bff's hybrid family: only
// a lower bound is rejected, no upper bound ever - the call site
// clamps the upper bound itself afterward.
func TestClampOrReject_NoMaxIsUnbounded(t *testing.T) {
	got, ok := httpkit.ClampOrReject(intp(999999), 20, 1)
	if !ok || got != 999999 {
		t.Fatalf("got %d, %v; want 999999, true (no upper bound supplied)", got, ok)
	}
}

func TestClampOrReject_NegativeOffsetRejectsWithNoUpperBound(t *testing.T) {
	if _, ok := httpkit.ClampOrReject(intp(-1), 0, 0); ok {
		t.Fatal("ok = true, want false (negative offset)")
	}
	if got, ok := httpkit.ClampOrReject(nil, 0, 0); !ok || got != 0 {
		t.Fatalf("nil offset: got %d, %v; want 0, true", got, ok)
	}
}

func TestClampSilent_NilUsesDefault(t *testing.T) {
	if got := httpkit.ClampSilent(nil, 200, 1, 500); got != 200 {
		t.Fatalf("got %d, want 200", got)
	}
}

func TestClampSilent_InRangeUnchanged(t *testing.T) {
	if got := httpkit.ClampSilent(intp(300), 200, 1, 500); got != 300 {
		t.Fatalf("got %d, want 300", got)
	}
}

func TestClampSilent_BelowLoFloors(t *testing.T) {
	if got := httpkit.ClampSilent(intp(-5), 200, 1, 500); got != 1 {
		t.Fatalf("got %d, want 1 (floored)", got)
	}
}

func TestClampSilent_AboveHiCeils(t *testing.T) {
	if got := httpkit.ClampSilent(intp(10000), 200, 1, 500); got != 500 {
		t.Fatalf("got %d, want 500 (ceiled)", got)
	}
}

// TestClampSilent_NoHiFloorsOnly covers enrichment's offset clamp,
// which only ever floors at 0 and never caps an upper bound.
func TestClampSilent_NoHiFloorsOnly(t *testing.T) {
	if got := httpkit.ClampSilent(intp(-3), 0, 0); got != 0 {
		t.Fatalf("negative: got %d, want 0", got)
	}
	if got := httpkit.ClampSilent(intp(0), 0, 0); got != 0 {
		t.Fatalf("zero: got %d, want 0", got)
	}
	if got := httpkit.ClampSilent(intp(123456), 0, 0); got != 123456 {
		t.Fatalf("large: got %d, want 123456 (unbounded above)", got)
	}
}

func TestClampSilent_BoundaryValuesUnchanged(t *testing.T) {
	if got := httpkit.ClampSilent(intp(1), 200, 1, 500); got != 1 {
		t.Fatalf("lo boundary: got %d, want 1", got)
	}
	if got := httpkit.ClampSilent(intp(500), 200, 1, 500); got != 500 {
		t.Fatalf("hi boundary: got %d, want 500", got)
	}
}
