// Tests for product-identity administration: worklists, mapping
// corrections, community minting, and promotion.

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
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/enrichapi"
)

func TestUnitAdminWorklist_RelaysAndForwardsParams(t *testing.T) {
	const page = `{"products":[],"total_count":0}`
	var gotBearer string
	var gotParams *enrichapi.ListUnmatchedProductsParams
	enrich := &stubEnrichment{unmatchedProducts: func(_ context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error) {
		gotBearer, gotParams = bearer, params
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(page)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/unmatched?limit=5&offset=10")
	if rec.Code != 200 || rec.Body.String() != page {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}
	if gotParams == nil || gotParams.Limit == nil || *gotParams.Limit != 5 || gotParams.Offset == nil || *gotParams.Offset != 10 {
		t.Fatalf("params passthrough: %+v", gotParams)
	}
}

func TestUnitAdminWorklist_Forbidden403RelaysVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Forbidden","status":403,"code":"forbidden","detail":"role admin required"}`
	enrich := &stubEnrichment{unmatchedProducts: func(context.Context, string, *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 403, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/unmatched")
	if rec.Code != 403 || rec.Body.String() != problem {
		t.Fatalf("403 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type: %q", ct)
	}
}

func TestUnitCommunityWorklist_RelaysAndForwardsParams(t *testing.T) {
	const page = `{"products":[],"total_count":0}`
	var gotBearer string
	var gotParams *enrichapi.ListCommunityProductsParams
	enrich := &stubEnrichment{communityProducts: func(_ context.Context, bearer string, params *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error) {
		gotBearer, gotParams = bearer, params
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(page)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/community?limit=5&offset=10")
	if rec.Code != 200 || rec.Body.String() != page {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}
	if gotParams == nil || gotParams.Limit == nil || *gotParams.Limit != 5 || gotParams.Offset == nil || *gotParams.Offset != 10 {
		t.Fatalf("params passthrough: %+v", gotParams)
	}
}

func TestUnitCommunityWorklist_Forbidden403RelaysVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Forbidden","status":403,"code":"forbidden","detail":"role admin required"}`
	enrich := &stubEnrichment{communityProducts: func(context.Context, string, *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 403, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/community")
	if rec.Code != 403 || rec.Body.String() != problem {
		t.Fatalf("403 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type: %q", ct)
	}
}

func TestUnitCommunityWorklist_ClientErrorAnswers502(t *testing.T) {
	enrich := &stubEnrichment{communityProducts: func(context.Context, string, *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/community")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

func TestUnitAdminMapping_RelaysBodyAndConflict(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Conflict","status":409,"code":"identity_taken","detail":"another product with the same identity already carries that listing"}`
	id := uuid.New()
	var gotBody []byte
	enrich := &stubEnrichment{setProductMapping: func(_ context.Context, _ string, gotID uuid.UUID, body []byte) (enrichmentclient.Result, error) {
		if gotID != id {
			t.Errorf("id = %v", gotID)
		}
		gotBody = body
		return enrichmentclient.Result{Status: 409, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	r := httptest.NewRequest(http.MethodPut, "/api/admin/products/"+id.String()+"/pricecharting", strings.NewReader(`{"pc_product_id":5005}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(env.cookie)
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != 409 || rec.Body.String() != problem {
		t.Fatalf("409 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if string(gotBody) != `{"pc_product_id":5005}` {
		t.Fatalf("body passthrough: %s", gotBody)
	}
}

func TestUnitAdminDelete_ReferencedAnswers409BeforeEnrichment(t *testing.T) {
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{"entry_count":3}`)}, nil
	}}
	// deleteProduct stays nil: reaching enrichment would panic, which
	// is the ordering assertion.
	h, env := newTestHandlersWithEnrichment(t, &stubEnrichment{})
	h.collection = col
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/admin/products/"+uuid.NewString(), nil)
	r.AddCookie(env.cookie)
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("referenced delete: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "product_referenced") || !strings.Contains(rec.Body.String(), "3 entries") {
		t.Fatalf("problem must carry the code and count: %s", rec.Body.String())
	}
}

func TestUnitAdminDelete_UnreferencedRelaysEnrichment(t *testing.T) {
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{"entry_count":0}`)}, nil
	}}
	var gotBearer string
	enrich := &stubEnrichment{deleteProduct: func(_ context.Context, bearer string, _ uuid.UUID) (enrichmentclient.Result, error) {
		gotBearer = bearer
		return enrichmentclient.Result{Status: http.StatusNoContent}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	h.collection = col
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/admin/products/"+uuid.NewString(), nil)
	r.AddCookie(env.cookie)
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unreferenced delete: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer reaching enrichment: %q", gotBearer)
	}
}

func TestUnitAdminDelete_Collection403RelaysVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Forbidden","status":403,"code":"forbidden","detail":"role admin required"}`
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 403, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, &stubEnrichment{})
	h.collection = col
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/admin/products/"+uuid.NewString(), nil)
	r.AddCookie(env.cookie)
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != 403 || rec.Body.String() != problem {
		t.Fatalf("collection 403 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnitAdminDelete_NoSession401(t *testing.T) {
	h, env := newTestHandlersWithEnrichment(t, &stubEnrichment{})
	rec := doUnauthed(t, h, env, http.MethodDelete, "/api/admin/products/"+uuid.NewString())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

// TestUnitPromoteRelays_ParamsAndConflict proves the candidates read
// forwards its query params (limit/offset/product_id) and the promote
// mutation relays a conflict (a provider twin already holds the
// identity) verbatim.
func TestUnitPromoteRelays_ParamsAndConflict(t *testing.T) {
	var gotParams *enrichapi.ListPromoteCandidatesParams
	enrich := &stubEnrichment{
		promoteCandidates: func(_ context.Context, _ string, params *enrichapi.ListPromoteCandidatesParams) (enrichmentclient.Result, error) {
			gotParams = params
			return enrichmentclient.Result{Status: 200, ContentType: "application/json",
				Body: []byte(`{"products":[],"total_count":0}`)}, nil
		},
		promoteProduct: func(context.Context, string, uuid.UUID, []byte) (enrichmentclient.Result, error) {
			return enrichmentclient.Result{Status: 409, ContentType: "application/problem+json",
				Body: []byte(`{"type":"about:blank","title":"Conflict","status":409,"code":"identity_taken"}`)}, nil
		},
	}
	h, env := newTestHandlersWithEnrichment(t, enrich)

	pid := uuid.NewString()
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/promote-candidates?limit=5&offset=10&product_id="+pid)
	if rec.Code != 200 {
		t.Fatalf("candidates relay: %d", rec.Code)
	}
	if gotParams == nil || gotParams.Limit == nil || *gotParams.Limit != 5 || gotParams.ProductId == nil {
		t.Fatalf("params passthrough: %+v", gotParams)
	}

	rec = doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products/"+pid+"/promote", `{"igdb_game_id":1011,"platform_igdb_id":19}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "identity_taken") {
		t.Fatalf("promote conflict relay: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitCreateCommunityProduct_RelaysBodyAndForbidden proves the
// admin mint forwards the browser's body untouched and relays
// enrichment's role refusal verbatim (enrichment enforces admin, the
// bff holds no role logic of its own on admin routes).
func TestUnitCreateCommunityProduct_RelaysBodyAndForbidden(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Forbidden","status":403,"code":"forbidden","detail":"role admin required"}`
	var gotBody []byte
	enrich := &stubEnrichment{createCommunityProduct: func(_ context.Context, _ string, body []byte) (enrichmentclient.Result, error) {
		gotBody = body
		return enrichmentclient.Result{Status: 403, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	payload := `{"name":"Homebrew Cart","type":"game"}`
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products", payload)
	if rec.Code != 403 || rec.Body.String() != problem {
		t.Fatalf("403 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if string(gotBody) != payload {
		t.Fatalf("body passthrough: %s", gotBody)
	}
}

// TestUnitDismissPromoteCandidate_RelaysBodyAndNotFound proves the
// candidate dismissal forwards the target id and the browser's body
// untouched, and relays a not-found verbatim (the candidate left the
// sweep worklist between page load and dismiss).
func TestUnitDismissPromoteCandidate_RelaysBodyAndNotFound(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Not Found","status":404,"code":"not_found"}`
	pid := uuid.New()
	var gotID uuid.UUID
	var gotBody []byte
	enrich := &stubEnrichment{dismissPromoteCandidate: func(_ context.Context, _ string, id uuid.UUID, body []byte) (enrichmentclient.Result, error) {
		gotID, gotBody = id, body
		return enrichmentclient.Result{Status: 404, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	payload := `{"provider":"pricecharting","provider_id":5005}`
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products/"+pid.String()+"/promote-candidates/dismiss", payload)
	if rec.Code != 404 || rec.Body.String() != problem {
		t.Fatalf("404 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if gotID != pid || string(gotBody) != payload {
		t.Fatalf("id/body passthrough: id=%s body=%s", gotID, gotBody)
	}
}

func TestUnitCreateCommunityProduct_ClientErrorAnswers502(t *testing.T) {
	enrich := &stubEnrichment{createCommunityProduct: func(context.Context, string, []byte) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products", `{"name":"Homebrew Cart","type":"game"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

func TestUnitDismissPromoteCandidate_ClientErrorAnswers502(t *testing.T) {
	enrich := &stubEnrichment{dismissPromoteCandidate: func(context.Context, string, uuid.UUID, []byte) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products/"+uuid.NewString()+"/promote-candidates/dismiss", `{"provider":"pricecharting","provider_id":5005}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}
