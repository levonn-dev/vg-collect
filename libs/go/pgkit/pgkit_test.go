package pgkit_test

import (
	"context"
	"embed"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
)

//go:embed testdata/migrations/*.sql
var testMigrations embed.FS

func TestConnectMigrateHealth(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	// 180s deadlines outlast dev-host Docker daemon freezes; the outer
	// deadline matters too - WithWaitStrategy alone caps the wait at 60s.
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("t"), tcpostgres.WithUsername("t"), tcpostgres.WithPassword("t"),
		testcontainers.WithWaitStrategyAndDeadline(180*time.Second,
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(180*time.Second),
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(180*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	url, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := pgkit.Migrate(url, testMigrations, "testdata/migrations"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := pgkit.Migrate(url, testMigrations, "testdata/migrations"); err != nil {
		t.Fatalf("Migrate (idempotent rerun): %v", err)
	}

	// Pool metrics ride the same global meter the services install;
	// drain it into a manual reader to see what Connect registered.
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	pool, err := pgkit.Connect(ctx, url)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pgkit.Health(ctx, pool); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO t (id) VALUES (1)"); err != nil {
		t.Fatalf("migrated table missing: %v", err)
	}

	// The Ping inside Connect guarantees at least one acquire.
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	if got := poolAcquires(t, rm); got < 1 {
		t.Fatalf("want at least one recorded pool acquire, got %d", got)
	}

	// A metric registration failure must fail Connect, not limp on
	// half-instrumented.
	otel.SetMeterProvider(stubErrMeterProvider{})
	if _, err := pgkit.Connect(ctx, url); err == nil || !strings.Contains(err.Error(), "pgkit: pool metrics") {
		t.Fatalf("want pool metrics error, got %v", err)
	}
}

func poolAcquires(t *testing.T, rm metricdata.ResourceMetrics) int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != "github.com/levonn-dev/vgkeep/libs/go/pgkit" {
			continue
		}
		for _, m := range sm.Metrics {
			if m.Name != "vg.pgkit.pool.acquires" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 {
				t.Fatalf("vg.pgkit.pool.acquires: unexpected shape %+v", m.Data)
			}
			return sum.DataPoints[0].Value
		}
	}
	t.Fatal("vg.pgkit.pool.acquires not exported")
	return 0
}

// stubErrMeterProvider fails the first instrument registration so the
// error leg of Connect is reachable against a live database.
type stubErrMeterProvider struct{ noop.MeterProvider }

func (stubErrMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return stubErrMeter{}
}

type stubErrMeter struct{ noop.Meter }

func (stubErrMeter) Int64ObservableGauge(string, ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	return nil, errors.New("stubbed instrument failure")
}

func TestMigrate_BadSourceDir(t *testing.T) {
	if err := pgkit.Migrate("postgres://u:p@localhost:5432/x", testMigrations, "no/such/dir"); err == nil {
		t.Fatal("want migration source error")
	}
}

func TestMigrate_BadURL(t *testing.T) {
	if err := pgkit.Migrate("not-a-url", testMigrations, "testdata/migrations"); err == nil {
		t.Fatal("want migrate init error")
	}
}

func TestConnect_BadURL(t *testing.T) {
	if _, err := pgkit.Connect(context.Background(), "://nope"); err == nil {
		t.Fatal("want parse error")
	}
}
