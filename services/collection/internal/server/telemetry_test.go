package server_test

// Telemetry emission tests: the domain counters, the pending-queue
// gauge, and the structured log lines the collection runbook documents.
// Metrics are asserted through a per-test SDK meter provider swapped
// into the otel global (the pgkit test idiom).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/server"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

const collectionMeter = "github.com/levonn-dev/vgkeep/services/collection"

// installTestMeter points the global meter provider at a fresh SDK
// draining into the returned reader. Instruments queued on the global
// delegate by earlier constructions in this package would flush into
// the first real provider installed, so they are absorbed by a
// throwaway provider first; the reader then sees only instruments
// created after this call. Cleanup installs a noop provider, keeping
// later constructions inert.
func installTestMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider())
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(noop.NewMeterProvider()) })
	return reader
}

func collectDomainMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != collectionMeter {
			continue
		}
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

// counterPoints returns the series of a monotonic Int64 counter.
func counterPoints(t *testing.T, got map[string]metricdata.Metrics, name string) []metricdata.DataPoint[int64] {
	t.Helper()
	m, ok := got[name]
	if !ok {
		t.Fatalf("metric %q not exported", name)
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s: want Sum[int64], got %T", name, m.Data)
	}
	if !sum.IsMonotonic {
		t.Fatalf("%s: want monotonic sum", name)
	}
	return sum.DataPoints
}

// counterValue returns the value of the series carrying exactly attrs,
// 0 when that series was never written.
func counterValue(t *testing.T, got map[string]metricdata.Metrics, name string, attrs ...attribute.KeyValue) int64 {
	t.Helper()
	want := attribute.NewSet(attrs...)
	for _, dp := range counterPoints(t, got, name) {
		if dp.Attributes.Equals(&want) {
			return dp.Value
		}
	}
	return 0
}

// withPendingCount arms the gauge callback that every server.New
// registers, so collecting metrics never trips the stub's nil panic.
func withPendingCount(st *stubStore, n int64) *stubStore {
	st.countAllPendingSubmissions = func(context.Context) (int64, error) { return n, nil }
	return st
}

// TestUnitPricingComposeCounter pins the op per surface and the
// ok/degraded outcome split, and that a degraded read still answers
// 200 (the failure mode RED never shows).
func TestUnitPricingComposeCounter(t *testing.T) {
	user := uuid.New()
	productID := uuid.New()
	entry := store.Entry{ID: uuid.New(), UserID: user, ItemType: "game", MediaType: "physical",
		DisplayName: "Chrono Trigger", Region: "ntsc_u", Packaging: "cib", Currency: "USD",
		PricingMode: "auto", Status: "backlog", Source: "manual", ProductID: &productID}
	row := store.PricingRow{EntryID: entry.ID, Packaging: "cib", PricingMode: "auto", ProductID: &productID}

	surfaces := []struct {
		op    string
		path  string
		store func() *stubStore
	}{
		{"entry", "/entries/" + entry.ID.String(), func() *stubStore {
			return &stubStore{getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
				return entry, nil
			}}
		}},
		{"list", "/entries", func() *stubStore {
			return &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
				return []store.Entry{entry}, nil
			}}
		}},
		{"dashboard", "/dashboard", func() *stubStore {
			return dashboardStore(user, []store.PricingRow{row})
		}},
		{"value_history", "/dashboard/value-history", func() *stubStore {
			return &stubStore{pricingRows: func(context.Context, uuid.UUID, store.Filters) ([]store.PricingRow, error) {
				return []store.PricingRow{row}, nil
			}}
		}},
	}
	for _, sf := range surfaces {
		for _, outcome := range []string{"ok", "degraded"} {
			t.Run(sf.op+"_"+outcome, func(t *testing.T) {
				reader := installTestMeter(t)
				enrich := &stubEnrichment{
					batchPrices: pricedAs(1500, 4200, 9900),
					priceHistory: func(context.Context, string, []uuid.UUID, int) (map[string][]enrichapi.PricePoint, error) {
						return map[string][]enrichapi.PricePoint{}, nil
					},
				}
				if outcome == "degraded" {
					enrich.batchPrices = func(context.Context, string, []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
						return nil, enrichmentclient.ErrUnavailable
					}
					enrich.priceHistory = func(context.Context, string, []uuid.UUID, int) (map[string][]enrichapi.PricePoint, error) {
						return nil, enrichmentclient.ErrUnavailable
					}
				}
				srv, a := newUnitServer(t, withPendingCount(sf.store(), 0), enrich, newStubCache())
				resp := do(t, http.MethodGet, srv.URL+sf.path, a.token(t, user.String()), nil)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status %d: a degraded composition must never fail the read", resp.StatusCode)
				}
				got := collectDomainMetrics(t, reader)
				if pts := counterPoints(t, got, "vg.collection.pricing.compose"); len(pts) != 1 {
					t.Fatalf("want exactly one series, got %d", len(pts))
				}
				if v := counterValue(t, got, "vg.collection.pricing.compose",
					attribute.String("op", sf.op), attribute.String("outcome", outcome)); v != 1 {
					t.Fatalf("{op=%s,outcome=%s} = %d, want 1", sf.op, outcome, v)
				}
			})
		}
	}
}

// TestUnitPricingComposeCounter_SkipsWhenNothingPriced pins the ratio
// hygiene: a read that never calls enrichment must not increment.
func TestUnitPricingComposeCounter_SkipsWhenNothingPriced(t *testing.T) {
	reader := installTestMeter(t)
	disabled := store.Entry{ID: uuid.New(), ItemType: "game", MediaType: "physical",
		DisplayName: "Fan cart", Region: "pal", Packaging: "loose", Currency: "USD",
		PricingMode: "disabled", Status: "backlog", Source: "manual"}
	st := withPendingCount(&stubStore{getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
		return disabled, nil
	}}, 0)
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	if resp := do(t, http.MethodGet, srv.URL+"/entries/"+disabled.ID.String(), a.token(t, uuid.NewString()), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// A never-incremented counter exports nothing at all.
	if _, ok := collectDomainMetrics(t, reader)["vg.collection.pricing.compose"]; ok {
		t.Fatal("unpriced reads must not count a composition")
	}
}

func TestUnitCacheLookupAndFailOpenCounters(t *testing.T) {
	user := uuid.New()

	t.Run("hits", func(t *testing.T) {
		reader := installTestMeter(t)
		c := newStubCache()
		c.bodies[user.String()] = []byte(`{"total_entries":1}`)
		c.vhBodies[user.String()] = []byte(`{"available":true,"points":[]}`)
		// Zero-field store: a hit must answer without any recompute.
		srv, a := newUnitServer(t, withPendingCount(&stubStore{}, 0), &stubEnrichment{}, c)
		bearer := a.token(t, user.String())
		for _, path := range []string{"/dashboard", "/dashboard/value-history"} {
			if resp := do(t, http.MethodGet, srv.URL+path, bearer, nil); resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: %d", path, resp.StatusCode)
			}
		}
		got := collectDomainMetrics(t, reader)
		for _, cache := range []string{"dashboard", "value_history"} {
			if v := counterValue(t, got, "vg.collection.cache.lookups",
				attribute.String("cache", cache), attribute.String("outcome", "hit")); v != 1 {
				t.Fatalf("%s hit = %d, want 1", cache, v)
			}
		}
		if _, ok := got["vg.collection.cache.fail_open"]; ok {
			t.Fatal("hits must not fail open")
		}
	})

	t.Run("miss", func(t *testing.T) {
		reader := installTestMeter(t)
		srv, a := newUnitServer(t, withPendingCount(dashboardStore(user, nil), 0), &stubEnrichment{}, newStubCache())
		if resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("dashboard: %d", resp.StatusCode)
		}
		got := collectDomainMetrics(t, reader)
		if v := counterValue(t, got, "vg.collection.cache.lookups",
			attribute.String("cache", "dashboard"), attribute.String("outcome", "miss")); v != 1 {
			t.Fatalf("dashboard miss = %d, want 1", v)
		}
	})

	t.Run("valkey_error_is_miss_plus_fail_open", func(t *testing.T) {
		reader := installTestMeter(t)
		c := newStubCache()
		c.err = errors.New("valkey is having a moment")
		srv, a := newUnitServer(t, withPendingCount(dashboardStore(user, nil), 0), &stubEnrichment{}, c)
		if resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("dashboard: %d", resp.StatusCode)
		}
		got := collectDomainMetrics(t, reader)
		if v := counterValue(t, got, "vg.collection.cache.lookups",
			attribute.String("cache", "dashboard"), attribute.String("outcome", "miss")); v != 1 {
			t.Fatalf("errored lookup must count a miss, got %d", v)
		}
		// The GET failed open, the recompute succeeded, and the write
		// failed open too.
		for _, op := range []string{"dashboard_get", "dashboard_put"} {
			if v := counterValue(t, got, "vg.collection.cache.fail_open", attribute.String("op", op)); v != 1 {
				t.Fatalf("%s fail-open = %d, want 1", op, v)
			}
		}
	})

	t.Run("value_history_error_ops", func(t *testing.T) {
		reader := installTestMeter(t)
		st := &stubStore{pricingRows: func(context.Context, uuid.UUID, store.Filters) ([]store.PricingRow, error) {
			return nil, nil
		}}
		c := newStubCache()
		c.err = errors.New("valkey is having a moment")
		srv, a := newUnitServer(t, withPendingCount(st, 0), &stubEnrichment{}, c)
		if resp := do(t, http.MethodGet, srv.URL+"/dashboard/value-history", a.token(t, user.String()), nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("value history: %d", resp.StatusCode)
		}
		got := collectDomainMetrics(t, reader)
		for _, op := range []string{"value_history_get", "value_history_put"} {
			if v := counterValue(t, got, "vg.collection.cache.fail_open", attribute.String("op", op)); v != 1 {
				t.Fatalf("%s fail-open = %d, want 1", op, v)
			}
		}
	})

	t.Run("invalidate_error_fails_open", func(t *testing.T) {
		reader := installTestMeter(t)
		st := &stubStore{deleteEntry: func(context.Context, uuid.UUID, uuid.UUID) error { return nil }}
		c := newStubCache()
		c.err = errors.New("valkey is having a moment")
		srv, a := newUnitServer(t, withPendingCount(st, 0), &stubEnrichment{}, c)
		resp := do(t, http.MethodDelete, srv.URL+"/entries/"+uuid.NewString(), a.token(t, user.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
		got := collectDomainMetrics(t, reader)
		if v := counterValue(t, got, "vg.collection.cache.fail_open", attribute.String("op", "dashboard_invalidate")); v != 1 {
			t.Fatalf("dashboard_invalidate fail-open = %d, want 1", v)
		}
	})
}

func TestUnitSubmissionEventsCounter(t *testing.T) {
	userID := uuid.New()
	entryID := uuid.New()
	subID := uuid.New()
	productID := uuid.New()
	custom := store.Entry{ID: entryID, UserID: userID, ItemType: "game", DisplayName: "Repro", Region: "pal"}
	pending := store.Submission{ID: subID, EntryID: entryID, UserID: userID, Status: "pending",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}

	assertOne := func(t *testing.T, reader *sdkmetric.ManualReader, event string) {
		t.Helper()
		got := collectDomainMetrics(t, reader)
		if pts := counterPoints(t, got, "vg.collection.submissions.events"); len(pts) != 1 {
			t.Fatalf("want exactly one series, got %d", len(pts))
		}
		if v := counterValue(t, got, "vg.collection.submissions.events", attribute.String("event", event)); v != 1 {
			t.Fatalf("{event=%s} = %d, want 1", event, v)
		}
	}

	t.Run("created", func(t *testing.T) {
		reader := installTestMeter(t)
		st := withPendingCount(&stubStore{
			getEntry:                func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return custom, nil },
			countPendingSubmissions: func(context.Context, uuid.UUID) (int64, error) { return 0, nil },
			countSubmissionsSince:   func(context.Context, uuid.UUID, time.Time) (int64, error) { return 0, nil },
			createSubmission: func(_ context.Context, u, id uuid.UUID) (store.Submission, error) {
				return pending, nil
			},
		}, 0)
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		if resp := do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission", a.token(t, userID.String()), nil); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %d", resp.StatusCode)
		}
		assertOne(t, reader, "created")
	})

	t.Run("cancelled", func(t *testing.T) {
		reader := installTestMeter(t)
		st := withPendingCount(&stubStore{
			cancelSubmission: func(context.Context, uuid.UUID, uuid.UUID) error { return nil },
		}, 0)
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		if resp := do(t, http.MethodDelete, srv.URL+"/entries/"+entryID.String()+"/submission", a.token(t, userID.String()), nil); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("cancel: %d", resp.StatusCode)
		}
		assertOne(t, reader, "cancelled")
	})

	t.Run("rejected", func(t *testing.T) {
		reader := installTestMeter(t)
		st := withPendingCount(&stubStore{
			getSubmission: func(context.Context, uuid.UUID) (store.Submission, error) { return pending, nil },
			rejectSubmission: func(_ context.Context, _ uuid.UUID, reason string) (store.Submission, error) {
				out := pending
				out.Status = "rejected"
				out.RejectReason = &reason
				return out, nil
			},
		}, 0)
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict",
			a.token(t, uuid.NewString(), "admin"), jsonBody(map[string]any{"action": "reject", "reason": "not a shared item"}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reject: %d", resp.StatusCode)
		}
		assertOne(t, reader, "rejected")
	})

	t.Run("approved", func(t *testing.T) {
		reader := installTestMeter(t)
		st := withPendingCount(&stubStore{
			getSubmission: func(context.Context, uuid.UUID) (store.Submission, error) { return pending, nil },
			getEntry:      func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return custom, nil },
			approveSubmission: func(_ context.Context, _ uuid.UUID, snap store.CatalogSnapshot) (store.Submission, error) {
				out := pending
				out.Status = "approved"
				out.ProductID = &snap.ProductID
				return out, nil
			},
		}, 0)
		enrich := &stubEnrichment{getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
			return enrichapi.Product{Id: productID, Type: "game", Name: "Chrono Trigger"}, nil
		}}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict",
			a.token(t, uuid.NewString(), "admin"), jsonBody(map[string]any{"action": "approve_existing", "product_id": productID.String()}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("approve: %d", resp.StatusCode)
		}
		assertOne(t, reader, "approved")
	})
}

func TestUnitPendingSubmissionsGauge(t *testing.T) {
	t.Run("observes_all_users_count", func(t *testing.T) {
		reader := installTestMeter(t)
		st := &stubStore{countAllPendingSubmissions: func(context.Context) (int64, error) { return 7, nil }}
		server.New(st, &stubEnrichment{}, newStubCache(), server.Options{
			DashboardCacheTTL: time.Minute, Logger: testLogger(),
		})
		got := collectDomainMetrics(t, reader)
		m, ok := got["vg.collection.submissions.pending"]
		if !ok {
			t.Fatal("gauge not exported")
		}
		g, ok := m.Data.(metricdata.Gauge[int64])
		if !ok {
			t.Fatalf("want Gauge[int64], got %T", m.Data)
		}
		if len(g.DataPoints) != 1 || g.DataPoints[0].Value != 7 {
			t.Fatalf("pending = %+v, want a single point of 7", g.DataPoints)
		}
		if g.DataPoints[0].Attributes.Len() != 0 {
			t.Fatalf("gauge must carry no attributes: %v", g.DataPoints[0].Attributes)
		}
	})

	t.Run("count_error_skips_observation", func(t *testing.T) {
		reader := installTestMeter(t)
		st := &stubStore{countAllPendingSubmissions: func(context.Context) (int64, error) {
			return 0, errors.New("pg down")
		}}
		server.New(st, &stubEnrichment{}, newStubCache(), server.Options{
			DashboardCacheTTL: time.Minute, Logger: testLogger(),
		})
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err == nil {
			t.Fatal("collect must surface the count error")
		}
	})
}

// stubErrMeterProvider hands out a meter that refuses every counter
// registration this service performs; the noop embeds satisfy the
// rest of the interfaces.
type stubErrMeterProvider struct{ noop.MeterProvider }

func (stubErrMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return stubErrMeter{}
}

type stubErrMeter struct{ noop.Meter }

func (stubErrMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errors.New("registration refused")
}

// TestUnitNew_NilLoggerDoesNotPanic pins the constructor's
// tolerate-nil idiom (shared across services): a caller that leaves
// Options.Logger nil must not crash New, whose counter registration
// otherwise logs straight through it. Every registration is forced to
// fail (a nil store also skips the pending-submissions gauge), so New
// actually reaches opts.Logger.Error; without the nil-logger guard
// this call would panic on the nil receiver.
func TestUnitNew_NilLoggerDoesNotPanic(t *testing.T) {
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(stubErrMeterProvider{})
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	h := server.New(nil, nil, nil, server.Options{})
	if h == nil {
		t.Fatal("New returned nil")
	}
}

// ---- log additions ----

// syncBuffer is a mutex-guarded buffer: handler goroutines write log
// lines while the test goroutine reads them back.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// lines decodes every JSON log line written so far.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func findLine(lines []map[string]any, msg string) map[string]any {
	for _, l := range lines {
		if l["msg"] == msg {
			return l
		}
	}
	return nil
}

// newLoggedServer is newUnitServer with a capturing JSON logger.
func newLoggedServer(t *testing.T, st server.Store, enrich server.Enrichment, c server.Cache) (*httptest.Server, authEnv, *syncBuffer) {
	t.Helper()
	buf := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	a := newAuthEnv(t)
	h := server.New(st, enrich, c, server.Options{DashboardCacheTTL: 5 * time.Minute, Logger: logger})
	router := server.NewRouter(h, a.v, logger, func(context.Context) error { return nil })
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, a, buf
}

// TestUnitInternalErrorLogCarriesCause pins the shared 500 helper: the
// problem body stays generic while the log line carries the cause.
func TestUnitInternalErrorLogCarriesCause(t *testing.T) {
	boom := errors.New("pg exploded")
	st := &stubStore{getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
		return store.Entry{}, boom
	}}
	srv, a, buf := newLoggedServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/entries/"+uuid.NewString(), a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusInternalServerError, "internal")

	line := findLine(buf.lines(t), "internal error")
	if line == nil {
		t.Fatal("no internal error log line")
	}
	if line["level"] != "ERROR" || line["detail"] != "get failed" ||
		!strings.Contains(fmt.Sprint(line["err"]), "pg exploded") {
		t.Fatalf("internal error line: %v", line)
	}
}

func TestUnitSubmissionCreatedAndCapLogs(t *testing.T) {
	userID := uuid.New()
	entryID := uuid.New()
	subID := uuid.New()
	custom := store.Entry{ID: entryID, UserID: userID, ItemType: "game", DisplayName: "Repro", Region: "pal"}
	pendingCount, windowCount := int64(0), int64(0)
	st := &stubStore{
		getEntry:                func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return custom, nil },
		countPendingSubmissions: func(context.Context, uuid.UUID) (int64, error) { return pendingCount, nil },
		countSubmissionsSince:   func(context.Context, uuid.UUID, time.Time) (int64, error) { return windowCount, nil },
		createSubmission: func(_ context.Context, u, id uuid.UUID) (store.Submission, error) {
			return store.Submission{ID: subID, EntryID: id, UserID: u, Status: "pending",
				CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil
		},
	}
	srv, a, buf := newLoggedServer(t, st, &stubEnrichment{}, newStubCache())
	bearer := a.token(t, userID.String())
	url := srv.URL + "/entries/" + entryID.String() + "/submission"

	if resp := do(t, http.MethodPost, url, bearer, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	line := findLine(buf.lines(t), "submission created")
	if line == nil || line["level"] != "INFO" ||
		line["submission_id"] != subID.String() || line["entry_id"] != entryID.String() {
		t.Fatalf("created line: %v", line)
	}

	// The two cap branches log WARN naming the user and which cap bit.
	pendingCount = 10
	if resp := do(t, http.MethodPost, url, bearer, nil); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("pending cap: %d", resp.StatusCode)
	}
	pendingCount, windowCount = 0, 20
	if resp := do(t, http.MethodPost, url, bearer, nil); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate cap: %d", resp.StatusCode)
	}
	var caps []string
	for _, l := range buf.lines(t) {
		if l["msg"] != "submission cap hit" {
			continue
		}
		if l["level"] != "WARN" || l["user_id"] != userID.String() {
			t.Fatalf("cap line: %v", l)
		}
		caps = append(caps, fmt.Sprint(l["cap"]))
	}
	if len(caps) != 2 || caps[0] != "pending" || caps[1] != "rate" {
		t.Fatalf("caps logged: %v", caps)
	}
}

// TestUnitSubmissionVerdictLogs pins the admin audit trail: reject
// logs the action without a product, approvals name the adopted
// product and the arm that chose it.
func TestUnitSubmissionVerdictLogs(t *testing.T) {
	subID := uuid.New()
	userID := uuid.New()
	entryID := uuid.New()
	productID := uuid.New()
	pending := store.Submission{ID: subID, EntryID: entryID, UserID: userID, Status: "pending",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	st := &stubStore{
		getSubmission: func(context.Context, uuid.UUID) (store.Submission, error) { return pending, nil },
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
			return store.Entry{ID: entryID, UserID: userID, Region: "pal"}, nil
		},
		rejectSubmission: func(_ context.Context, _ uuid.UUID, reason string) (store.Submission, error) {
			out := pending
			out.Status = "rejected"
			out.RejectReason = &reason
			return out, nil
		},
		approveSubmission: func(_ context.Context, _ uuid.UUID, snap store.CatalogSnapshot) (store.Submission, error) {
			out := pending
			out.Status = "approved"
			out.ProductID = &snap.ProductID
			return out, nil
		},
	}
	enrich := &stubEnrichment{getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
		return enrichapi.Product{Id: productID, Type: "game", Name: "Chrono Trigger"}, nil
	}}
	srv, a, buf := newLoggedServer(t, st, enrich, newStubCache())
	admin := a.token(t, uuid.NewString(), "admin")
	url := srv.URL + "/admin/submissions/" + subID.String() + "/verdict"

	if resp := do(t, http.MethodPost, url, admin, jsonBody(map[string]any{"action": "reject", "reason": "not a shared item"})); resp.StatusCode != http.StatusOK {
		t.Fatalf("reject: %d", resp.StatusCode)
	}
	if resp := do(t, http.MethodPost, url, admin, jsonBody(map[string]any{"action": "approve_existing", "product_id": productID.String()})); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %d", resp.StatusCode)
	}

	var verdicts []map[string]any
	for _, l := range buf.lines(t) {
		if l["msg"] == "submission verdict" {
			verdicts = append(verdicts, l)
		}
	}
	if len(verdicts) != 2 {
		t.Fatalf("verdict lines: %d, want 2", len(verdicts))
	}
	reject := verdicts[0]
	if reject["level"] != "INFO" || reject["action"] != "reject" ||
		reject["submission_id"] != subID.String() || reject["entry_id"] != entryID.String() {
		t.Fatalf("reject line: %v", reject)
	}
	if _, ok := reject["product_id"]; ok {
		t.Fatalf("reject must not name a product: %v", reject)
	}
	approve := verdicts[1]
	if approve["level"] != "INFO" || approve["action"] != "approve_existing" ||
		approve["submission_id"] != subID.String() || approve["entry_id"] != entryID.String() ||
		approve["product_id"] != productID.String() {
		t.Fatalf("approve line: %v", approve)
	}
}

// TestUnitLeverCompletionLogs pins the durable outcome lines both
// admin levers write before answering.
func TestUnitLeverCompletionLogs(t *testing.T) {
	st := &stubStore{
		listGameBackedRefs:          func(context.Context) ([]store.GameEntryRef, error) { return nil, nil },
		listNameOnlyPlatformEntries: func(context.Context) ([]store.PlatformEntryRef, error) { return nil, nil },
	}
	enrich := &stubEnrichment{listPlatforms: func(context.Context, string) ([]enrichmentclient.Platform, error) {
		return nil, nil
	}}
	srv, a, buf := newLoggedServer(t, st, enrich, newStubCache())
	admin := a.token(t, uuid.NewString(), "admin")

	if resp := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", admin, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("resnapshot: %d", resp.StatusCode)
	}
	if resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-platforms", admin, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("normalize: %d", resp.StatusCode)
	}

	lines := buf.lines(t)
	rl := findLine(lines, "resnapshot complete")
	if rl == nil || rl["level"] != "INFO" || rl["products_seen"] != float64(0) ||
		rl["products_failed"] != float64(0) || rl["entries_updated"] != float64(0) {
		t.Fatalf("resnapshot line: %v", rl)
	}
	nl := findLine(lines, "normalize-platforms complete")
	if nl == nil || nl["level"] != "INFO" || nl["scanned"] != float64(0) ||
		nl["normalized"] != float64(0) || nl["skipped"] != float64(0) {
		t.Fatalf("normalize line: %v", nl)
	}
}
