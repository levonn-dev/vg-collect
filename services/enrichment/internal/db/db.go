// Package db owns the enrichment service's MongoDB bootstrap:
// construction, OTel instrumentation, migration running, and health.
// Queries live in internal/store. This mirrors the datastore-kit scope
// (pgkit/valkeykit) without being a shared lib, because enrichment is
// the only document-store consumer.
package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/mongodb"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/mongo/otelmongo"
)

// ComposeURL returns the URL Connect and Migrate should use. When
// username and password are both empty, baseURL passes through
// unchanged byte-for-byte (baseURL may already carry inline
// credentials, e.g. local dev and testcontainers connection strings).
// When both are set, baseURL must not already carry userinfo -
// ComposeURL injects url.UserPassword(username, password) and errors
// otherwise, since two credential sources for one connection cannot
// be reconciled safely.
func ComposeURL(baseURL, username, password string) (string, error) {
	if username == "" && password == "" {
		return baseURL, nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("db: parse mongo url: %w", err)
	}
	if u.User != nil {
		return "", errors.New("db: mongo url already has userinfo; cannot also set MONGO_USERNAME/MONGO_PASSWORD")
	}
	u.User = url.UserPassword(username, password)
	return u.String(), nil
}

// Connect builds an OTel-instrumented client from a mongodb:// URL and
// verifies connectivity. TLS rides in URL parameters
// (tls=true&tlsCAFile=...), so the same construction covers dev and
// tests.
func Connect(ctx context.Context, url string) (*mongo.Client, error) {
	opts := options.Client().ApplyURI(url).SetMonitor(otelmongo.NewMonitor())
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return client, nil
}

// Migrate connects, runs the embedded up-migrations against dbName,
// and disconnects. Idempotent: ErrNoChange is success. Concurrent
// runners serialize on the driver's advisory-lock collection (the
// Mongo analog of the Postgres services' pg_advisory_lock).
func Migrate(ctx context.Context, url, dbName string, fsys fs.FS, dir string) error {
	client, err := Connect(ctx, url)
	if err != nil {
		return err
	}
	// The deferred Disconnect owns teardown; migrate.Close is skipped
	// deliberately because the mongodb driver's Close would disconnect
	// this same client a second time (the iofs source holds nothing).
	defer func() { _ = client.Disconnect(context.Background()) }()

	driver, err := mongodb.WithInstance(client, &mongodb.Config{
		DatabaseName: dbName,
		Locking:      mongodb.Locking{Enabled: true},
	})
	if err != nil {
		return fmt.Errorf("db: migrate driver: %w", err)
	}
	src, err := iofs.New(fsys, dir)
	if err != nil {
		return fmt.Errorf("db: migration source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, dbName, driver)
	if err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}

// Health pings the primary to verify liveness.
func Health(ctx context.Context, client *mongo.Client) error {
	return client.Ping(ctx, readpref.Primary())
}
