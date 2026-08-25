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
	"time"

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

// connectPingTimeout bounds the eager startup ping; pgx has no connect
// timeout of its own, so an unreachable Postgres would hang readiness.
var connectPingTimeout = 10 * time.Second

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
	pingCtx, cancel := context.WithTimeout(ctx, connectPingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgkit: ping: %w", err)
	}
	if err := registerPoolMetrics(pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgkit: pool metrics: %w", err)
	}
	return pool, nil
}

// registerPoolMetrics reports pgxpool.Stat() through the global OTel meter, a no-op until an SDK
// is installed. Instruments are shared by name process-wide: with several pools in one process,
// counters sum across pools but each gauge reflects only one pool per collection.
// Callbacks stay safe after Close: Stat() on a closed pool is a mutex-guarded read of frozen counters.
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

// Migrate applies all up migrations from an embedded FS directory. A no-change run is success.
// Concurrent runs serialize on golang-migrate's pg_advisory_lock, which releases if a migrating pod dies.
func Migrate(databaseURL string, fsys fs.FS, dir string) error {
	src, err := iofs.New(fsys, dir)
	if err != nil {
		return fmt.Errorf("pgkit: migration source: %w", err)
	}
	// golang-migrate picks its driver by URL scheme; swap to pgx5://. "postgresql://" does not
	// contain "postgres://" as a substring, so both forms need their own replace.
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
