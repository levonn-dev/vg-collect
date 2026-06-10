// Package valkeykit constructs OTel-instrumented go-redis clients for
// per-service Valkey caches. Construction/instrumentation/health only.
package valkeykit

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// Substitution seams for white-box tests: redisotel never errors for
// *redis.Client today, but the API contract says it can.
var (
	instrumentTracing = redisotel.InstrumentTracing
	instrumentMetrics = redisotel.InstrumentMetrics
)

// Connect builds a client from a redis:// or rediss:// URL (TLS + CA
// via rediss and standard URL params) and verifies connectivity.
func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("valkeykit: parse url: %w", err)
	}
	client := redis.NewClient(opt)
	if err := instrumentTracing(client); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("valkeykit: tracing: %w", err)
	}
	if err := instrumentMetrics(client); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("valkeykit: metrics: %w", err)
	}
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("valkeykit: ping: %w", err)
	}
	return client, nil
}

// Health pings the client to verify liveness.
func Health(ctx context.Context, client *redis.Client) error {
	return client.Ping(ctx).Err()
}
