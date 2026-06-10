package valkeykit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"

	"github.com/levonn-dev/vg-collect/libs/go/valkeykit"
)

func TestConnectAndRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })

	url, err := vk.ConnectionString(ctx) // redis://host:port
	if err != nil {
		t.Fatal(err)
	}
	client, err := valkeykit.Connect(ctx, url)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	got, err := client.Get(ctx, "k").Result()
	if err != nil || got != "v" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if err := valkeykit.Health(ctx, client); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

func TestConnect_InvalidURL(t *testing.T) {
	_, err := valkeykit.Connect(context.Background(), "://not-a-url")
	if err == nil {
		t.Fatal("want parse error, got nil")
	}
}

// TestConnect_PingFail tests the Ping error path: a valid redis URL pointing
// at a port with no server causes Connect to return a valkeykit: ping error.
func TestConnect_PingFail(t *testing.T) {
	// Port 1 is administratively prohibited; connection refused is immediate.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := valkeykit.Connect(ctx, "redis://127.0.0.1:1/0")
	if err == nil {
		t.Fatal("want ping error for unreachable host, got nil")
	}
	// Confirm the error is from the ping stage (not parse).
	if !strings.Contains(err.Error(), "valkeykit: ping:") {
		t.Fatalf("unexpected error stage: %v", err)
	}
}
