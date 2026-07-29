// Package pgkit constructs instrumented pgx pools and runs embedded
// golang-migrate migrations. Construction/instrumentation/migration/
// health only; query helpers and repositories stay in each service.
package pgkit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/exaring/otelpgx"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers pgx5://
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// meterName follows the repo convention: meter name = module path.
const meterName = "github.com/levonn-dev/vgkeep/libs/go/pgkit"

// Connect builds an OTel-instrumented pool from a postgres:// URL.
// TLS (verify-full + CA) rides in URL params: sslmode, sslrootcert.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("pgkit: parse url: %w", err)
	}
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgkit: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgkit: ping: %w", err)
	}
	if err := registerPoolMetrics(pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgkit: pool metrics: %w", err)
	}
	return pool, nil
}

// registerPoolMetrics reports pgxpool.Stat() through the global OTel
// meter, a no-op until an SDK is installed (libs/go/otel Setup with an
// OTLP endpoint), so processes without a collector observe nothing.
// Instruments are identified by name and therefore shared process-wide
// while each Connect registers its own callback: if one process holds
// several pools (integration tests; services hold exactly one), counter
// observations sum across pools and each gauge keeps a single pool's
// value per collection. That trade is accepted so Connect can keep
// returning a bare *pgxpool.Pool instead of an unregister handle no
// service needs. Callbacks outlive Close, which is safe: Stat() on a
// closed pool is a mutex-guarded read of frozen counters.
func registerPoolMetrics(pool *pgxpool.Pool) error {
	m := otel.Meter(meterName)
	conns, err := m.Int64ObservableGauge("vg.pgkit.pool.connections",
		metric.WithDescription("Connections currently in the pool: constructing, acquired, and idle"),
		metric.WithUnit("{connection}"))
	if err != nil {
		return err
	}
	idle, err := m.Int64ObservableGauge("vg.pgkit.pool.connections.idle",
		metric.WithDescription("Idle connections in the pool"),
		metric.WithUnit("{connection}"))
	if err != nil {
		return err
	}
	maxConns, err := m.Int64ObservableGauge("vg.pgkit.pool.connections.max",
		metric.WithDescription("Configured maximum size of the pool"),
		metric.WithUnit("{connection}"))
	if err != nil {
		return err
	}
	acquires, err := m.Int64ObservableCounter("vg.pgkit.pool.acquires",
		metric.WithDescription("Cumulative successful connection acquires from the pool"),
		metric.WithUnit("{acquire}"))
	if err != nil {
		return err
	}
	emptyAcquires, err := m.Int64ObservableCounter("vg.pgkit.pool.empty_acquires",
		metric.WithDescription("Cumulative acquires that waited for a connection because the pool was empty"),
		metric.WithUnit("{acquire}"))
	if err != nil {
		return err
	}
	acquireWait, err := m.Float64ObservableCounter("vg.pgkit.pool.acquire_wait",
		metric.WithDescription("Cumulative time spent in successful connection acquires"),
		metric.WithUnit("s"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := pool.Stat()
		o.ObserveInt64(conns, int64(s.TotalConns()))
		o.ObserveInt64(idle, int64(s.IdleConns()))
		o.ObserveInt64(maxConns, int64(s.MaxConns()))
		o.ObserveInt64(acquires, s.AcquireCount())
		o.ObserveInt64(emptyAcquires, s.EmptyAcquireCount())
		o.ObserveFloat64(acquireWait, s.AcquireDuration().Seconds())
		return nil
	}, conns, idle, maxConns, acquires, emptyAcquires, acquireWait)
	return err
}

// Migrate applies all up migrations from an embedded FS directory.
// A no-change run is success (idempotent at startup/init-container).
// Concurrent runs (e.g. two replicas' init containers during a
// rollout) serialize on golang-migrate's pg_advisory_lock; if a
// migrating pod dies, the session lock releases with its connection.
func Migrate(databaseURL string, fsys fs.FS, dir string) error {
	src, err := iofs.New(fsys, dir)
	if err != nil {
		return fmt.Errorf("pgkit: migration source: %w", err)
	}
	// Scheme swap: golang-migrate picks its driver by URL scheme and we
	// want the pgx/v5 driver. "postgresql://" does NOT contain
	// "postgres://" as a substring (the 'q' breaks it), so this pair of
	// single replacements handles both canonical forms safely.
	url := strings.Replace(databaseURL, "postgres://", "pgx5://", 1)
	url = strings.Replace(url, "postgresql://", "pgx5://", 1)
	m, err := migrate.NewWithSourceInstance("iofs", src, url)
	if err != nil {
		return fmt.Errorf("pgkit: migrate init: %w", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("pgkit: migrate up: %w", err)
	}
	return nil
}

// Health pings the pool to verify liveness.
func Health(ctx context.Context, pool *pgxpool.Pool) error {
	return pool.Ping(ctx)
}
