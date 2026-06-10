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
)

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
	return pool, nil
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
