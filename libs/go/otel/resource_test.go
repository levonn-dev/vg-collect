package otel

import (
	"context"
	"testing"

	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func TestBuildResource_InstanceIDFromHostname(t *testing.T) {
	t.Setenv("HOSTNAME", "enrichment-6b7f9d-abcde")
	res, err := buildResource(context.Background(), Config{ServiceName: "enrichment", Version: "dev"})
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got[string(semconv.ServiceNameKey)] != "enrichment" {
		t.Fatalf("service.name = %q", got[string(semconv.ServiceNameKey)])
	}
	if got[string(semconv.ServiceInstanceIDKey)] != "enrichment-6b7f9d-abcde" {
		t.Fatalf("service.instance.id = %q, want pod name", got[string(semconv.ServiceInstanceIDKey)])
	}
}

func TestBuildResource_NoHostnameOmitsInstanceID(t *testing.T) {
	t.Setenv("HOSTNAME", "")
	res, err := buildResource(context.Background(), Config{ServiceName: "user", Version: "dev"})
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	for _, kv := range res.Attributes() {
		if kv.Key == semconv.ServiceInstanceIDKey {
			t.Fatalf("service.instance.id present (%q); want omitted when HOSTNAME is empty", kv.Value.AsString())
		}
	}
}
