// Package valkeykit constructs OTel-instrumented go-redis clients for per-service Valkey
// caches, verifies health, and carries shared cache idioms: GetBytes/PutBytes map a redis.Nil
// miss to (nil, nil), and FailOpen logs plus counts a caller degrading a request on cache error.
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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// meterName follows the repo convention: meter name = module path.
const meterName = "github.com/levonn-dev/vgkeep/libs/go/valkeykit"

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

// ConnectTLS is Connect for a server whose certificate chains to a private CA: caFile pins
// the roots. ParseURL already sets ServerName from the URL host.
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

// ConnectFromConfig picks ConnectTLS when caFile is non-empty, else Connect: the (url, caFile)
// pair every valkey-backed service's config carries as one unit.
func ConnectFromConfig(ctx context.Context, url, caFile string) (*redis.Client, error) {
	if caFile != "" {
		return ConnectTLS(ctx, url, caFile)
	}
	return Connect(ctx, url)
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
	if err := registerPoolMetrics(client); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("valkeykit: pool metrics: %w", err)
	}
	return client, nil
}

// registerPoolMetrics reports client.PoolStats() through the global OTel meter, a no-op until
// an SDK is installed. redisotel covers connection usage and timings but not hits/misses (the
// pool-fit signal), so this adds those plus timeouts and conn gauges under the vg namespace.
// Instruments are shared by name process-wide: counters sum but each gauge reflects only one
// client per collection. Callbacks stay safe after Close: PoolStats() reads atomic counters
// frozen at their final values.
func registerPoolMetrics(client *redis.Client) error {
	m := otel.Meter(meterName)
	hits, err := m.Int64ObservableCounter("vg.valkeykit.pool.hits",
		metric.WithDescription("Cumulative pool acquires served by a free connection"),
		metric.WithUnit("{hit}"))
	if err != nil {
		return err
	}
	misses, err := m.Int64ObservableCounter("vg.valkeykit.pool.misses",
		metric.WithDescription("Cumulative pool acquires that found no free connection"),
		metric.WithUnit("{miss}"))
	if err != nil {
		return err
	}
	timeouts, err := m.Int64ObservableCounter("vg.valkeykit.pool.timeouts",
		metric.WithDescription("Cumulative wait timeouts acquiring a connection from the pool"),
		metric.WithUnit("{timeout}"))
	if err != nil {
		return err
	}
	conns, err := m.Int64ObservableGauge("vg.valkeykit.pool.connections",
		metric.WithDescription("Connections currently open in the pool"),
		metric.WithUnit("{connection}"))
	if err != nil {
		return err
	}
	idle, err := m.Int64ObservableGauge("vg.valkeykit.pool.connections.idle",
		metric.WithDescription("Idle connections in the pool"),
		metric.WithUnit("{connection}"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := client.PoolStats()
		o.ObserveInt64(hits, int64(s.Hits))
		o.ObserveInt64(misses, int64(s.Misses))
		o.ObserveInt64(timeouts, int64(s.Timeouts))
		o.ObserveInt64(conns, int64(s.TotalConns))
		o.ObserveInt64(idle, int64(s.IdleConns))
		return nil
	}, hits, misses, timeouts, conns, idle)
	return err
}

// Health pings the client to verify liveness.
func Health(ctx context.Context, client *redis.Client) error {
	return client.Ping(ctx).Err()
}
