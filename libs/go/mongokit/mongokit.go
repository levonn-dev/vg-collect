// Package mongokit constructs OTel-instrumented MongoDB clients, runs each service's embedded
// golang-migrate migrations, and verifies health. Construction, instrumentation, migration
// running, and health live here; queries, collections, and indexes stay in each consumer's store layer.
package mongokit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/mongodb"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/mongo/otelmongo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// meterName follows the repo convention: meter name = module path.
const meterName = "github.com/levonn-dev/vgkeep/libs/go/mongokit"

// ComposeURL returns the URL Connect and Migrate should use. With username and password both
// empty, baseURL passes through unchanged. With both set, baseURL must not already carry
// userinfo (ComposeURL injects url.UserPassword) or it errors.
func ComposeURL(baseURL, username, password string) (string, error) {
	if username == "" && password == "" {
		return baseURL, nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("mongokit: parse mongo url: %w", err)
	}
	if u.User != nil {
		return "", errors.New("mongokit: mongo url already has userinfo; cannot also set MONGO_USERNAME/MONGO_PASSWORD")
	}
	u.User = url.UserPassword(username, password)
	return u.String(), nil
}

// Connect builds an OTel-instrumented client from a mongodb:// URL and verifies connectivity.
// TLS rides in URL parameters (tls=true&tlsCAFile=...).
func Connect(ctx context.Context, url string) (*mongo.Client, error) {
	pm := &poolMetrics{}
	opts := options.Client().ApplyURI(url).
		SetMonitor(otelmongo.NewMonitor()).
		SetPoolMonitor(&event.PoolMonitor{Event: pm.onEvent})
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("mongokit: connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongokit: ping: %w", err)
	}
	if err := registerPoolMetrics(pm); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongokit: pool metrics: %w", err)
	}
	return client, nil
}

// poolMetrics accumulates one client's connection-pool event counts. Unlike pgxpool.Stat() or
// go-redis PoolStats(), the mongo driver exposes no pool snapshot, only per-event notifications.
type poolMetrics struct {
	connections int64 // net: ConnectionCreated - ConnectionClosed
	checkedOut  int64 // net: ConnectionCheckedOut - ConnectionCheckedIn
	maxConns    int64 // set once from the PoolCreated event's MaxPoolSize
	acquires    int64 // cumulative ConnectionCheckedOut count
	cleared     int64 // cumulative PoolCleared count (backpressure/error signal)
}

// onEvent is the event.PoolMonitor callback; it only updates counters, since handlers must not block.
func (pm *poolMetrics) onEvent(e *event.PoolEvent) {
	switch e.Type {
	case event.ConnectionCreated:
		atomic.AddInt64(&pm.connections, 1)
	case event.ConnectionClosed:
		atomic.AddInt64(&pm.connections, -1)
	case event.GetSucceeded:
		atomic.AddInt64(&pm.checkedOut, 1)
		atomic.AddInt64(&pm.acquires, 1)
	case event.ConnectionReturned:
		atomic.AddInt64(&pm.checkedOut, -1)
	case event.PoolCreated:
		if e.PoolOptions != nil && e.PoolOptions.MaxPoolSize <= math.MaxInt64 {
			atomic.StoreInt64(&pm.maxConns, int64(e.PoolOptions.MaxPoolSize))
		}
	case event.PoolCleared:
		atomic.AddInt64(&pm.cleared, 1)
	}
}

// registerPoolMetrics reports pm's counters through the global OTel meter, a no-op until an SDK
// is installed. Instruments are shared by name process-wide: with several clients in one process,
// counters sum across clients but each gauge reflects only one client per collection.
// Callbacks stay safe after Disconnect: pm's counters are plain atomics.
func registerPoolMetrics(pm *poolMetrics) error {
	m := otel.Meter(meterName)
	conns, err := m.Int64ObservableGauge("vg.mongokit.pool.connections",
		metric.WithDescription("Connections currently open in the pool"),
		metric.WithUnit("{connection}"))
	if err != nil {
		return err
	}
	idle, err := m.Int64ObservableGauge("vg.mongokit.pool.connections.idle",
		metric.WithDescription("Idle connections currently available in the pool"),
		metric.WithUnit("{connection}"))
	if err != nil {
		return err
	}
	maxConns, err := m.Int64ObservableGauge("vg.mongokit.pool.connections.max",
		metric.WithDescription("Configured maximum size of the pool"),
		metric.WithUnit("{connection}"))
	if err != nil {
		return err
	}
	acquires, err := m.Int64ObservableCounter("vg.mongokit.pool.acquires",
		metric.WithDescription("Cumulative successful connection checkouts from the pool"),
		metric.WithUnit("{acquire}"))
	if err != nil {
		return err
	}
	cleared, err := m.Int64ObservableCounter("vg.mongokit.pool.cleared",
		metric.WithDescription("Cumulative pool-cleared events (a network error or topology change invalidated the pool)"),
		metric.WithUnit("{event}"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		conn := atomic.LoadInt64(&pm.connections)
		checkedOut := atomic.LoadInt64(&pm.checkedOut)
		idleConn := conn - checkedOut
		if idleConn < 0 {
			idleConn = 0
		}
		o.ObserveInt64(conns, conn)
		o.ObserveInt64(idle, idleConn)
		o.ObserveInt64(maxConns, atomic.LoadInt64(&pm.maxConns))
		o.ObserveInt64(acquires, atomic.LoadInt64(&pm.acquires))
		o.ObserveInt64(cleared, atomic.LoadInt64(&pm.cleared))
		return nil
	}, conns, idle, maxConns, acquires, cleared)
	return err
}

// Migrate connects, runs the embedded up-migrations against dbName, and disconnects.
// Idempotent: ErrNoChange is success. Concurrent runners serialize on the driver's advisory-lock collection.
func Migrate(ctx context.Context, url, dbName string, fsys fs.FS, dir string) error {
	client, err := Connect(ctx, url)
	if err != nil {
		return err
	}
	// migrate.Close is skipped deliberately: the mongodb driver's Close would disconnect
	// this client a second time.
	defer func() { _ = client.Disconnect(context.Background()) }()

	//goland:noinspection GoResourceLeak
	driver, err := mongodb.WithInstance(client, &mongodb.Config{
		DatabaseName: dbName,
		Locking:      mongodb.Locking{Enabled: true},
	})
	if err != nil {
		return fmt.Errorf("mongokit: migrate driver: %w", err)
	}
	src, err := iofs.New(fsys, dir)
	if err != nil {
		return fmt.Errorf("mongokit: migration source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, dbName, driver)
	if err != nil {
		return fmt.Errorf("mongokit: migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("mongokit: migrate up: %w", err)
	}
	return nil
}

// Health pings the primary to verify liveness.
func Health(ctx context.Context, client *mongo.Client) error {
	return client.Ping(ctx, readpref.Primary())
}
