package valkeykit_test

// Tests for the shared cache-access idioms in cache.go. Hit/miss/TTL
// behaviors round-trip through a live container, the same convention
// TestConnectAndRoundtrip already uses in this package; the
// error-wrap tests dial a client at a port that refuses connections
// (TestConnect_PingFail's trick), a real network error without
// needing docker.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
)

// newDeadClient dials a port that refuses connections immediately (no
// docker needed), for tests that need a real, fast redis.Client error.
func newDeadClient(t *testing.T) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestGetBytes_HitAndMiss(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine",
		testcontainers.WithWaitStrategyAndDeadline(180*time.Second,
			wait.ForLog("* Ready to accept connections").WithStartupTimeout(180*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })
	url, err := vk.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client, err := valkeykit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if body, err := valkeykit.GetBytes(ctx, client, "k", "cache: get k"); err != nil || body != nil {
		t.Fatalf("cold read must be a nil miss: %v %v", body, err)
	}
	if err := client.Set(ctx, "k", "hello", 0).Err(); err != nil {
		t.Fatal(err)
	}
	body, err := valkeykit.GetBytes(ctx, client, "k", "cache: get k")
	if err != nil || string(body) != "hello" {
		t.Fatalf("hit: %q %v", body, err)
	}
}

func TestGetBytes_ErrorWrapsOpLabel(t *testing.T) {
	client := newDeadClient(t)
	_, err := valkeykit.GetBytes(context.Background(), client, "k", "cache: get widget")
	if err == nil {
		t.Fatal("want error from an unreachable client, got nil")
	}
	if !strings.HasPrefix(err.Error(), "cache: get widget: ") {
		t.Fatalf("want the op label to lead the wrapped error, got %q", err.Error())
	}
}

func TestPutBytes_SetsWithTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine",
		testcontainers.WithWaitStrategyAndDeadline(180*time.Second,
			wait.ForLog("* Ready to accept connections").WithStartupTimeout(180*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })
	url, err := vk.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client, err := valkeykit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := valkeykit.PutBytes(ctx, client, "k", []byte("hello"), 200*time.Millisecond, "cache: put k"); err != nil {
		t.Fatal(err)
	}
	body, err := valkeykit.GetBytes(ctx, client, "k", "cache: get k")
	if err != nil || string(body) != "hello" {
		t.Fatalf("hit before ttl: %q %v", body, err)
	}
	time.Sleep(400 * time.Millisecond)
	body, err = valkeykit.GetBytes(ctx, client, "k", "cache: get k")
	if err != nil || body != nil {
		t.Fatalf("entry must expire with its ttl: %q %v", body, err)
	}
}

func TestPutBytes_ErrorWrapsOpLabel(t *testing.T) {
	client := newDeadClient(t)
	err := valkeykit.PutBytes(context.Background(), client, "k", []byte("x"), time.Minute, "cache: put widget")
	if err == nil {
		t.Fatal("want error from an unreachable client, got nil")
	}
	if !strings.HasPrefix(err.Error(), "cache: put widget: ") {
		t.Fatalf("want the op label to lead the wrapped error, got %q", err.Error())
	}
}

func TestFailOpen_LogsWarningAndCountsMetric(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	reader := sdkmetric.NewManualReader()
	counter, err := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).
		Meter("valkeykit_test").Int64Counter("test.fail_open")
	if err != nil {
		t.Fatal(err)
	}

	valkeykit.FailOpen(ctx, logger, counter, "search_get", errors.New("valkey down"))

	logged := buf.String()
	for _, want := range []string{
		`"level":"WARN"`,
		`"msg":"valkey unavailable; failing open"`,
		`"op":"search_get"`,
		`"err":"valkey down"`,
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log line missing %q: %s", want, logged)
		}
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "test.fail_open" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 {
				t.Fatalf("test.fail_open: want one Sum[int64] series, got %#v", m.Data)
			}
			op, _ := sum.DataPoints[0].Attributes.Value(attribute.Key("op"))
			if sum.DataPoints[0].Value != 1 || op.AsString() != "search_get" {
				t.Fatalf("want value=1 op=search_get, got value=%d op=%s", sum.DataPoints[0].Value, op.AsString())
			}
			found = true
		}
	}
	if !found {
		t.Fatal("test.fail_open not exported")
	}
}

// A counter registration failure at startup is best-effort in every
// service (logged once, then the nil counter is kept): FailOpen must
// keep logging even when it has nothing to count with.
func TestFailOpen_NilCounterDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	valkeykit.FailOpen(context.Background(), logger, nil, "search_get", errors.New("valkey down"))
	if !strings.Contains(buf.String(), `"op":"search_get"`) {
		t.Fatal("a nil counter must not block the log line")
	}
}
