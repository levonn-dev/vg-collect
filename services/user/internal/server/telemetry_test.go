package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vgkeep/libs/go/metrictest"
	"github.com/levonn-dev/vgkeep/services/user/internal/store"
)

// ---- Telemetry unit layer (no Docker, runs under -short) ----
//
// Pins the domain counters and log events added for the runbook.
// server.New registers instruments against the global meter provider, so
// captureTelemetry must run BEFORE newUnitServer, to swap in the SDK's
// manual-reader provider and a capturing slog handler (restored on cleanup).

// logCapture is a slog.Handler recording every log record under a mutex
// (handlers log from the httptest goroutine); WithAttrs drops pre-bound attrs.
type logCapture struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, r.Clone())
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

// find returns the level and flattened attrs of the first record matching
// msg; string rendering is enough for the bounded field values under test.
func (c *logCapture) find(msg string) (slog.Level, map[string]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.recs {
		if r.Message != msg {
			continue
		}
		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		return r.Level, attrs, true
	}
	return 0, nil, false
}

// captureTelemetry adapts metrictest.Install, also swapping in the
// record-capturing slog handler and restoring both on cleanup.
func captureTelemetry(t *testing.T) (*sdkmetric.ManualReader, *logCapture) {
	t.Helper()
	reader := metrictest.Install(t)
	logs := &logCapture{}
	prevLog := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(prevLog) })
	return reader, logs
}

// wantCounterPoint asserts the data point carrying label=value has the
// wanted count. want 0 asserts the labeled point is absent.
func wantCounterPoint(t *testing.T, points []metricdata.DataPoint[int64], label, value string, want int64) {
	t.Helper()
	for _, dp := range points {
		if v, ok := dp.Attributes.Value(attribute.Key(label)); ok && v.AsString() == value {
			if dp.Value != want {
				t.Fatalf("%s=%s: count = %d, want %d", label, value, dp.Value, want)
			}
			return
		}
	}
	if want != 0 {
		t.Fatalf("%s=%s: no data point, want count %d", label, value, want)
	}
}

func TestUpsertTelemetry_Created(t *testing.T) {
	reader, logs := captureTelemetry(t)
	wantID := uuid.New()
	st := &stubStore{
		upsert: func(_ context.Context, email, name string, _ *string, preferredCurrency string) (store.User, bool, error) {
			return store.User{ID: wantID, Email: email, Handle: name,
				PreferredCurrency: preferredCurrency, Roles: []string{"user"}}, true, nil
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "a@example.com", "display_name": "Alice", "locale_hint": "de-DE"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	wantCounterPoint(t, metrictest.Int64Points(t, reader, "vg.user.account.upserts"), "outcome", "created", 1)
	wantCounterPoint(t, metrictest.Int64Points(t, reader, "vg.user.currency.seeds"), "source", "locale", 1)

	level, attrs, ok := logs.find("account created")
	if !ok {
		t.Fatal("no account created record")
	}
	if level != slog.LevelInfo {
		t.Fatalf("account created level = %v, want INFO", level)
	}
	if attrs["user_id"] != wantID.String() {
		t.Fatalf("user_id = %q, want %q", attrs["user_id"], wantID.String())
	}
	if attrs["preferred_currency"] != "EUR" || attrs["currency_source"] != "locale" {
		t.Fatalf("currency fields = %q/%q, want EUR/locale", attrs["preferred_currency"], attrs["currency_source"])
	}
}

func TestUpsertTelemetry_Existing(t *testing.T) {
	reader, logs := captureTelemetry(t)
	st := &stubStore{
		upsert: func(_ context.Context, email, name string, _ *string, preferredCurrency string) (store.User, bool, error) {
			return store.User{ID: uuid.New(), Email: email, Handle: name,
				PreferredCurrency: preferredCurrency, Roles: []string{"user"}}, false, nil
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "a@example.com", "display_name": "Alice", "locale_hint": "de-DE"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	upserts := metrictest.Int64Points(t, reader, "vg.user.account.upserts")
	wantCounterPoint(t, upserts, "outcome", "existing", 1)
	wantCounterPoint(t, upserts, "outcome", "created", 0)
	// The seed happens only at account creation; a returning login with
	// a mappable hint must not count.
	if pts := metrictest.Int64Points(t, reader, "vg.user.currency.seeds"); len(pts) != 0 {
		t.Fatalf("currency.seeds incremented on the existing branch: %v", pts)
	}
	if _, _, ok := logs.find("account created"); ok {
		t.Fatal("account created logged on the existing branch")
	}
}

func TestUpsertTelemetry_SeedSourceByHintClass(t *testing.T) {
	cases := []struct {
		name       string
		body       map[string]string
		wantSource string
	}{
		{"mapped hint", map[string]string{"email": "a@example.com", "display_name": "A", "locale_hint": "de-DE"}, "locale"},
		{"absent hint", map[string]string{"email": "a@example.com", "display_name": "A"}, "fallback"},
		{"unparseable hint", map[string]string{"email": "a@example.com", "display_name": "A", "locale_hint": "not a tag !!!"}, "fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader, _ := captureTelemetry(t)
			st := &stubStore{
				upsert: func(_ context.Context, email, name string, _ *string, preferredCurrency string) (store.User, bool, error) {
					return store.User{ID: uuid.New(), Email: email, Handle: name,
						PreferredCurrency: preferredCurrency, Roles: []string{"user"}}, true, nil
				},
			}
			srv, a := newUnitServer(t, st)
			resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"), tc.body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			wantCounterPoint(t, metrictest.Int64Points(t, reader, "vg.user.currency.seeds"), "source", tc.wantSource, 1)
		})
	}
}

func TestUpsertTelemetry_HandlerErrorLog(t *testing.T) {
	reader, logs := captureTelemetry(t)
	st := &stubStore{
		upsert: func(context.Context, string, string, *string, string) (store.User, bool, error) {
			return store.User{}, false, errStubUser
		},
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "POST", srv.URL+"/internal/users/upsert", a.serviceToken(t, "svc:auth"),
		map[string]string{"email": "a@example.com", "display_name": "Alice"})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}

	level, attrs, ok := logs.find("handler error")
	if !ok {
		t.Fatal("no handler error record on the 500 path")
	}
	if level != slog.LevelError {
		t.Fatalf("handler error level = %v, want ERROR", level)
	}
	if attrs["op"] != "upsert" || attrs["err"] == "" {
		t.Fatalf("handler error attrs = %v, want op=upsert with err", attrs)
	}
	// A failed upsert has no outcome; the counter must not move.
	if pts := metrictest.Int64Points(t, reader, "vg.user.account.upserts"); len(pts) != 0 {
		t.Fatalf("account.upserts incremented on the error path: %v", pts)
	}
}

func TestGetTelemetry_HandlerErrorLog(t *testing.T) {
	_, logs := captureTelemetry(t)
	st := &stubStore{
		get: func(context.Context, uuid.UUID) (store.User, error) { return store.User{}, errStubUser },
	}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/users/"+uuid.NewString(), a.serviceToken(t, "svc"), nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	_, attrs, ok := logs.find("handler error")
	if !ok || attrs["op"] != "get" || attrs["err"] == "" {
		t.Fatalf("handler error record = %v (found %v), want op=get with err", attrs, ok)
	}
}

func TestUpdateTelemetry_HandlerErrorLog(t *testing.T) {
	_, logs := captureTelemetry(t)
	st := &stubStore{
		update: func(context.Context, uuid.UUID, *string, *string, *string, *string, *string, time.Duration) (store.User, error) {
			return store.User{}, errStubUser
		},
	}
	srv, a := newUnitServer(t, st)
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid, "user"),
		map[string]string{"handle": "Neo"})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	_, attrs, ok := logs.find("handler error")
	if !ok || attrs["op"] != "update" || attrs["err"] == "" {
		t.Fatalf("handler error record = %v (found %v), want op=update with err", attrs, ok)
	}
}

func TestDeleteTelemetry_OutcomeAndLog(t *testing.T) {
	cases := []struct {
		name        string
		deleted     bool
		wantOutcome string
	}{
		{"row removed", true, "deleted"},
		{"already gone", false, "noop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader, logs := captureTelemetry(t)
			st := &stubStore{
				delete: func(context.Context, uuid.UUID) (bool, error) { return tc.deleted, nil },
			}
			srv, a := newUnitServer(t, st)
			uid := uuid.NewString()
			resp := do(t, "DELETE", srv.URL+"/users/"+uid, a.token(t, uid, "user"), nil)
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", resp.StatusCode)
			}

			points := metrictest.Int64Points(t, reader, "vg.user.account.deletes")
			wantCounterPoint(t, points, "outcome", tc.wantOutcome, 1)
			if len(points) != 1 {
				t.Fatalf("account.deletes points = %d, want 1", len(points))
			}

			level, attrs, ok := logs.find("account deleted")
			if !ok {
				t.Fatal("no account deleted record")
			}
			if level != slog.LevelInfo {
				t.Fatalf("account deleted level = %v, want INFO", level)
			}
			if attrs["user_id"] != uid || attrs["outcome"] != tc.wantOutcome {
				t.Fatalf("account deleted attrs = %v, want user_id=%s outcome=%s", attrs, uid, tc.wantOutcome)
			}
		})
	}
}

func TestDeleteTelemetry_HandlerErrorLog(t *testing.T) {
	reader, logs := captureTelemetry(t)
	st := &stubStore{
		delete: func(context.Context, uuid.UUID) (bool, error) { return false, errStubUser },
	}
	srv, a := newUnitServer(t, st)
	uid := uuid.NewString()
	resp := do(t, "DELETE", srv.URL+"/users/"+uid, a.token(t, uid, "user"), nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	_, attrs, ok := logs.find("handler error")
	if !ok || attrs["op"] != "delete" || attrs["err"] == "" {
		t.Fatalf("handler error record = %v (found %v), want op=delete with err", attrs, ok)
	}
	if pts := metrictest.Int64Points(t, reader, "vg.user.account.deletes"); len(pts) != 0 {
		t.Fatalf("account.deletes incremented on the error path: %v", pts)
	}
}

// Pins the shared 500 helper itself (the four *_HandlerErrorLog tests
// above already pin each call site's op/detail): the problem body carries
// generic detail text, distinct from the log line's op label and cause.
func TestUnitInternalErrorLogCarriesCause(t *testing.T) {
	_, logs := captureTelemetry(t)
	st := &stubStore{get: func(context.Context, uuid.UUID) (store.User, error) { return store.User{}, errStubUser }}
	srv, a := newUnitServer(t, st)
	resp := do(t, "GET", srv.URL+"/users/"+uuid.NewString(), a.serviceToken(t, "svc"), nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Code != "internal" || p.Detail != "get failed" {
		t.Fatalf("problem = %+v, want code internal, detail %q", p, "get failed")
	}

	level, attrs, ok := logs.find("handler error")
	if !ok || level != slog.LevelError || attrs["op"] != "get" || attrs["err"] == "" {
		t.Fatalf("handler error record = %v (found %v), want ERROR op=get with err", attrs, ok)
	}
}
