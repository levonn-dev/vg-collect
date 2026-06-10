package valkeykit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

func TestConnect_InstrumentErrorsCloseClient(t *testing.T) {
	t.Cleanup(func() {
		instrumentTracing = redisotel.InstrumentTracing
		instrumentMetrics = redisotel.InstrumentMetrics
	})

	instrumentTracing = func(redis.UniversalClient, ...redisotel.TracingOption) error {
		return errors.New("boom")
	}
	_, err := Connect(context.Background(), "redis://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "valkeykit: tracing") {
		t.Fatalf("want wrapped tracing error, got %v", err)
	}

	instrumentTracing = redisotel.InstrumentTracing
	instrumentMetrics = func(redis.UniversalClient, ...redisotel.MetricsOption) error {
		return errors.New("boom")
	}
	_, err = Connect(context.Background(), "redis://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "valkeykit: metrics") {
		t.Fatalf("want wrapped metrics error, got %v", err)
	}
}
