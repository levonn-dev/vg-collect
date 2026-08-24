// Package ctrtest provides the boot-once-and-cache-the-result
// container singleton shared by pgtest, mongotest, and valkeytest,
// plus the per-test-binary database naming those kits share. It knows
// nothing about Docker or any specific datastore: each kit declares
// its own package-level Container and supplies its own boot function,
// so a postgres boot and a Mongo boot never share a Once.
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

// Container boots at most once and remembers either its URL or its
// boot error, so every caller after the first - success or failure -
// gets the same outcome instead of retrying a boot that already ran.
// Zero value is ready to use; each kit keeps its own package-level
// instance so different container types never share a boot.
type Container struct {
	once sync.Once
	url  string
	err  error
}

// resolve runs boot the first time it is called and returns its URL
// (or its boot error, cached the same way) on every call after that.
// Plain error return, not a *testing.T dependency: that keeps the
// once.Do memoization - including the failure leg, where a real boot
// failure isn't something a test can reliably trigger and once.Do
// would refuse to retry it anyway - unit-testable with a stub.
func (c *Container) resolve(boot func(context.Context) (string, error)) (string, error) {
	c.once.Do(func() {
		c.url, c.err = boot(context.Background())
	})
	return c.url, c.err
}

// URL boots c the first time it is called for that container in a
// test binary and returns its connection string on every call after
// that, including from other test files and packages sharing the
// process. Each kit wraps this as its own one-argument URL(t), so the
// -short skip below stays the same escape hatch every fixture this
// replaces already gave callers.
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

// DBName derives a database name for the test binary whose package
// lives in dir. On a shared datastore server (the Taskfile-managed
// containers the kits adopt via their *_URL env vars), each test
// binary isolates itself in its own database instead of its own
// container: the package-dir hash separates binaries running
// concurrently under go test -p 2 (basenames collide: every service
// has an internal/store), and the TESTDS_RUN scope - minted per task
// invocation by the Taskfile - separates whole concurrent runs. The
// "t_" prefix (with the scope, "t_<run>_") is the contract with the
// Taskfile's post-run clean. 63 bytes is postgres's identifier limit
// (Mongo allows 64).
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

// sanitizeName lowercases s and maps everything outside [a-z0-9_] to
// an underscore, so any input stays a valid identifier on every
// datastore the kits front.
func sanitizeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, strings.ToLower(s))
}
