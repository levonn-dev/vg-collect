package valkeykit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
)

// GetBytes reads key and returns its stored bytes, or (nil, nil) on a cache miss (redis.Nil):
// every caller treats a miss as data to recompute, not an error. op labels a returned error
// verbatim (it lands in logs and alerts), so callers own its exact wording.
func GetBytes(ctx context.Context, rdb *redis.Client, key, op string) ([]byte, error) {
	v, err := rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	// Copied out of the client's reply string so callers own the bytes.
	return []byte(v), nil
}

// PutBytes stores body at key for ttl. op labels a returned error the
// same way GetBytes does.
func PutBytes(ctx context.Context, rdb *redis.Client, key string, body []byte, ttl time.Duration, op string) error {
	if err := rdb.Set(ctx, key, body, ttl).Err(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// FailOpen logs and counts a Valkey failure the caller treats as a cache miss. op names the
// operation (the dashboard's per-op key). counter is nil-safe, so a failed counter
// registration at startup still logs the event, just without the count.
func FailOpen(ctx context.Context, logger *slog.Logger, counter metric.Int64Counter, op string, err error) {
	logger.WarnContext(ctx, "valkey unavailable; failing open", "op", op, "err", err)
	vgotel.Count(ctx, counter, attribute.String("op", op))
}
