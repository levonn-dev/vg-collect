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
	"os"
	"slices"
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

	"github.com/levonn-dev/vgkeep/libs/go/metrictest"
	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/server"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

const collectionMeter = "github.com/levonn-dev/vgkeep/services/collection"

// TestMain pins the global meter provider to a real (readerless) SDK
// provider before any test in this package runs. server.New registers
// this package's pending-submissions Observable gauge on every build,
// and nearly every test in this package - not just the telemetry ones
// below - constructs a Handlers. Without this, the default delegating
// provider would queue every one of those gauge callbacks (each
// closing over its own test's now-gone stub) and replay all of them
// into the first manual reader metrictest.Install installs here, so
// that test's Collect would invoke every earlier test's stub too -
// most of which never wired countAllPendingSubmissions and panic on
// the call. Mirrors auth/server's TestMain, needed for the identical
// reason (its own constructor-registered signing-keys gauge).
func TestMain(m *testing.M) {
	otel.SetMeterProvider(sdkmetric.NewMeterProvider())
	os.Exit(m.Run())
}

func collectDomainMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	return metrictest.ScopeMetrics(t, reader, collectionMeter)
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
				reader := metrictest.Install(t)
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
	reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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
		reader := metrictest.Install(t)
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

// TestUnitRematchMetrics pins the entry rematch's three domain
// instruments: one triples point per outcome (ok: nothing pending or
// the triple's resolve succeeded; failed: a member fetch or resolve
// error), one repoints increment per entry actually repointed, and a
// duration series that exports once the CAS-gated, now-detached run
// completes. The trigger answers 202 immediately, so the duration
// histogram gaining its one point (recorded last, deferred) is the
// external proof the run has fully finished; every other assertion
// below only runs once that is true.
func TestUnitRematchMetrics(t *testing.T) {
	reader := metrictest.Install(t)
	productBase := uuid.New()
	productJP := uuid.New()
	entryOK, entryFail := uuid.New(), uuid.New()

	// Base class is region-correct for neither triple below, so both
	// land on the resolve leg; ntsc_j succeeds (repoints), pal fails
	// (the resolve stub errors on it).
	baseMember := pricedGameProduct(productBase, "Super Nintendo")
	jpMember := pricedGameProduct(productJP, "Super Famicom")

	refs := []store.RematchEntryRef{
		{EntryID: entryOK, ProductID: productBase, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
		{EntryID: entryFail, ProductID: productBase, IGDBGameID: 2000, PlatformIGDBID: 7, Region: "pal"},
	}
	st := withPendingCount(&stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) { return refs, nil },
		repointEntry: func(context.Context, uuid.UUID, uuid.UUID, *time.Time, *string, *string, *string, []string, []string) error {
			return nil
		},
	}, 0)
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			return baseMember, nil
		},
		resolve: func(_ context.Context, _ string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
			if req.Region != nil && *req.Region == "pal" {
				return enrichapi.Product{}, enrichmentclient.ErrUnavailable
			}
			return jpMember, nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", a.token(t, uuid.NewString(), "admin"), nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("trigger: %d, want 202 (the sweep now detaches)", resp.StatusCode)
	}

	reqtest.WaitFor(t, 5*time.Second, func() bool {
		m, ok := collectDomainMetrics(t, reader)["vg.collection.rematch.duration"]
		if !ok {
			return false
		}
		hist, ok := m.Data.(metricdata.Histogram[float64])
		return ok && len(hist.DataPoints) == 1
	})

	metrics := collectDomainMetrics(t, reader)
	if v := counterValue(t, metrics, "vg.collection.rematch.triples", attribute.String("outcome", "ok")); v != 1 {
		t.Fatalf("triples{outcome=ok} = %d, want 1", v)
	}
	if v := counterValue(t, metrics, "vg.collection.rematch.triples", attribute.String("outcome", "failed")); v != 1 {
		t.Fatalf("triples{outcome=failed} = %d, want 1", v)
	}
	if v := counterValue(t, metrics, "vg.collection.rematch.repoints"); v != 1 {
		t.Fatalf("repoints = %d, want 1", v)
	}
	m, ok := metrics["vg.collection.rematch.duration"]
	if !ok {
		t.Fatal("duration histogram not exported")
	}
	hist, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("duration: want Histogram[float64], got %T", m.Data)
	}
	if len(hist.DataPoints) != 1 {
		t.Fatalf("duration points = %d, want exactly one (the single completed run)", len(hist.DataPoints))
	}
	// Widened like enrichment's refresh.step_duration: the SDK defaults
	// top out at 10s and would flatten a multi-minute run into the last
	// bucket.
	if want := []float64{1, 5, 15, 60, 300, 900, 1800}; !slices.Equal(hist.DataPoints[0].Bounds, want) {
		t.Fatalf("duration bounds = %v, want %v", hist.DataPoints[0].Bounds, want)
	}
}

// TestUnitRematchMetrics_MemberFetchFailureCountsFailedTriple pins
// that a member-fetch failure (not just a resolve failure -
// TestUnitRematchMetrics's own pal triple) also counts its triple
// against triples{outcome=failed}: two triples, one healthy and one
// whose sole member fetch errors, land ok==1 and failed==1.
func TestUnitRematchMetrics_MemberFetchFailureCountsFailedTriple(t *testing.T) {
	reader := metrictest.Install(t)
	productOK := uuid.New()
	productBad := uuid.New()
	productJP := uuid.New()
	entryOK, entryBad := uuid.New(), uuid.New()

	okMember := pricedGameProduct(productOK, "Super Nintendo") // base class, not region-correct for ntsc_j
	jpMember := pricedGameProduct(productJP, "Super Famicom")

	refs := []store.RematchEntryRef{
		{EntryID: entryOK, ProductID: productOK, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
		{EntryID: entryBad, ProductID: productBad, IGDBGameID: 2000, PlatformIGDBID: 7, Region: "ntsc_j"},
	}
	st := withPendingCount(&stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) { return refs, nil },
		repointEntry: func(context.Context, uuid.UUID, uuid.UUID, *time.Time, *string, *string, *string, []string, []string) error {
			return nil
		},
	}, 0)
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			if id == productBad {
				return enrichapi.Product{}, enrichmentclient.ErrUnavailable
			}
			return okMember, nil
		},
		resolve: func(_ context.Context, _ string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
			return jpMember, nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", a.token(t, uuid.NewString(), "admin"), nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("trigger: %d, want 202 (the sweep now detaches)", resp.StatusCode)
	}

	reqtest.WaitFor(t, 5*time.Second, func() bool {
		m, ok := collectDomainMetrics(t, reader)["vg.collection.rematch.duration"]
		if !ok {
			return false
		}
		hist, ok := m.Data.(metricdata.Histogram[float64])
		return ok && len(hist.DataPoints) == 1
	})

	metrics := collectDomainMetrics(t, reader)
	if v := counterValue(t, metrics, "vg.collection.rematch.triples", attribute.String("outcome", "ok")); v != 1 {
		t.Fatalf("triples{outcome=ok} = %d, want 1", v)
	}
	if v := counterValue(t, metrics, "vg.collection.rematch.triples", attribute.String("outcome", "failed")); v != 1 {
		t.Fatalf("triples{outcome=failed} = %d, want 1 (the member-fetch failure)", v)
	}
}

// TestUnitRematchMetrics_ConflictNeverRecordsDuration pins that a
// 409-refused overlapping trigger never runs, so it must never emit a
// duration point either (only a CAS-won run does): one completed run
// plus one concurrent 409 must still export exactly one duration
// point, not two. The trigger itself answers 202 immediately (the CAS
// happens before the detach), so the first request no longer blocks -
// the second (409-refused) trigger only needs to land while the
// detached sweep is still inside listAutoGameRematchRefs.
func TestUnitRematchMetrics_ConflictNeverRecordsDuration(t *testing.T) {
	reader := metrictest.Install(t)
	release := make(chan struct{})
	started := make(chan struct{})
	st := withPendingCount(&stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) {
			close(started)
			<-release
			return nil, nil
		},
	}, 0)
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	tok := a.token(t, uuid.NewString(), "admin")

	resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", tok, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first trigger: %d, want 202", resp.StatusCode)
	}

	<-started
	if resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", tok, nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("concurrent trigger: %d, want 409", resp.StatusCode)
	}
	close(release)

	// The duration histogram records last (deferred), so waiting for
	// its one point proves the detached run has fully finished.
	reqtest.WaitFor(t, 5*time.Second, func() bool {
		m, ok := collectDomainMetrics(t, reader)["vg.collection.rematch.duration"]
		if !ok {
			return false
		}
		hist, ok := m.Data.(metricdata.Histogram[float64])
		return ok && len(hist.DataPoints) == 1
	})

	metrics := collectDomainMetrics(t, reader)
	m, ok := metrics["vg.collection.rematch.duration"]
	if !ok {
		t.Fatal("duration histogram not exported after the completed run")
	}
	hist, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("duration: want Histogram[float64], got %T", m.Data)
	}
	if len(hist.DataPoints) != 1 {
		t.Fatalf("duration points = %d, want exactly 1 (the 409 must not have recorded)", len(hist.DataPoints))
	}
}

// TestUnitRematchMetrics_ResolvedSameIdSkipsRepoint pins the
// no-in-region-listing convergence case: when the resolve leg's only
// candidate for the triple is the very same member the entry already
// sits on (resolved.Id == ref.ProductID), the repoint is a would-be
// no-op and must never be written. Proven against a row a real
// repoint would mutate - product_id, updated_at, and the ack stamp
// RepointEntry always clears - seeded so a wrongly fired repoint
// would visibly change all three.
func TestUnitRematchMetrics_ResolvedSameIdSkipsRepoint(t *testing.T) {
	reader := metrictest.Install(t)
	productUnmatched := uuid.New()
	entry := uuid.New()

	unmatched := gameProduct(productUnmatched) // no pricecharting -> never region-correct
	seedUpdatedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	seededAck := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)

	var mu sync.Mutex
	row := struct {
		productID uuid.UUID
		updatedAt time.Time
		ackAt     *time.Time
	}{productID: productUnmatched, updatedAt: seedUpdatedAt, ackAt: &seededAck}
	var repointCalls, resolveCalls int

	refs := []store.RematchEntryRef{
		{EntryID: entry, ProductID: productUnmatched, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
	}
	st := withPendingCount(&stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) { return refs, nil },
		repointEntry: func(_ context.Context, _, productID uuid.UUID, _ *time.Time, _, _, _ *string, _, _ []string) error {
			mu.Lock()
			defer mu.Unlock()
			repointCalls++
			// A real repoint would land exactly these three changes; wiring
			// them here means a wrongly fired call is caught below even if
			// the call-count assertion were ever weakened.
			row.productID = productID
			row.updatedAt = time.Now().UTC()
			row.ackAt = nil
			return nil
		},
	}, 0)
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) { return unmatched, nil },
		resolve: func(_ context.Context, _ string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
			mu.Lock()
			resolveCalls++
			mu.Unlock()
			return unmatched, nil // the resolve's only candidate is the same unmatched member
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", a.token(t, uuid.NewString(), "admin"), nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("trigger: %d, want 202 (the sweep now detaches)", resp.StatusCode)
	}

	reqtest.WaitFor(t, 5*time.Second, func() bool {
		m, ok := collectDomainMetrics(t, reader)["vg.collection.rematch.duration"]
		if !ok {
			return false
		}
		hist, ok := m.Data.(metricdata.Histogram[float64])
		return ok && len(hist.DataPoints) == 1
	})

	mu.Lock()
	defer mu.Unlock()
	if resolveCalls != 1 {
		t.Fatalf("resolve calls: %d, want 1 (must reach the resolve leg)", resolveCalls)
	}
	if repointCalls != 0 {
		t.Fatalf("resolved.Id == ref.ProductID must skip the repoint: %d calls", repointCalls)
	}
	if row.productID != productUnmatched || !row.updatedAt.Equal(seedUpdatedAt) || row.ackAt == nil || !row.ackAt.Equal(seededAck) {
		t.Fatalf("the skip must leave the row untouched: %+v", row)
	}
	metrics := collectDomainMetrics(t, reader)
	// A never-incremented counter exports no series at all (the same
	// posture TestUnitPricingComposeCounter_SkipsWhenNothingPriced
	// pins), so the repoints count for this run is the metric's
	// absence, not a zero-valued point.
	if _, ok := metrics["vg.collection.rematch.repoints"]; ok {
		t.Fatal("the skipped repoint must not export a repoints count")
	}
	if v := counterValue(t, metrics, "vg.collection.rematch.triples", attribute.String("outcome", "ok")); v != 1 {
		t.Fatalf("triples{outcome=ok} = %d, want 1", v)
	}
}

// ---- normalize levers ----

// TestUnitNormalizePlatformsCounter pins the normalize-platforms
// sweep's outcome counter: a matched free-text platform is normalized,
// an unmatched one is skipped.
func TestUnitNormalizePlatformsCounter(t *testing.T) {
	reader := metrictest.Install(t)
	matched, unmatched := uuid.New(), uuid.New()
	st := withPendingCount(&stubStore{
		listNameOnlyPlatformEntries: func(context.Context) ([]store.PlatformEntryRef, error) {
			return []store.PlatformEntryRef{
				{EntryID: matched, PlatformName: "snes"},
				{EntryID: unmatched, PlatformName: "my homebrew rig"},
			}, nil
		},
		setEntryPlatformIdentity: func(context.Context, uuid.UUID, int64, string) error { return nil },
	}, 0)
	enr := &stubEnrichment{
		listPlatforms: func(context.Context, string) ([]enrichmentclient.Platform, error) {
			return []enrichmentclient.Platform{{IGDBID: 19, Name: "Super Nintendo Entertainment System", Aliases: []string{"snes"}}}, nil
		},
	}
	srv, a := newUnitServer(t, st, enr, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-platforms", a.token(t, uuid.NewString(), "admin"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("normalize: %d", resp.StatusCode)
	}

	metrics := collectDomainMetrics(t, reader)
	if v := counterValue(t, metrics, "vg.collection.normalize.platforms", attribute.String("outcome", "normalized")); v != 1 {
		t.Fatalf("platforms{outcome=normalized} = %d, want 1", v)
	}
	if v := counterValue(t, metrics, "vg.collection.normalize.platforms", attribute.String("outcome", "skipped")); v != 1 {
		t.Fatalf("platforms{outcome=skipped} = %d, want 1", v)
	}
}

// TestUnitNormalizeRegionsCounter pins the normalize-regions sweep's
// outcome counter: a matched free-text region is normalized, one with
// no reviewed synonym is skipped.
func TestUnitNormalizeRegionsCounter(t *testing.T) {
	reader := metrictest.Install(t)
	matched, unmatched := uuid.New(), uuid.New()
	st := withPendingCount(&stubStore{
		listOpenRegionEntries: func(context.Context, []string) ([]store.OpenRegionEntryRef, error) {
			return []store.OpenRegionEntryRef{
				{EntryID: matched, Region: "Japan"},
				{EntryID: unmatched, Region: "atlantis"},
			}, nil
		},
		promoteEntryRegion: func(context.Context, uuid.UUID, string) error { return nil },
	}, 0)
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-regions", a.token(t, uuid.NewString(), "admin"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("normalize: %d", resp.StatusCode)
	}

	metrics := collectDomainMetrics(t, reader)
	if v := counterValue(t, metrics, "vg.collection.normalize.regions", attribute.String("outcome", "normalized")); v != 1 {
		t.Fatalf("regions{outcome=normalized} = %d, want 1", v)
	}
	if v := counterValue(t, metrics, "vg.collection.normalize.regions", attribute.String("outcome", "skipped")); v != 1 {
		t.Fatalf("regions{outcome=skipped} = %d, want 1", v)
	}
}

// TestUnitNormalizeRegionsCounter_WriteFailure mirrors enrichment's
// TestUnitTelemetry_NormalizeCommunityRegions write-failure case: a
// promotable non-igdb region whose store write errors counts only in
// the failed metric outcome - PromoteEntryRegion's error path never
// increments the response's own normalized or skipped counters (see
// InternalNormalizeRegions), so a write failure is the one way scanned
// can outrun their sum.
func TestUnitNormalizeRegionsCounter_WriteFailure(t *testing.T) {
	reader := metrictest.Install(t)
	failing := uuid.New()
	st := withPendingCount(&stubStore{
		listOpenRegionEntries: func(context.Context, []string) ([]store.OpenRegionEntryRef, error) {
			return []store.OpenRegionEntryRef{{EntryID: failing, Region: "japan"}}, nil
		},
		promoteEntryRegion: func(context.Context, uuid.UUID, string) error {
			return errors.New("db down")
		},
	}, 0)
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-regions", a.token(t, uuid.NewString(), "admin"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("normalize: %d", resp.StatusCode)
	}

	var counts map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&counts); err != nil {
		t.Fatal(err)
	}
	if counts["scanned"] != 1 || counts["normalized"] != 0 || counts["skipped"] != 0 {
		t.Fatalf("counts = %+v, want scanned 1 normalized 0 skipped 0", counts)
	}
	// The write failure lands in neither counter, so scanned outruns
	// their sum - a caller must not read {normalized+skipped==scanned}
	// as proof every scanned row was fully accounted for.
	if counts["scanned"] <= counts["normalized"]+counts["skipped"] {
		t.Fatalf("scanned (%d) must exceed normalized+skipped (%d) when a write fails", counts["scanned"], counts["normalized"]+counts["skipped"])
	}

	metrics := collectDomainMetrics(t, reader)
	if v := counterValue(t, metrics, "vg.collection.normalize.regions", attribute.String("outcome", "failed")); v != 1 {
		t.Fatalf("regions{outcome=failed} = %d, want 1", v)
	}
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
	for line := range strings.SplitSeq(strings.TrimSpace(b.buf.String()), "\n") {
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

// TestUnitLeverCompletionLogs pins the durable outcome lines all
// three internal levers write before (resnapshot, normalize-platforms)
// or after (the now-detached rematch) answering.
func TestUnitLeverCompletionLogs(t *testing.T) {
	st := withPendingCount(&stubStore{
		listGameBackedRefs:          func(context.Context) ([]store.GameEntryRef, error) { return nil, nil },
		listNameOnlyPlatformEntries: func(context.Context) ([]store.PlatformEntryRef, error) { return nil, nil },
		listAutoGameRematchRefs:     func(context.Context) ([]store.RematchEntryRef, error) { return nil, nil },
	}, 0)
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
	if resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", admin, nil); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("rematch-entries: %d, want 202 (the sweep now detaches)", resp.StatusCode)
	}

	// resnapshot and normalize-platforms are still synchronous, so
	// their lines are already in buf; the rematch's completion line
	// lands only once its detached sweep finishes.
	reqtest.WaitFor(t, 5*time.Second, func() bool {
		return findLine(buf.lines(t), "rematch-entries complete") != nil
	})

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
	rml := findLine(lines, "rematch-entries complete")
	if rml == nil || rml["level"] != "INFO" || rml["triples_seen"] != float64(0) ||
		rml["triples_failed"] != float64(0) || rml["entries_repointed"] != float64(0) {
		t.Fatalf("rematch-entries line: %v", rml)
	}
}
