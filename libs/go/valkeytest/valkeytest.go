// Package valkeytest boots one shared Valkey testcontainer per test
// binary and hands back its connection URL. Each suite still owns its
// own connect and reset: this package only replaces the hand-rolled
// container boot that used to be copied into every cache and handler
// test fixture across bff, collection, and enrichment.
package valkeytest

import (
	"context"
	"sync"
	"testing"

	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
)

// container boots at most once and remembers either its URL or its
// boot error, so every caller after the first - success or failure -
// gets the same outcome instead of retrying a boot that already ran.
type container struct {
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
func (c *container) resolve(boot func(context.Context) (string, error)) (string, error) {
	c.once.Do(func() {
		c.url, c.err = boot(context.Background())
	})
	return c.url, c.err
}

var shared container

// bootValkey starts a valkey/valkey:8-alpine container and returns its
// connection URL. No custom wait strategy: unlike postgres's
// restart-after-initdb quirk (see pgtest), every call site this
// package replaces already ran tcvalkey.Run with its default wait, and
// none of them flaked on it. No Terminate: the testcontainers reaper
// collects the container when the test process exits.
func bootValkey(ctx context.Context) (string, error) {
	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
	if err != nil {
		return "", err
	}
	return vk.ConnectionString(ctx)
}

// URL boots the shared Valkey container the first time it is called in
// a test binary and returns its connection URL on every call after
// that, including from other test files and packages sharing the
// process. Callers run their own valkeykit.Connect against the URL and
// their own reset first (typically FlushAll, since what "empty" means
// - and whether every test needs it - is a per-suite choice) - this
// package stops at handing back a live, reachable Valkey.
func URL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	url, err := shared.resolve(bootValkey)
	if err != nil {
		t.Fatal(err)
	}
	return url
}
