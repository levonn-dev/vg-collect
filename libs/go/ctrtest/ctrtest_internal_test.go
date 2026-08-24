package ctrtest

import (
	"context"
	"errors"
	"flag"
	"strings"
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
	c := &Container{}
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
// This is the property every kit's URL(t) depends on (one container
// per test binary, not one per test).
func TestContainer_resolve_CachesSuccess(t *testing.T) {
	c := &Container{}
	calls := 0
	boot := func(context.Context) (string, error) {
		calls++
		return "proto://shared:0", nil
	}

	for i := 0; i < 3; i++ {
		url, err := c.resolve(boot)
		if err != nil {
			t.Fatalf("resolve #%d: %v", i, err)
		}
		if url != "proto://shared:0" {
			t.Fatalf("resolve #%d url = %q", i, url)
		}
	}
	if calls != 1 {
		t.Fatalf("boot called %d times, want exactly 1", calls)
	}
}

// TestContainer_URL_BootsAndReturnsURL covers URL's happy path
// directly (the per-kit packages only exercise it against a real
// container): it must delegate to resolve and hand back exactly what
// boot returned.
func TestContainer_URL_BootsAndReturnsURL(t *testing.T) {
	c := &Container{}
	got := c.URL(t, func(context.Context) (string, error) {
		return "proto://shared:0", nil
	})
	if got != "proto://shared:0" {
		t.Fatalf("URL = %q, want proto://shared:0", got)
	}
}

// TestDBName_Properties pins the contract the kits and the Taskfile
// sweep both depend on: the sweep prefix, postgres's 63-byte
// identifier ceiling, a charset every datastore accepts, stability
// for the same package dir, and distinct names for distinct dirs even
// when their basenames collide (every service has an internal/store).
func TestDBName_Properties(t *testing.T) {
	a := DBName("/repo/services/auth/internal/store")
	b := DBName("/repo/services/user/internal/store")
	if a == b {
		t.Fatalf("same name %q for two dirs sharing a basename", a)
	}
	if a != DBName("/repo/services/auth/internal/store") {
		t.Fatal("DBName not stable for the same dir")
	}
	for _, name := range []string{a, b, DBName("/repo/x/Weird Dir.v2")} {
		if !strings.HasPrefix(name, "t_") {
			t.Fatalf("%q lacks the t_ sweep prefix", name)
		}
		if len(name) > 63 {
			t.Fatalf("%q exceeds postgres's 63-byte identifier limit", name)
		}
		for _, r := range name {
			valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_'
			if !valid {
				t.Fatalf("%q contains %q outside [a-z0-9_]", name, r)
			}
		}
	}
}

// TestDBName_TruncatesLongBasenames pins the ceiling: a basename long
// enough to push past 63 bytes must be cut, and the uniqueness-
// carrying hash prefix must survive the cut.
func TestDBName_TruncatesLongBasenames(t *testing.T) {
	long := DBName("/repo/" + strings.Repeat("verylongdirectoryname", 5))
	if len(long) != 63 {
		t.Fatalf("len = %d, want exactly 63 after truncation", len(long))
	}
	if !strings.HasPrefix(long, "t_") {
		t.Fatalf("%q lost its prefix in truncation", long)
	}
}

// TestDBName_RunScope pins the concurrent-run isolation contract: with
// TESTDS_RUN set, names carry the sanitized, length-capped scope right
// after the t_ prefix (the shape the Taskfile's scoped clean matches),
// two scopes yield two databases for the same package, and hostile
// scope values cannot break the identifier charset or the 63-byte cap.
func TestDBName_RunScope(t *testing.T) {
	const dir = "/repo/services/auth/internal/store"
	unscoped := DBName(dir)

	t.Setenv("TESTDS_RUN", "1a2b3c4d")
	a := DBName(dir)
	if !strings.HasPrefix(a, "t_1a2b3c4d_") {
		t.Fatalf("scoped name %q lacks the t_<run>_ shape", a)
	}
	if a == unscoped {
		t.Fatal("scoped and unscoped names collide")
	}

	t.Setenv("TESTDS_RUN", "ffee0011")
	if b := DBName(dir); b == a {
		t.Fatalf("two run scopes produced the same name %q", b)
	}

	t.Setenv("TESTDS_RUN", "We/ird-Scope.Value:That&Is(Way)TooLong")
	hostile := DBName(dir)
	if len(hostile) > 63 {
		t.Fatalf("hostile scope pushed the name to %d bytes", len(hostile))
	}
	for _, r := range hostile {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_'
		if !valid {
			t.Fatalf("%q contains %q outside [a-z0-9_]", hostile, r)
		}
	}
}

// TestContainer_URL_SkipsUnderShort flips the test.short flag at
// runtime (there is no other way to drive testing.Short() from inside
// a test) and checks URL honors it before ever calling boot - the
// same "go test -short" escape hatch every kit's own URL(t) gives
// callers.
func TestContainer_URL_SkipsUnderShort(t *testing.T) {
	orig := flag.Lookup("test.short").Value.String()
	if err := flag.Set("test.short", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := flag.Set("test.short", orig); err != nil {
			t.Fatal(err)
		}
	})

	c := &Container{}
	boot := func(context.Context) (string, error) { return "unused", nil }
	var sub *testing.T
	t.Run("short", func(st *testing.T) {
		sub = st
		c.URL(st, boot)
		st.Error("URL returned instead of skipping under -short")
	})
	if !sub.Skipped() {
		t.Fatal("want the subtest skipped by URL's -short check")
	}
}
