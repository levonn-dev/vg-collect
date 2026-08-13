package valkeytest

import (
	"context"
	"errors"
	"testing"
)

// TestContainer_resolve_CachesBootError drives resolve's failure leg
// directly with a stub boot: a real Docker failure isn't something a
// test can reliably trigger, and once.Do would make the real thing
// untestable a second time anyway (a failed Do never re-runs). Both
// calls must return the same error, and boot itself must run exactly
// once: the cached error has to reach the second caller without a
// second boot attempt.
func TestContainer_resolve_CachesBootError(t *testing.T) {
	c := &container{}
	bootErr := errors.New("boom")
	calls := 0
	boot := func(context.Context) (string, error) {
		calls++
		return "", bootErr
	}

	if _, err := c.resolve(boot); !errors.Is(err, bootErr) {
		t.Fatalf("first resolve error = %v, want %v", err, bootErr)
	}
	if _, err := c.resolve(boot); !errors.Is(err, bootErr) {
		t.Fatalf("second resolve error = %v, want the cached %v", err, bootErr)
	}
	if calls != 1 {
		t.Fatalf("boot called %d times, want exactly 1 (once.Do must memoize the failure too)", calls)
	}
}

// TestContainer_resolve_CachesSuccess pins the other half of the
// singleton contract: a successful boot also runs exactly once, and
// every subsequent resolve reuses its URL instead of booting again.
// This is the property the six adopted call sites depend on (one
// container per test binary, not one per test).
func TestContainer_resolve_CachesSuccess(t *testing.T) {
	c := &container{}
	calls := 0
	boot := func(context.Context) (string, error) {
		calls++
		return "redis://shared:6379", nil
	}

	for i := 0; i < 3; i++ {
		url, err := c.resolve(boot)
		if err != nil {
			t.Fatalf("resolve #%d: %v", i, err)
		}
		if url != "redis://shared:6379" {
			t.Fatalf("resolve #%d url = %q", i, url)
		}
	}
	if calls != 1 {
		t.Fatalf("boot called %d times, want exactly 1", calls)
	}
}
