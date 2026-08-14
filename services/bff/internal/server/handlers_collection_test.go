// Tests for the collection surface: entries, tags, saved views,
// and the dashboard.

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/collectionapi"
)

// TestUnitViewPublish_FiresEventFailOpen pins publishIfListed: a
// successful view write that lands visibility=listed fires
// social.RecordPublish with the view's id and the caller's own bearer;
// a RecordPublish failure is swallowed (fail-open) so the view save
// itself still answers exactly the collection relay - a social outage
// must never fail the write. RecordPublish must not fire at all when
// the write did not land listed: a private body, or any non-2xx
// collection answer regardless of body. Both CreateView (201, id
// parsed from the relay body) and UpdateView (200) are covered. Two
// divergent-body subtests pin that the RESULT decides, not the
// REQUEST: a request that asked for listed but whose result came back
// private must not fire, and a request that never asked for listed
// but whose result came back listed anyway must.
func TestUnitViewPublish_FiresEventFailOpen(t *testing.T) {
	viewID := uuid.New()
	const listedBody = `{"name":"Backlog","params":{},"visibility":"listed"}`
	const privateBody = `{"name":"Backlog","params":{},"visibility":"private"}`
	relayed := func(id uuid.UUID) []byte {
		return []byte(`{"id":"` + id.String() + `","name":"Backlog","params":{},"visibility":"listed"}`)
	}

	setup := func(col *stubCollection, soc *stubSocialFull) (*Handlers, *testEnv) {
		h := newTestHandlers(t, newStubCache(), &stubAuth{})
		h.collection, h.social = col, soc
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
	}

	t.Run("PUT listed, collection 200: RecordPublish fires with the view id and bearer", func(t *testing.T) {
		var calls int
		var gotID uuid.UUID
		var gotBearer string
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			return collectionclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: relayed(viewID)}, nil
		}}
		soc := &stubSocialFull{recordPublish: func(_ context.Context, bearer string, id uuid.UUID) error {
			calls++
			gotID, gotBearer = id, bearer
			return nil
		}}
		h, env := setup(col, soc)
		rec := doAuthedBody(t, h, env, http.MethodPut, "/api/views/"+viewID.String(), listedBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
		if calls != 1 || gotID != viewID {
			t.Fatalf("RecordPublish calls = %d id = %v, want exactly 1 call with %v", calls, gotID, viewID)
		}
		if gotBearer != env.sessionAccessToken {
			t.Fatalf("RecordPublish bearer = %q, want the caller's own session token", gotBearer)
		}
	})

	t.Run("PUT listed, RecordPublish errors: the save still answers the collection relay (fail-open)", func(t *testing.T) {
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			return collectionclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: relayed(viewID)}, nil
		}}
		soc := &stubSocialFull{recordPublish: func(context.Context, string, uuid.UUID) error {
			return errors.New("social down")
		}}
		h, env := setup(col, soc)
		rec := doAuthedBody(t, h, env, http.MethodPut, "/api/views/"+viewID.String(), listedBody)
		if rec.Code != http.StatusOK || rec.Body.String() != string(relayed(viewID)) {
			t.Fatalf("a social outage must not change the relay: code = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("PUT private: RecordPublish is never called", func(t *testing.T) {
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			return collectionclient.Result{Status: http.StatusOK, ContentType: "application/json",
				Body: []byte(`{"id":"` + viewID.String() + `","name":"Backlog","params":{},"visibility":"private"}`)}, nil
		}}
		soc := &stubSocialFull{} // recordPublish left nil: an unwanted call panics
		h, env := setup(col, soc)
		rec := doAuthedBody(t, h, env, http.MethodPut, "/api/views/"+viewID.String(), privateBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("PUT listed, collection 409: RecordPublish is never called", func(t *testing.T) {
		const problem = `{"type":"about:blank","title":"Conflict","status":409,"code":"conflict"}`
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			return collectionclient.Result{Status: http.StatusConflict, ContentType: "application/problem+json", Body: []byte(problem)}, nil
		}}
		soc := &stubSocialFull{} // recordPublish left nil: an unwanted call panics
		h, env := setup(col, soc)
		rec := doAuthedBody(t, h, env, http.MethodPut, "/api/views/"+viewID.String(), listedBody)
		if rec.Code != http.StatusConflict || rec.Body.String() != problem {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("PUT listed, collection 200 with no id in the relay body: RecordPublish is never called", func(t *testing.T) {
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			return collectionclient.Result{Status: http.StatusOK, ContentType: "application/json",
				Body: []byte(`{"name":"Backlog","params":{},"visibility":"listed"}`)}, nil
		}}
		soc := &stubSocialFull{} // recordPublish left nil: an unwanted call panics
		h, env := setup(col, soc)
		rec := doAuthedBody(t, h, env, http.MethodPut, "/api/views/"+viewID.String(), listedBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST listed, collection 201: RecordPublish fires with the created view's id parsed from the relay body", func(t *testing.T) {
		var calls int
		var gotID uuid.UUID
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			return collectionclient.Result{Status: http.StatusCreated, ContentType: "application/json", Body: relayed(viewID)}, nil
		}}
		soc := &stubSocialFull{recordPublish: func(_ context.Context, _ string, id uuid.UUID) error {
			calls++
			gotID = id
			return nil
		}}
		h, env := setup(col, soc)
		rec := doAuthedBody(t, h, env, http.MethodPost, "/api/views", listedBody)
		if rec.Code != http.StatusCreated {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
		if calls != 1 || gotID != viewID {
			t.Fatalf("RecordPublish calls = %d id = %v, want exactly 1 call with %v", calls, gotID, viewID)
		}
	})

	t.Run("POST private: RecordPublish is never called", func(t *testing.T) {
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			return collectionclient.Result{Status: http.StatusCreated, ContentType: "application/json",
				Body: []byte(`{"id":"` + viewID.String() + `","name":"Backlog","params":{},"visibility":"private"}`)}, nil
		}}
		soc := &stubSocialFull{} // recordPublish left nil: an unwanted call panics
		h, env := setup(col, soc)
		rec := doAuthedBody(t, h, env, http.MethodPost, "/api/views", privateBody)
		if rec.Code != http.StatusCreated {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("PUT request says listed but the result says private: RecordPublish does not fire (the result governs, not the request)", func(t *testing.T) {
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			return collectionclient.Result{Status: http.StatusOK, ContentType: "application/json",
				Body: []byte(`{"id":"` + viewID.String() + `","name":"Backlog","params":{},"visibility":"private"}`)}, nil
		}}
		soc := &stubSocialFull{} // recordPublish left nil: an unwanted call panics
		h, env := setup(col, soc)
		rec := doAuthedBody(t, h, env, http.MethodPut, "/api/views/"+viewID.String(), listedBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("PUT request says private but the result says listed: RecordPublish fires with the result's id (the result governs, not the request)", func(t *testing.T) {
		var calls int
		var gotID uuid.UUID
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			return collectionclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: relayed(viewID)}, nil
		}}
		soc := &stubSocialFull{recordPublish: func(_ context.Context, _ string, id uuid.UUID) error {
			calls++
			gotID = id
			return nil
		}}
		h, env := setup(col, soc)
		rec := doAuthedBody(t, h, env, http.MethodPut, "/api/views/"+viewID.String(), privateBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
		}
		if calls != 1 || gotID != viewID {
			t.Fatalf("RecordPublish calls = %d id = %v, want exactly 1 call with %v", calls, gotID, viewID)
		}
	})
}

// newTestHandlersWithCollection wires a session-ready Handlers around
// the collection stub.
func newTestHandlersWithCollection(t *testing.T, col *stubCollection) (*Handlers, *testEnv) {
	t.Helper()
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.collection = col
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
}

func TestUnitCollectionPassThroughs_RouteMatrix(t *testing.T) {
	id := uuid.NewString()
	routes := []struct {
		method, path, op string
		body             string
		status           int
	}{
		{http.MethodGet, "/api/entries?status=backlog", "list_entries", "", 200},
		{http.MethodPost, "/api/entries", "create_entry", `{"product_id":"x"}`, 201},
		{http.MethodGet, "/api/entries/" + id, "get_entry", "", 200},
		{http.MethodPut, "/api/entries/" + id, "update_entry", `{}`, 200},
		{http.MethodDelete, "/api/entries/" + id, "delete_entry", "", 204},
		{http.MethodPost, "/api/entries/" + id + "/reorder", "reorder_entry", `{"after_id":null}`, 200},
		{http.MethodPost, "/api/entries/" + id + "/region-mismatch-ack", "ack_region_mismatch", "", 204},
		{http.MethodPost, "/api/entries/bulk-update", "bulk_update_entries", `{"entry_ids":["` + id + `"],"status":"playing"}`, 200},
		{http.MethodGet, "/api/tags", "list_tags", "", 200},
		{http.MethodPost, "/api/tags", "create_tag", `{"name":"x"}`, 201},
		{http.MethodPut, "/api/tags/" + id, "rename_tag", `{"name":"y"}`, 200},
		{http.MethodDelete, "/api/tags/" + id, "delete_tag", "", 204},
		{http.MethodGet, "/api/views", "list_views", "", 200},
		{http.MethodPost, "/api/views", "create_view", `{"name":"v","params":{}}`, 201},
		{http.MethodPut, "/api/views/" + id, "update_view", `{"name":"v","params":{}}`, 200},
		{http.MethodDelete, "/api/views/" + id, "delete_view", "", 204},
		{http.MethodGet, "/api/dashboard", "dashboard", "", 200},
	}
	for _, rt := range routes {
		t.Run(rt.op, func(t *testing.T) {
			col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
				if op != rt.op {
					t.Fatalf("routed to %q, want %q", op, rt.op)
				}
				return collectionclient.Result{Status: rt.status, ContentType: "application/json", Body: []byte(`{"ok":true}`)}, nil
			}}
			h, env := newTestHandlersWithCollection(t, col)
			var body io.Reader
			if rt.body != "" {
				body = strings.NewReader(rt.body)
			}
			req := httptest.NewRequest(rt.method, rt.path, body)
			req.AddCookie(env.cookie)
			if rt.body != "" {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Origin", "http://localhost:8090")
			}
			rec := httptest.NewRecorder()
			newRouterFor(t, h).ServeHTTP(rec, req)
			if rec.Code != rt.status {
				t.Fatalf("relay status: %d, want %d (%s)", rec.Code, rt.status, rec.Body.String())
			}
			if got := col.gotBearer[len(col.gotBearer)-1]; got != env.sessionAccessToken {
				t.Fatalf("the session token must ride the hop, got %q", got)
			}
		})
	}
}

func TestUnitCollectionPassThroughs_UpstreamFailureIs502(t *testing.T) {
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{}, collectionclient.ErrUpstream
	}}
	h, env := newTestHandlersWithCollection(t, col)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/dashboard")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
}

// TestUnitCollectionReorderPassThrough_RelaysProblemBody mirrors
// TestUnitProductPassThrough_RelaysProblemBody for a collection route:
// a conflict from the collection service (two backlog items racing for
// the same rank) must relay verbatim - status, content type, and body -
// exactly like every other pass-through, never rewritten by the bff.
func TestUnitCollectionReorderPassThrough_RelaysProblemBody(t *testing.T) {
	const problemBody = `{"type":"about:blank","title":"Conflict","status":409,"code":"conflicting_order"}`
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 409, ContentType: "application/problem+json",
			Body: []byte(problemBody)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	id := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/api/entries/"+id+"/reorder", strings.NewReader(`{"after_id":null}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != 409 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("problem relay: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != problemBody {
		t.Fatalf("body must relay verbatim, got %s", rec.Body.String())
	}
}

// TestUnitValueHistoryPassThrough proves the value-history route relays
// the collection service's body verbatim and forwards the session's
// own bearer, exactly like the other collection pass-throughs.
func TestUnitValueHistoryPassThrough(t *testing.T) {
	relayed := []byte(`{"available":true,"points":[{"date":"2026-07-01","value_cents":4200}]}`)
	col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
		if op != "value_history" {
			t.Fatalf("routed to %q, want value_history", op)
		}
		return collectionclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: relayed}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/dashboard/value-history")
	if rec.Code != http.StatusOK || rec.Body.String() != string(relayed) {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if got := col.gotBearer[len(col.gotBearer)-1]; got != env.sessionAccessToken {
		t.Fatalf("the session token must ride the hop, got %q", got)
	}
}

// captureCollection embeds the stub so every method forwards, while
// ListEntries, GetDashboard, CreateEntry, and UpdateEntry additionally
// expose their converted params / raw body.
type captureCollection struct {
	*stubCollection
	onList        func(*collectionapi.ListEntriesParams)
	onDashboard   func(*collectionapi.GetDashboardParams)
	onCreateEntry func(body []byte)
	onUpdateEntry func(id uuid.UUID, body []byte)
}

func (c *captureCollection) ListEntries(ctx context.Context, bearer string, p *collectionapi.ListEntriesParams) (collectionclient.Result, error) {
	c.onList(p)
	return c.stubCollection.ListEntries(ctx, bearer, p)
}

func (c *captureCollection) GetDashboard(ctx context.Context, bearer string, p *collectionapi.GetDashboardParams) (collectionclient.Result, error) {
	c.onDashboard(p)
	return c.stubCollection.GetDashboard(ctx, bearer, p)
}

func (c *captureCollection) CreateEntry(ctx context.Context, bearer string, body []byte) (collectionclient.Result, error) {
	c.onCreateEntry(body)
	return c.stubCollection.CreateEntry(ctx, bearer, body)
}

func (c *captureCollection) UpdateEntry(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error) {
	c.onUpdateEntry(id, body)
	return c.stubCollection.UpdateEntry(ctx, bearer, id, body)
}

func TestUnitCollectionListParams_Conversion(t *testing.T) {
	var got *collectionapi.ListEntriesParams
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{}`)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	h.collection = &captureCollection{stubCollection: col, onList: func(p *collectionapi.ListEntriesParams) { got = p }}
	rec := doAuthed(t, h, env, http.MethodGet,
		"/api/entries?status=backlog&status=playing&sort=value&order=desc&group_by=platform&platform_id=6&developer=Nintendo&developer=Rare&publisher=THQ")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if got == nil || got.Status == nil || len(*got.Status) != 2 ||
		string((*got.Status)[0]) != "backlog" || string(*got.Sort) != "value" ||
		string(*got.Order) != "desc" || string(*got.GroupBy) != "platform" ||
		(*got.PlatformId)[0] != 6 {
		t.Fatalf("converted params: %+v", got)
	}
	if got.Developer == nil || len(*got.Developer) != 2 || (*got.Developer)[0] != "Nintendo" || (*got.Developer)[1] != "Rare" ||
		got.Publisher == nil || len(*got.Publisher) != 1 || (*got.Publisher)[0] != "THQ" {
		t.Fatalf("converted developer/publisher: %+v", got)
	}
}

func TestUnitDashboardParams_Forwarded(t *testing.T) {
	var got *collectionapi.GetDashboardParams
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{}`)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	h.collection = &captureCollection{stubCollection: col, onDashboard: func(p *collectionapi.GetDashboardParams) { got = p }}
	rec := doAuthed(t, h, env, http.MethodGet,
		"/api/dashboard?status=backlog&item_type=game&platform_id=6&developer=Nintendo&developer=Rare&publisher=THQ")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if got == nil || got.Status == nil || len(*got.Status) != 1 ||
		string((*got.Status)[0]) != "backlog" ||
		got.ItemType == nil || string((*got.ItemType)[0]) != "game" ||
		got.PlatformId == nil || (*got.PlatformId)[0] != 6 {
		t.Fatalf("forwarded params: %+v", got)
	}
	if got.Developer == nil || len(*got.Developer) != 2 || (*got.Developer)[0] != "Nintendo" || (*got.Developer)[1] != "Rare" ||
		got.Publisher == nil || len(*got.Publisher) != 1 || (*got.Publisher)[0] != "THQ" {
		t.Fatalf("forwarded developer/publisher: %+v", got)
	}
}

// populateEveryPointerField sets every pointer field of v (a struct
// value addressed via reflect.ValueOf(&x).Elem()) to a non-nil pointer
// - a one-element slice for a slice pointer, a zero value otherwise.
// Every field of the generated *Params structs is a pointer, so a
// struct built this way exercises every field a mapping function must
// carry through.
func populateEveryPointerField(v reflect.Value) {
	st := v.Type()
	for i := 0; i < st.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.Ptr {
			continue
		}
		elem := f.Type().Elem()
		ptr := reflect.New(elem)
		if elem.Kind() == reflect.Slice {
			ptr.Elem().Set(reflect.MakeSlice(elem, 1, 1))
		}
		f.Set(ptr)
	}
}

// assertEveryFieldMapped holds the class-closing invariant a query-
// param mapping function must keep: every source field must land on
// its same-named destination field non-nil, so a future contract param
// added to one generated package and forgotten in the hand-written
// mapping fails a test instead of silently dropping a filter - exactly
// this task's bug, for Developer/Publisher.
func assertEveryFieldMapped(t *testing.T, src, dst reflect.Value) {
	t.Helper()
	st := src.Type()
	for i := 0; i < st.NumField(); i++ {
		name := st.Field(i).Name
		df := dst.FieldByName(name)
		if !df.IsValid() {
			continue
		}
		if df.Kind() != reflect.Ptr || df.IsNil() {
			t.Errorf("field %s: not carried through the mapping", name)
		}
	}
}

func TestUnitCollectionListParams_MapsEveryField(t *testing.T) {
	var src api.ListEntriesParams
	populateEveryPointerField(reflect.ValueOf(&src).Elem())
	dst := collectionListParams(src)
	assertEveryFieldMapped(t, reflect.ValueOf(src), reflect.ValueOf(*dst))
}

func TestUnitDashboardParams_MapsEveryField(t *testing.T) {
	var src api.GetDashboardParams
	populateEveryPointerField(reflect.ValueOf(&src).Elem())
	dst := collectionDashboardParams(src)
	assertEveryFieldMapped(t, reflect.ValueOf(src), reflect.ValueOf(*dst))
}

// TestUnitUpdateEntryPassThrough_CustomPricingRoundTrips pins that a
// pricing_mode=custom entry update reaches the collection stub with
// its body unmodified, and the stub's answer - carrying
// custom_value_cents/custom_value_set_at - round-trips to the client
// verbatim. The bff neither validates nor reshapes custom pricing; it
// is a pass-through like every other entry mutation.
func TestUnitUpdateEntryPassThrough_CustomPricingRoundTrips(t *testing.T) {
	id := uuid.New()
	const sent = `{"pricing_mode":"custom","custom_value_cents":12345}`
	relayed := []byte(`{"id":"` + id.String() + `","pricing_mode":"custom","custom_value_cents":12345,"custom_value_set_at":"2026-07-09T00:00:00Z"}`)

	col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
		if op != "update_entry" {
			t.Fatalf("routed to %q, want update_entry", op)
		}
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: relayed}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	var gotID uuid.UUID
	var gotBody []byte
	h.collection = &captureCollection{stubCollection: col, onUpdateEntry: func(recvID uuid.UUID, body []byte) {
		gotID, gotBody = recvID, body
	}}

	req := httptest.NewRequest(http.MethodPut, "/api/entries/"+id.String(), strings.NewReader(sent))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)

	if gotID != id || string(gotBody) != sent {
		t.Fatalf("collection saw id=%s body=%s, want id=%s body=%s", gotID, gotBody, id, sent)
	}
	if rec.Code != 200 || rec.Body.String() != string(relayed) {
		t.Fatalf("relay: %d %s, want 200 %s", rec.Code, rec.Body.String(), relayed)
	}
}

// TestUnitUpdateEntryPassThrough_CustomPricingEnteredPairRoundTrips pins
// that the typed custom-price pair (custom_value_entered_cents /
// custom_value_entered_currency) rides an entry update through the bff
// exactly like every other custom pricing field: unmodified in, and the
// collection stub's answer - carrying the pair - relays to the client
// verbatim. The bff neither validates nor reshapes it.
func TestUnitUpdateEntryPassThrough_CustomPricingEnteredPairRoundTrips(t *testing.T) {
	id := uuid.New()
	const sent = `{"pricing_mode":"custom","custom_value_cents":5400,"custom_value_entered_cents":6000,"custom_value_entered_currency":"EUR"}`
	const relayed = `{"id":"e1","custom_value_cents":5400,"custom_value_entered_cents":6000,"custom_value_entered_currency":"EUR","pricing_mode":"custom"}`

	col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
		if op != "update_entry" {
			t.Fatalf("routed to %q, want update_entry", op)
		}
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(relayed)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	var gotID uuid.UUID
	var gotBody []byte
	h.collection = &captureCollection{stubCollection: col, onUpdateEntry: func(recvID uuid.UUID, body []byte) {
		gotID, gotBody = recvID, body
	}}

	req := httptest.NewRequest(http.MethodPut, "/api/entries/"+id.String(), strings.NewReader(sent))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)

	if gotID != id || string(gotBody) != sent {
		t.Fatalf("collection saw id=%s body=%s, want id=%s body=%s", gotID, gotBody, id, sent)
	}
	if rec.Code != 200 || rec.Body.String() != relayed {
		t.Fatalf("relay: %d %s, want 200 %s", rec.Code, rec.Body.String(), relayed)
	}
}

// TestUnitCreateEntryPassThrough_OpenWorldRegionRoundTrips pins that an
// entry create body carrying a region outside the known
// ntsc_u/ntsc_j/pal/region_free set reaches the collection stub
// byte-identical, and the stub's answer relays back unmodified. The bff
// contract widened region to an open string alongside the collection
// service; the create path was already a pure byte relay with no
// region-shaped validation of its own, so this pins the widened value
// travels the same way every other entry field already does.
func TestUnitCreateEntryPassThrough_OpenWorldRegionRoundTrips(t *testing.T) {
	const sent = `{"item_type":"game","display_name":"Import Cart","packaging":"loose","region":"Korea"}`
	const relayed = `{"id":"e1","region":"Korea"}`

	col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
		if op != "create_entry" {
			t.Fatalf("routed to %q, want create_entry", op)
		}
		return collectionclient.Result{Status: 201, ContentType: "application/json", Body: []byte(relayed)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	var gotBody []byte
	h.collection = &captureCollection{stubCollection: col, onCreateEntry: func(body []byte) {
		gotBody = body
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/entries", strings.NewReader(sent))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)

	if string(gotBody) != sent {
		t.Fatalf("collection saw body=%s, want %s", gotBody, sent)
	}
	if rec.Code != 201 || rec.Body.String() != relayed {
		t.Fatalf("relay: %d %s, want 201 %s", rec.Code, rec.Body.String(), relayed)
	}
}

func TestUnitEntryMutationInvalidatesRecs(t *testing.T) {
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 201, ContentType: "application/json", Body: []byte(`{}`)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	sc := h.cache.(*stubCache)
	// Pre-seed a cached recommendation body for this session's subject.
	sub := subjectOf(t, env.sessionAccessToken)
	sc.recs[sub] = []byte(`{"degraded":false,"recommendations":[]}`)

	req := httptest.NewRequest(http.MethodPost, "/api/entries", strings.NewReader(`{"product_id":"x"}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("mutation: %d", rec.Code)
	}
	if sc.recs[sub] != nil {
		t.Fatal("a successful entry mutation must invalidate the recommendations cache")
	}
}

// subjectOf decodes the sub claim from an unverified test token.
func subjectOf(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims.Sub
}

// TestUnitAckEntryRegionMismatch_RelaysAndForwardsBearer mirrors
// TestUnitAckSubmission_RelaysAndForwardsBearer above for the
// region-mismatch ack: an authed POST relays 204 and forwards the
// caller's own bearer to collection; no session is 401.
func TestUnitAckEntryRegionMismatch_RelaysAndForwardsBearer(t *testing.T) {
	col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
		if op != "ack_region_mismatch" {
			t.Fatalf("routed to %q, want ack_region_mismatch", op)
		}
		return collectionclient.Result{Status: http.StatusNoContent}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	entry := uuid.NewString()

	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/entries/"+entry+"/region-mismatch-ack", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ack relay: %d %s", rec.Code, rec.Body.String())
	}
	if got := col.gotBearer[len(col.gotBearer)-1]; got != env.sessionAccessToken {
		t.Fatalf("bearer: %q", got)
	}
	rec = doUnauthed(t, h, env, http.MethodPost, "/api/entries/"+entry+"/region-mismatch-ack")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}
