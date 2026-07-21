package valkeykit_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vg-collect/libs/go/valkeykit"
)

func TestConnectAndRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })

	url, err := vk.ConnectionString(ctx) // redis://host:port
	if err != nil {
		t.Fatal(err)
	}

	// Pool metrics ride the same global meter the services install;
	// drain it into a manual reader to see what Connect registered.
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	client, err := valkeykit.Connect(ctx, url)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	got, err := client.Get(ctx, "k").Result()
	if err != nil || got != "v" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if err := valkeykit.Health(ctx, client); err != nil {
		t.Fatalf("Health: %v", err)
	}

	// The first command dialed a fresh connection (a miss) and the
	// roundtrip left it pooled.
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	if v := poolMetricInt64(t, rm, "vg.valkeykit.pool.misses"); v < 1 {
		t.Fatalf("want at least one pool miss, got %d", v)
	}
	if v := poolMetricInt64(t, rm, "vg.valkeykit.pool.connections"); v < 1 {
		t.Fatalf("want at least one pooled connection, got %d", v)
	}

	// A metric registration failure must fail Connect, not limp on
	// half-instrumented.
	otel.SetMeterProvider(stubErrMeterProvider{})
	if _, err := valkeykit.Connect(ctx, url); err == nil || !strings.Contains(err.Error(), "valkeykit: pool metrics") {
		t.Fatalf("want pool metrics error, got %v", err)
	}
}

func poolMetricInt64(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != "github.com/levonn-dev/vg-collect/libs/go/valkeykit" {
			continue
		}
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				if len(data.DataPoints) != 1 {
					t.Fatalf("%s: want one series, got %d", name, len(data.DataPoints))
				}
				return data.DataPoints[0].Value
			case metricdata.Gauge[int64]:
				if len(data.DataPoints) != 1 {
					t.Fatalf("%s: want one series, got %d", name, len(data.DataPoints))
				}
				return data.DataPoints[0].Value
			default:
				t.Fatalf("%s: unexpected data type %T", name, m.Data)
			}
		}
	}
	t.Fatalf("%s not exported", name)
	return 0
}

// stubErrMeterProvider fails the first instrument registration so the
// error leg of Connect is reachable against a live server.
type stubErrMeterProvider struct{ noop.MeterProvider }

func (stubErrMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return stubErrMeter{}
}

type stubErrMeter struct{ noop.Meter }

func (stubErrMeter) Int64ObservableCounter(string, ...metric.Int64ObservableCounterOption) (metric.Int64ObservableCounter, error) {
	return nil, errors.New("stubbed instrument failure")
}

func TestConnect_InvalidURL(t *testing.T) {
	_, err := valkeykit.Connect(context.Background(), "://not-a-url")
	if err == nil {
		t.Fatal("want parse error, got nil")
	}
}

// TestConnect_PingFail tests the Ping error path: a valid redis URL pointing
// at a port with no server causes Connect to return a valkeykit: ping error.
func TestConnect_PingFail(t *testing.T) {
	// Port 1 is administratively prohibited; connection refused is immediate.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := valkeykit.Connect(ctx, "redis://127.0.0.1:1/0")
	if err == nil {
		t.Fatal("want ping error for unreachable host, got nil")
	}
	// Confirm the error is from the ping stage (not parse).
	if !strings.Contains(err.Error(), "valkeykit: ping:") {
		t.Fatalf("unexpected error stage: %v", err)
	}
}

func TestConnectTLSRejectsPlainURL(t *testing.T) {
	_, err := valkeykit.ConnectTLS(context.Background(), "redis://localhost:6379", "/does/not/matter.pem")
	if err == nil || !strings.Contains(err.Error(), "rediss") {
		t.Fatalf("want rediss requirement error, got %v", err)
	}
}

func TestConnectTLSMissingCAFile(t *testing.T) {
	_, err := valkeykit.ConnectTLS(context.Background(), "rediss://localhost:6379", "/does/not/exist.pem")
	if err == nil || !strings.Contains(err.Error(), "read ca") {
		t.Fatalf("want read error, got %v", err)
	}
}

func TestConnectTLSBadPEM(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(f, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := valkeykit.ConnectTLS(context.Background(), "rediss://localhost:6379", f)
	if err == nil || !strings.Contains(err.Error(), "no certificates") {
		t.Fatalf("want pem error, got %v", err)
	}
}

func TestConnectTLSAgainstPrivateCA(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	dir := t.TempDir()
	caPath, certPath, keyPath := writeTestCerts(t, dir)

	req := testcontainers.ContainerRequest{
		Image:        "valkey/valkey:8-alpine",
		ExposedPorts: []string{"6379/tcp"},
		Files: []testcontainers.ContainerFile{
			{HostFilePath: caPath, ContainerFilePath: "/tls/ca.crt", FileMode: 0o644},
			{HostFilePath: certPath, ContainerFilePath: "/tls/tls.crt", FileMode: 0o644},
			{HostFilePath: keyPath, ContainerFilePath: "/tls/tls.key", FileMode: 0o644},
		},
		Cmd: []string{
			"valkey-server", "--tls-port", "6379", "--port", "0",
			"--tls-cert-file", "/tls/tls.crt", "--tls-key-file", "/tls/tls.key",
			"--tls-ca-cert-file", "/tls/ca.crt", "--tls-auth-clients", "no",
		},
		WaitingFor: wait.ForListeningPort("6379/tcp"),
	}
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("rediss://%s:%s/0", host, port.Port())

	client, err := valkeykit.ConnectTLS(ctx, url, caPath)
	if err != nil {
		t.Fatalf("ConnectTLS: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Set(ctx, "k", "v", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}

	// Without the pinned CA the chain must not verify.
	if _, err := valkeykit.Connect(ctx, url); err == nil {
		t.Fatal("Connect without CA should fail certificate verification")
	}

	// An unrelated CA must also fail: this is the leg that proves the
	// pinned pool is the verification mechanism (a regression that
	// disabled verification would pass the other two assertions).
	otherCAPath, _, _ := writeTestCerts(t, t.TempDir())
	if _, err := valkeykit.ConnectTLS(ctx, url, otherCAPath); err == nil {
		t.Fatal("ConnectTLS with an unrelated CA must fail certificate verification")
	}
}

// writeTestCerts self-signs a CA and a server certificate for
// localhost/127.0.0.1 and writes the PEM files into dir.
func writeTestCerts(t *testing.T, dir string) (caPath, certPath, keyPath string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "valkeykit test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "valkey"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	caPath = filepath.Join(dir, "ca.crt")
	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")
	writePEM(t, caPath, "CERTIFICATE", caDER)
	writePEM(t, certPath, "CERTIFICATE", srvDER)
	keyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	writePEM(t, keyPath, "EC PRIVATE KEY", keyDER)
	return caPath, certPath, keyPath
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
