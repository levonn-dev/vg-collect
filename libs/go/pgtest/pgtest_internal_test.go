package pgtest

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
