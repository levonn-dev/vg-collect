package mongotest

import (
	"context"
	"testing"
)

// TestServerURL_PrefersEnv pins the adoption seam: with MONGOTEST_URL
// set, serverURL must hand it back verbatim without touching Docker
// (the value is a sentinel no daemon could produce).
func TestServerURL_PrefersEnv(t *testing.T) {
	t.Setenv(envURL, "mongodb://example.invalid:1/adopted")
	got, err := serverURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "mongodb://example.invalid:1/adopted" {
		t.Fatalf("serverURL = %q, want the env value verbatim", got)
	}
}

// TestBootMongo_CanceledContext pins bootMongo's error return without
// paying for a container: a pre-canceled context must fail the boot
// instead of hanging on the daemon.
func TestBootMongo_CanceledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("docker client interaction")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bootMongo(ctx); err == nil {
		t.Fatal("bootMongo succeeded with a canceled context")
	}
}
