package mongokit_test

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/bson"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vgkeep/libs/go/ctrtest"
	"github.com/levonn-dev/vgkeep/libs/go/mongokit"
)

// TestMain: under the shared server every boot self-terminates; the reaper's
// hardcoded 60s startup wait would be the only unprotected window.
func TestMain(m *testing.M) {
	if os.Getenv("MONGOTEST_URL") != "" {
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
	os.Exit(m.Run())
}

// mongoWait raises every container-start deadline from the 60s
// defaults to 180s, outlasting dev-host Docker daemon freezes.
func mongoWait() testcontainers.CustomizeRequestOption {
	return testcontainers.WithWaitStrategyAndDeadline(180*time.Second,
		wait.ForLog("Waiting for connections").WithStartupTimeout(180*time.Second),
		wait.ForListeningPort("27017/tcp").WithStartupTimeout(180*time.Second))
}

//go:embed testdata/migrations/*.json
var testMigrations embed.FS

//go:embed testdata/badmigrations/*.json
var badMigrations embed.FS

func TestUnitComposeURL_EmptyPairPassesThroughUnchanged(t *testing.T) {
	const base = "mongodb://u:p@localhost:27017/mongokit" //nolint:gosec // G101: synthetic test fixture, not a real credential
	got, err := mongokit.ComposeURL(base, "", "")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got != base {
		t.Fatalf("got %q, want byte-identical %q", got, base)
	}
}

func TestUnitComposeURL_InjectsAndEscapesReservedChars(t *testing.T) {
	const (
		base     = "mongodb://mongokit-test:27017/mongokit?tls=true"
		username = "mongokit"
		password = `p@ss:w/rd?#` //nolint:gosec // G101: synthetic test fixture, not a real credential
	)
	composed, err := mongokit.ComposeURL(base, username, password)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	parsed, err := url.Parse(composed)
	if err != nil {
		t.Fatalf("parse composed url: %v", err)
	}
	if parsed.User == nil {
		t.Fatal("composed url has no userinfo")
	}
	if parsed.User.Username() != username {
		t.Fatalf("username round-trip: got %q, want %q", parsed.User.Username(), username)
	}
	gotPassword, ok := parsed.User.Password()
	if !ok {
		t.Fatal("composed url has no password")
	}
	if gotPassword != password {
		t.Fatalf("password round-trip: got %q, want %q", gotPassword, password)
	}
}

func TestUnitComposeURL_ExistingUserinfoErrors(t *testing.T) {
	_, err := mongokit.ComposeURL("mongodb://already:there@localhost:27017/mongokit", "mongokit", "s3cret")
	if err == nil {
		t.Fatal("want error when the base url already carries userinfo")
	}
}

// TestUnitComposeURL_UnparsableBaseErrors pins that an unparsable base URL errors instead of proceeding on a zero-value URL.
func TestUnitComposeURL_UnparsableBaseErrors(t *testing.T) {
	_, err := mongokit.ComposeURL("mongodb://%zz", "mongokit", "s3cret")
	if err == nil {
		t.Fatal("want error for an unparsable base url")
	}
}

// newTestMongo returns a server URL and a fresh per-test database name: the shared server
// when MONGOTEST_URL is set, else a throwaway per-test container. Skipped under -short.
func newTestMongo(t *testing.T) (string, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	uri := os.Getenv("MONGOTEST_URL")
	if uri == "" {
		mc, err := tcmongo.Run(ctx, "mongo:8", mongoWait())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = mc.Terminate(ctx) })
		uri, err = mc.ConnectionString(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	db := ctrtest.DBName("libs/go/mongokit/" + t.Name())
	client, err := mongokit.Connect(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := client.Database(db).Drop(ctx); err != nil {
		t.Fatal(err)
	}
	return uri, db
}

func TestMigrate_CreatesCollectionAndIsIdempotent(t *testing.T) {
	uri, db := newTestMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := mongokit.Migrate(ctx, uri, db, testMigrations, "testdata/migrations"); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Second run must be a clean no-op (init containers re-run on every pod start).
	if err := mongokit.Migrate(ctx, uri, db, testMigrations, "testdata/migrations"); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	client, err := mongokit.Connect(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	mdb := client.Database(db)

	specs, err := mdb.ListCollectionSpecifications(ctx, bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, s := range specs {
		kinds[s.Name] = s.Type
	}
	if kinds["t"] != "collection" {
		t.Fatalf("collection t missing (got %q)", kinds["t"])
	}
}

// TestMigrate_BadSourceDir pins that a missing migration source dir errors after Connect succeeds.
func TestMigrate_BadSourceDir(t *testing.T) {
	uri, db := newTestMongo(t)
	ctx := context.Background()
	if err := mongokit.Migrate(ctx, uri, db, testMigrations, "no/such/dir"); err == nil {
		t.Fatal("want migration source error")
	}
}

// TestMigrate_UpError pins that an invalid migration command fails m.Up() and surfaces through Migrate.
func TestMigrate_UpError(t *testing.T) {
	uri, db := newTestMongo(t)
	ctx := context.Background()
	if err := mongokit.Migrate(ctx, uri, db, badMigrations, "testdata/badmigrations"); err == nil {
		t.Fatal("want migrate error for an invalid migration command")
	}
}

func TestHealth(t *testing.T) {
	uri, _ := newTestMongo(t)
	ctx := context.Background()
	client, err := mongokit.Connect(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := mongokit.Health(ctx, client); err != nil {
		t.Fatalf("health: %v", err)
	}
}

// TestConnect_BadURL pins that a malformed URI fails at ApplyURI's eager parse, before any network dial.
func TestConnect_BadURL(t *testing.T) {
	_, err := mongokit.Connect(context.Background(), "not-a-mongo-url")
	if err == nil {
		t.Fatal("want connect error for a malformed uri")
	}
}

// TestConnect_PingFail pins Connect's ping error leg. Port 1 is administratively prohibited, so refusal is immediate.
func TestConnect_PingFail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := mongokit.Connect(ctx, "mongodb://127.0.0.1:1/db?connectTimeoutMS=500&serverSelectionTimeoutMS=500")
	if err == nil {
		t.Fatal("want ping error for an unreachable host")
	}
}

// TestConnect_PoolMetrics pins that Connect's Ping records at least one acquire, and that a
// metric registration failure fails Connect rather than limping on half-instrumented.
func TestConnect_PoolMetrics(t *testing.T) {
	uri, _ := newTestMongo(t)
	ctx := context.Background()

	// Pool metrics ride the global meter the services install; drain into a manual reader.
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	client, err := mongokit.Connect(ctx, uri)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	if got := poolAcquires(t, rm); got < 1 {
		t.Fatalf("want at least one recorded pool acquire, got %d", got)
	}

	// A metric registration failure must fail Connect, not limp on half-instrumented.
	otel.SetMeterProvider(stubErrMeterProvider{})
	if _, err := mongokit.Connect(ctx, uri); err == nil || !strings.Contains(err.Error(), "mongokit: pool metrics") {
		t.Fatalf("want pool metrics error, got %v", err)
	}
}

func poolAcquires(t *testing.T, rm metricdata.ResourceMetrics) int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != "github.com/levonn-dev/vgkeep/libs/go/mongokit" {
			continue
		}
		for _, m := range sm.Metrics {
			if m.Name != "vg.mongokit.pool.acquires" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 {
				t.Fatalf("vg.mongokit.pool.acquires: unexpected shape %+v", m.Data)
			}
			return sum.DataPoints[0].Value
		}
	}
	t.Fatal("vg.mongokit.pool.acquires not exported")
	return 0
}

// stubErrMeterProvider fails the first instrument registration to reach Connect's error leg.
type stubErrMeterProvider struct{ noop.MeterProvider }

func (stubErrMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return stubErrMeter{}
}

type stubErrMeter struct{ noop.Meter }

func (stubErrMeter) Int64ObservableGauge(string, ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	return nil, errors.New("stubbed instrument failure")
}

// TestConnect_ReservedCharPasswordViaComposedURL starts Mongo with a reserved-char root
// password (set unescaped via MONGO_INITDB_ROOT_PASSWORD) and connects through ComposeURL,
// proving the escaping round-trips against a real server, not just net/url's parser.
func TestConnect_ReservedCharPasswordViaComposedURL(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	const (
		username = "mongokit"
		password = `p@ss:w/rd?#` //nolint:gosec // G101: synthetic test fixture, not a real credential
	)
	ctx := context.Background()
	mc, err := tcmongo.Run(ctx, "mongo:8", tcmongo.WithUsername(username), tcmongo.WithPassword(password), mongoWait())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Terminate(ctx) })

	host, err := mc.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := mc.MappedPort(ctx, "27017/tcp")
	if err != nil {
		t.Fatal(err)
	}
	credsLessURL := fmt.Sprintf("mongodb://%s:%s/?authSource=admin", host, port.Port())

	dsn, err := mongokit.ComposeURL(credsLessURL, username, password)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	client, err := mongokit.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect with composed dsn: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := mongokit.Health(ctx, client); err != nil {
		t.Fatalf("health: %v", err)
	}
}
