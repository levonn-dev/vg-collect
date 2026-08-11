// Tests for catalog discovery: search, FX rates, the platform
// list, product resolve and read, and recommendations.

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/collectionapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/enrichapi"
)

func TestUnitSearchPassThrough_RelaysAndForwardsBearer(t *testing.T) {
	var gotBearer string
	enrich := &stubEnrichment{search: func(_ context.Context, bearer, typ, q string) (enrichmentclient.Result, error) {
		gotBearer = bearer
		if typ != "game" || q != "zelda" {
			t.Fatalf("params: %s %s", typ, q)
		}
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{"degraded":false,"results":[]}`)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/search?type=game&q=zelda")
	if rec.Code != 200 || rec.Body.String() != `{"degraded":false,"results":[]}` {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer == "" || gotBearer != env.sessionAccessToken {
		t.Fatalf("the session's access token must ride the proxied call, got %q", gotBearer)
	}
}

func TestUnitSearchPassThrough_UpstreamFailureIs502(t *testing.T) {
	enrich := &stubEnrichment{search: func(context.Context, string, string, string) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/search?type=game&q=zelda")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream failure: %d", rec.Code)
	}
}

// TestUnitFxRelay_SnapshotRelaysVerbatim pins that /api/fx passes the
// enrichment answer through byte-for-byte with the user's own token.
func TestUnitFxRelay_SnapshotRelaysVerbatim(t *testing.T) {
	const relayed = `{"base":"USD","date":"2026-07-01","rates":{"EUR":0.5,"JPY":150}}`
	var gotBearer string
	enrich := &stubEnrichment{fx: func(_ context.Context, bearer string) (enrichmentclient.Result, error) {
		gotBearer = bearer
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(relayed)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/fx")
	if rec.Code != 200 || rec.Body.String() != relayed {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer reaching enrichment: %q", gotBearer)
	}
}

// TestUnitFxRelay_UpstreamProblemRelaysVerbatim pins that a 502
// problem from enrichment (cold fx cache) reaches the browser with
// status, content type, and body intact.
func TestUnitFxRelay_UpstreamProblemRelaysVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Bad Gateway","status":502,"code":"upstream_unavailable","detail":"exchange rates are unavailable"}`
	enrich := &stubEnrichment{fx: func(context.Context, string) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 502, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/fx")
	if rec.Code != 502 || rec.Body.String() != problem {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type: %q", ct)
	}
}

func TestUnitFxRelay_ClientErrorAnswers502(t *testing.T) {
	enrich := &stubEnrichment{fx: func(context.Context, string) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/fx")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

// TestUnitSearchPassThrough_PcListingTypeRelaysVerbatim pins that
// type=pc_listing is not blocked by any bff-side enum check: the
// generated query binding treats type as an opaque string (enrichment
// owns validation), so it must reach the stub exactly as sent and the
// stub's body must round-trip to the client untouched.
func TestUnitSearchPassThrough_PcListingTypeRelaysVerbatim(t *testing.T) {
	const relayed = `{"degraded":false,"results":[{"type":"pc_listing","pc_product_id":5099,"name":"Super Mario Bros. (NES)"}]}`
	var gotType, gotQ string
	enrich := &stubEnrichment{search: func(_ context.Context, _, typ, q string) (enrichmentclient.Result, error) {
		gotType, gotQ = typ, q
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(relayed)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/search?type=pc_listing&q=mario")
	if gotType != "pc_listing" || gotQ != "mario" {
		t.Fatalf("params reaching enrichment: type=%q q=%q, want pc_listing/mario", gotType, gotQ)
	}
	if rec.Code != 200 || rec.Body.String() != relayed {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitResolvePassThrough_PcListingBodyRelaysVerbatim pins that a
// pc_listing resolve body reaches the enrichment stub byte-for-byte -
// the bff reads and forwards raw bytes, it never decodes into
// ResolveRequest - and the stub's answer round-trips to the client.
func TestUnitResolvePassThrough_PcListingBodyRelaysVerbatim(t *testing.T) {
	const sent = `{"type":"pc_listing","pc_product_id":5099}`
	const relayed = `{"id":"22222222-2222-2222-2222-222222222222","type":"pc_listing","pc_product_id":5099}`
	var gotBody []byte
	enrich := &stubEnrichment{resolve: func(_ context.Context, _ string, body []byte) (enrichmentclient.Result, error) {
		gotBody = body
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(relayed)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	req := httptest.NewRequest(http.MethodPost, "/api/products/resolve", strings.NewReader(sent))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if string(gotBody) != sent {
		t.Fatalf("body reaching enrichment: %s, want unmodified %s", gotBody, sent)
	}
	if rec.Code != 200 || rec.Body.String() != relayed {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnitProductPassThrough_RelaysProblemBody(t *testing.T) {
	enrich := &stubEnrichment{product: func(context.Context, string, uuid.UUID) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 404, ContentType: "application/problem+json",
			Body: []byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"product_not_found"}`)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/products/11111111-1111-1111-1111-111111111111")
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("problem relay: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestUnitRecommendations_ComposesAndCaches(t *testing.T) {
	scoreBody := []byte(`{"degraded":false,"recommendations":[{"igdb_game_id":9,"name":"Alundra","genres":["RPG"],"score":4.2}]}`)
	rating := 8
	var scoreCalls int
	var gotReq enrichapi.ScoreRequest
	col := &stubCollection{library: func(_ context.Context, bearer string) (collectionapi.LibrarySummary, error) {
		dropped := "dropped"
		return collectionapi.LibrarySummary{Library: []collectionapi.LibraryGame{
			{IgdbGameId: 1000, Rating: &rating},
			{IgdbGameId: 1001, Status: &dropped},
		}}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	h.enrichment = &stubEnrichment{score: func(_ context.Context, _ string, req enrichapi.ScoreRequest) ([]byte, bool, error) {
		scoreCalls++
		gotReq = req
		return scoreBody, false, nil
	}}

	rec := doAuthed(t, h, env, http.MethodGet, "/api/recommendations")
	if rec.Code != 200 || rec.Body.String() != string(scoreBody) {
		t.Fatalf("compose: %d %s", rec.Code, rec.Body.String())
	}
	// The library piped through unshaped.
	if len(gotReq.Library) != 2 || gotReq.Library[0].IgdbGameId != 1000 ||
		*gotReq.Library[0].Rating != 8 || *gotReq.Library[1].Status != "dropped" {
		t.Fatalf("score request: %+v", gotReq.Library)
	}
	// The second read is a cache hit: no second score call.
	rec = doAuthed(t, h, env, http.MethodGet, "/api/recommendations")
	if rec.Code != 200 || scoreCalls != 1 {
		t.Fatalf("cache hit: %d calls=%d", rec.Code, scoreCalls)
	}
}

func TestUnitRecommendations_DegradedIsNotCached(t *testing.T) {
	col := &stubCollection{library: func(context.Context, string) (collectionapi.LibrarySummary, error) {
		return collectionapi.LibrarySummary{Library: []collectionapi.LibraryGame{}}, nil
	}}
	var scoreCalls int
	h, env := newTestHandlersWithCollection(t, col)
	h.enrichment = &stubEnrichment{score: func(context.Context, string, enrichapi.ScoreRequest) ([]byte, bool, error) {
		scoreCalls++
		return []byte(`{"degraded":true,"recommendations":[]}`), true, nil
	}}
	doAuthed(t, h, env, http.MethodGet, "/api/recommendations")
	doAuthed(t, h, env, http.MethodGet, "/api/recommendations")
	if scoreCalls != 2 {
		t.Fatalf("a degraded score must not be cached (calls=%d)", scoreCalls)
	}
}

func TestUnitRecommendations_UpstreamFailures(t *testing.T) {
	t.Run("collection down", func(t *testing.T) {
		col := &stubCollection{library: func(context.Context, string) (collectionapi.LibrarySummary, error) {
			return collectionapi.LibrarySummary{}, collectionclient.ErrUpstream
		}}
		h, env := newTestHandlersWithCollection(t, col)
		if rec := doAuthed(t, h, env, http.MethodGet, "/api/recommendations"); rec.Code != http.StatusBadGateway {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("enrichment down", func(t *testing.T) {
		col := &stubCollection{library: func(context.Context, string) (collectionapi.LibrarySummary, error) {
			return collectionapi.LibrarySummary{Library: []collectionapi.LibraryGame{}}, nil
		}}
		h, env := newTestHandlersWithCollection(t, col)
		h.enrichment = &stubEnrichment{score: func(context.Context, string, enrichapi.ScoreRequest) ([]byte, bool, error) {
			return nil, false, enrichmentclient.ErrUpstream
		}}
		if rec := doAuthed(t, h, env, http.MethodGet, "/api/recommendations"); rec.Code != http.StatusBadGateway {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

func TestUnitListPlatforms_RelaysAndForwardsBearer(t *testing.T) {
	const body = `{"platforms":[{"igdb_id":19,"name":"Super Nintendo Entertainment System","aliases":["snes"]}]}`
	var gotBearer string
	enr := &stubEnrichment{listPlatforms: func(_ context.Context, bearer string) (enrichmentclient.Result, error) {
		gotBearer = bearer
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(body)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enr)

	rec := doAuthed(t, h, env, http.MethodGet, "/api/platforms")
	if rec.Code != 200 || rec.Body.String() != body {
		t.Fatalf("platforms relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}
	rec = doUnauthed(t, h, env, http.MethodGet, "/api/platforms")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}
