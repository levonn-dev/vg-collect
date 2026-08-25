// Package ctrtest provides the boot-once-and-cache container singleton shared by pgtest,
// mongotest, and valkeytest, plus the per-test-binary database naming those kits share.
// Each kit declares its own package-level Container and boot function, so container types never share a Once.
package ctrtest

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Container boots at most once and remembers its URL or boot error, so every caller after
// the first gets the same outcome. Zero value is ready to use.
type Container struct {
	once sync.Once
	url  string
	err  error
}

// resolve runs boot once and caches its URL or error for every later call. Returns a plain
// error, not *testing.T, so the failure leg (once.Do never retries) stays unit-testable with a stub.
func (c *Container) resolve(boot func(context.Context) (string, error)) (string, error) {
	c.once.Do(func() {
		c.url, c.err = boot(context.Background())
	})
	return c.url, c.err
}

// URL boots c once per test binary and returns its connection string on every call after,
// including from other test files sharing the process. Skips under -short.
func (c *Container) URL(t *testing.T, boot func(context.Context) (string, error)) string {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	url, err := c.resolve(boot)
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// DBName derives a database name for the test binary whose package lives in dir, isolating
// it on a shared datastore server. The dir hash separates binaries under go test -p 2;
// TESTDS_RUN scopes separate concurrent runs. The "t_<scope>_" prefix is the Taskfile clean's
// contract. 63 bytes is postgres's identifier limit (Mongo allows 64).
func DBName(dir string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(dir)) // fnv's Write never fails
	scope := ""
	if v := sanitizeName(os.Getenv("TESTDS_RUN")); v != "" {
		if len(v) > 12 {
			v = v[:12]
		}
		scope = v + "_"
	}
	name := fmt.Sprintf("t_%s%08x_%s", scope, h.Sum32(), sanitizeName(filepath.Base(dir)))
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// sanitizeName lowercases s and maps non-[a-z0-9_] runes to underscore, for a valid identifier on every datastore.
func sanitizeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, strings.ToLower(s))
}
