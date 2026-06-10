package otel_test

import (
	"context"
	"testing"
	"time"

	vgotel "github.com/levonn-dev/vg-collect/libs/go/otel"
)

func TestSetup_NoEndpointIsNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := vgotel.Setup(context.Background(), vgotel.Config{ServiceName: "test", Version: "dev"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestSetup_WithEndpointWiresProvidersAndShutdown(t *testing.T) {
	// Lazy gRPC dial: constructors succeed without a collector, so the
	// whole OTLP wiring branch executes; shutdown must return promptly
	// (flush against a dead endpoint may error; that's acceptable).
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	shutdown, err := vgotel.Setup(context.Background(), vgotel.Config{ServiceName: "test", Version: "dev"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = shutdown(ctx)
	if time.Since(start) > 5*time.Second {
		t.Fatal("shutdown did not return promptly")
	}
}
