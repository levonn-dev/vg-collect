// Tests for the admin maintenance levers: refresh, rematch,
// resnapshot, and normalization sweeps.

package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/enrichmentclient"
)

func TestUnitAdminRefresh_Relays202(t *testing.T) {
	const accepted = `{"status":"started"}`
	enrich := &stubEnrichment{triggerRefresh: func(_ context.Context, bearer string) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 202, ContentType: "application/json", Body: []byte(accepted)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodPost, "/api/admin/refresh")
	if rec.Code != 202 || rec.Body.String() != accepted {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitAdminRematch_Relays202 mirrors TestUnitAdminRefresh_Relays202
// for the entry-rematch trigger: collection's 202 relays verbatim.
func TestUnitAdminRematch_Relays202(t *testing.T) {
	const accepted = `{"status":"started"}`
	col := &stubCollection{triggerRematch: func(_ context.Context, bearer string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 202, ContentType: "application/json", Body: []byte(accepted)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	rec := doAuthed(t, h, env, http.MethodPost, "/api/admin/rematch")
	if rec.Code != 202 || rec.Body.String() != accepted {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitAdminRematch_ConflictRelaysVerbatim mirrors
// TestUnitAdminMapping_RelaysBodyAndConflict's 409-relays-verbatim
// shape for the rematch route: the live stack cannot exercise this
// path reliably (the dev rematch dataset resolves faster than two
// sequential HTTP calls, so a live double-trigger observes 202 twice,
// never 202-then-409 - see bruno/bff/admin/rematch.bru), so this is
// the checked-in proof that collection's rematch_in_progress problem
// reaches the browser unmodified.
func TestUnitAdminRematch_ConflictRelaysVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Conflict","status":409,"code":"rematch_in_progress","detail":"an entry rematch is already running"}`
	col := &stubCollection{triggerRematch: func(_ context.Context, bearer string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 409, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	rec := doAuthed(t, h, env, http.MethodPost, "/api/admin/rematch")
	if rec.Code != 409 || rec.Body.String() != problem {
		t.Fatalf("409 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitAdminNormalizePlatforms_RelaysAndForwardsBearer mirrors
// TestUnitAdminRematch_Relays202 for the platform-canonicalization
// lever: collection's 200 sweep summary relays verbatim, the caller's
// own bearer reaches collection, and no session is 401.
func TestUnitAdminNormalizePlatforms_RelaysAndForwardsBearer(t *testing.T) {
	const summary = `{"scanned":10,"normalized":3,"skipped":7}`
	var gotBearer string
	col := &stubCollection{normalizePlatforms: func(_ context.Context, bearer string) (collectionclient.Result, error) {
		gotBearer = bearer
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(summary)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)

	rec := doAuthed(t, h, env, http.MethodPost, "/api/admin/normalize-platforms")
	if rec.Code != 200 || rec.Body.String() != summary {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}

	rec = doUnauthed(t, h, env, http.MethodPost, "/api/admin/normalize-platforms")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

// TestUnitAdminNormalizeRegions_RelaysAndForwardsBearer mirrors the
// above for the entry-region normalization lever.
func TestUnitAdminNormalizeRegions_RelaysAndForwardsBearer(t *testing.T) {
	const summary = `{"scanned":5,"normalized":2,"skipped":3}`
	var gotBearer string
	col := &stubCollection{normalizeRegions: func(_ context.Context, bearer string) (collectionclient.Result, error) {
		gotBearer = bearer
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(summary)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)

	rec := doAuthed(t, h, env, http.MethodPost, "/api/admin/normalize-regions")
	if rec.Code != 200 || rec.Body.String() != summary {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}

	rec = doUnauthed(t, h, env, http.MethodPost, "/api/admin/normalize-regions")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

// TestUnitAdminResnapshot_RelaysAndForwardsBearer mirrors the above
// for the snapshot-field recompute lever.
func TestUnitAdminResnapshot_RelaysAndForwardsBearer(t *testing.T) {
	const summary = `{"products_seen":3,"products_failed":1,"entries_updated":2}`
	var gotBearer string
	col := &stubCollection{resnapshot: func(_ context.Context, bearer string) (collectionclient.Result, error) {
		gotBearer = bearer
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(summary)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)

	rec := doAuthed(t, h, env, http.MethodPost, "/api/admin/resnapshot")
	if rec.Code != 200 || rec.Body.String() != summary {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}

	rec = doUnauthed(t, h, env, http.MethodPost, "/api/admin/resnapshot")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

// TestUnitAdminNormalizeCommunityRegions_RelaysAndForwardsBearer
// mirrors the above for the community-product region normalization
// lever, which relays through enrichment instead of collection.
func TestUnitAdminNormalizeCommunityRegions_RelaysAndForwardsBearer(t *testing.T) {
	const summary = `{"scanned":4,"normalized":1,"skipped":3}`
	var gotBearer string
	enrich := &stubEnrichment{normalizeCommunityRegions: func(_ context.Context, bearer string) (enrichmentclient.Result, error) {
		gotBearer = bearer
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(summary)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)

	rec := doAuthed(t, h, env, http.MethodPost, "/api/admin/normalize-community-regions")
	if rec.Code != 200 || rec.Body.String() != summary {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}

	rec = doUnauthed(t, h, env, http.MethodPost, "/api/admin/normalize-community-regions")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}
