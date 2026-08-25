package ctrtest

import (
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
)

// TestContainer_resolve_CachesBootError pins that a failed boot caches its error and runs exactly once.
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

// TestContainer_resolve_CachesSuccess pins that a successful boot runs exactly once and its URL is reused.
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

// TestContainer_URL_BootsAndReturnsURL pins that URL delegates to resolve and returns boot's result.
func TestContainer_URL_BootsAndReturnsURL(t *testing.T) {
	c := &Container{}
	got := c.URL(t, func(context.Context) (string, error) {
		return "proto://shared:0", nil
	})
	if got != "proto://shared:0" {
		t.Fatalf("URL = %q, want proto://shared:0", got)
	}
}

// TestDBName_Properties pins the t_ prefix, the 63-byte cap, the [a-z0-9_] charset, stability
// per dir, and distinct names for dirs with colliding basenames.
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

// TestDBName_TruncatesLongBasenames pins that a long basename is cut to 63 bytes with the hash prefix intact.
func TestDBName_TruncatesLongBasenames(t *testing.T) {
	long := DBName("/repo/" + strings.Repeat("verylongdirectoryname", 5))
	if len(long) != 63 {
		t.Fatalf("len = %d, want exactly 63 after truncation", len(long))
	}
	if !strings.HasPrefix(long, "t_") {
		t.Fatalf("%q lost its prefix in truncation", long)
	}
}

// TestDBName_RunScope pins that TESTDS_RUN inserts a sanitized, capped scope after "t_", two
// scopes yield distinct names, and hostile scope values stay within the charset and 63-byte cap.
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

// TestContainer_URL_SkipsUnderShort flips the test.short flag at runtime, the only way to
// drive testing.Short() from inside a test, and checks URL skips before calling boot.
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
