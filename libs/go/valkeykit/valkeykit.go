// Package valkeykit constructs OTel-instrumented go-redis clients for
// per-service Valkey caches. Construction/instrumentation/health only.
// Connect builds plain or rediss clients; ConnectTLS pins a private CA.
package valkeykit

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// Substitution seams for white-box tests: redisotel never errors for
// *redis.Client today, but the API contract says it can.
var (
	instrumentTracing = redisotel.InstrumentTracing
	instrumentMetrics = redisotel.InstrumentMetrics
)

// Connect builds a client from a redis:// or rediss:// URL and verifies
// connectivity. For rediss against a private CA, use ConnectTLS.
func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("valkeykit: parse url: %w", err)
	}
	return connect(ctx, opt)
}

// ConnectTLS is Connect for servers whose certificate chains to a
// private CA: the rediss URL carries host/port/db, caFile pins the
// roots. ParseURL already sets ServerName from the URL host.
func ConnectTLS(ctx context.Context, url, caFile string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("valkeykit: parse url: %w", err)
	}
	if opt.TLSConfig == nil {
		return nil, errors.New("valkeykit: ConnectTLS requires a rediss:// url")
	}
	pem, err := os.ReadFile(caFile) //nolint:gosec // caFile is caller-supplied; CA pinning is the purpose of this function.
	if err != nil {
		return nil, fmt.Errorf("valkeykit: read ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("valkeykit: no certificates in ca file")
	}
	opt.TLSConfig.RootCAs = pool
	return connect(ctx, opt)
}

func connect(ctx context.Context, opt *redis.Options) (*redis.Client, error) {
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
