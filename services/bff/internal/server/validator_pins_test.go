// Validator-path pins: each case drives a request through the FULL
// bff stack (real router, stubbed upstreams, no hand-faked wiring).
// bff was otherwise a pure relay before specval wired in - it never
// checked a request body's shape on its own - so
// TestValidatorPath_CreateEntry_OversizeDeveloperName,
// _MalformedJSON, and TestValidatorPath_UpdateMe_HandleTooShort were
// all red before wiring: the stub upstream answered success for
// whatever it was handed, so every one of those bodies used to relay
// straight through untouched. The query-enum case
// (TestValidatorPath_Explore_BadSortEnum) was the exception: it was
// green immediately, enforced by GetExplore's own hand check during
// the double-validation window before that check was removed, and
// stays green now that specval alone owns it. Valid bodies relaying
// byte-for-byte (pin e) is proven by re-running
// TestUnitCreateEntryPassThrough_OpenWorldRegionRoundTrips in
// handlers_collection_test.go unmodified - already a fully valid
// EntryCreate body, so it never depended on the absence of
// validation and needs no new case here.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
)

// TestValidatorPath_CreateEntry_OversizeDeveloperName pins the
// EntryCreate developers[] item maxLength(120) contract cap on the
// /api/entries relay: a violating body 400s invalid_body AT THE BFF,
// and the collection stub is never called - proving the bff's own
// copy of the contract catches the bad body before any hop upstream,
// not merely that collection would have rejected it too.
func TestValidatorPath_CreateEntry_OversizeDeveloperName(t *testing.T) {
	var called int
	col := &captureCollection{stubCollection: &stubCollection{}, onCreateEntry: func([]byte) { called++ }}
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.collection = col
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	body := `{"region":"ntsc_u","packaging":"loose","developers":["` + strings.Repeat("a", 121) + `"]}`
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/entries", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body)
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "invalid_body" {
		t.Fatalf("problem = %+v, err = %v, want invalid_body", p, err)
	}
	if called != 0 {
		t.Fatalf("collection.CreateEntry called %d times, want 0 (bff must reject before the upstream hop)", called)
	}
}

// TestValidatorPath_UpdateMe_HandleTooShort pins UpdateMeRequest's
// restored Handle contract (common.yaml's Handle: minLength 2):
// handle "x" is one rune short. The stub below answers 200 for
// whatever body it is handed, so before specval wired in this case
// was genuinely red - unlike a hand-check reversal, nothing stood
// between this body and the upstream relay until specval did.
func TestValidatorPath_UpdateMe_HandleTooShort(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	uid := uuid.New()
	userJSON := []byte(`{"id":"` + uid.String() + `","email":"alice@example.test","handle":"x","roles":["user"],` +
		`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`)
	h.users = &stubUsers{update: func(context.Context, string, string, []byte) (userclient.Result, error) {
		return userclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: userJSON}, nil
	}}
	access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	rec := doAuthedBody(t, h, env, http.MethodPatch, "/api/me", `{"handle":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body)
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "invalid_body" {
		t.Fatalf("problem = %+v, err = %v, want invalid_body", p, err)
	}
}

// TestValidatorPath_UpdateMe_AvatarUrlOversize pins
// UpdateMeRequest's avatar_url maxLength(2048) contract cap on the
// /api/me relay: an oversize URL 400s invalid_body AT THE BFF, and
// the user-service stub is never called - proving the bff's own copy
// of the contract catches the bad body before any hop upstream.
func TestValidatorPath_UpdateMe_AvatarUrlOversize(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	uid := uuid.New()
	var called int
	h.users = &stubUsers{update: func(context.Context, string, string, []byte) (userclient.Result, error) {
		called++
		return userclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: []byte(`{}`)}, nil
	}}
	access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	body := `{"avatar_url":"https://x.example/` + strings.Repeat("a", 2049) + `"}`
	rec := doAuthedBody(t, h, env, http.MethodPatch, "/api/me", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body)
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "invalid_body" {
		t.Fatalf("problem = %+v, err = %v, want invalid_body", p, err)
	}
	if called != 0 {
		t.Fatalf("users.Update called %d times, want 0 (bff must reject before the upstream hop)", called)
	}
}

// TestValidatorPath_CreateEntry_MalformedJSON pins that a body which
// is not valid JSON at all 400s invalid_body - the same house code a
// schema violation gets, since specval's encoder maps every
// unparseable body to the same detail regardless of cause.
func TestValidatorPath_CreateEntry_MalformedJSON(t *testing.T) {
	h, env := newTestHandlersWithCollection(t, &stubCollection{})
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/entries", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body)
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "invalid_body" {
		t.Fatalf("problem = %+v, err = %v, want invalid_body", p, err)
	}
}

// TestValidatorPath_Explore_BadSortEnum pins GetExplore's sort enum
// contract (recent|top) on a social list relay. Unlike the body pins
// above, this passed via GetExplore's own hand check during the
// double-validation window before that check was removed, and stays
// green now that specval alone enforces it.
func TestValidatorPath_Explore_BadSortEnum(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	rec := doAuthed(t, h, env, http.MethodGet, "/api/explore?sort=bogus")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body)
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "invalid_param" {
		t.Fatalf("problem = %+v, err = %v, want invalid_param", p, err)
	}
}

// TestValidatorPath_FeedAndExplore_LimitOverMaxRejected is the
// deliberate clamp reversal that removing GetFeed/GetExplore's ceiling clamp
// (limit = min(limit, ...)) produces: before that removal, an
// over-max limit was silently pulled back down to the page cap
// rather than rejected, even with specval already wired (the clamp
// ran inside the handler, after specval had already let a
// schema-valid-shaped integer through - specval's own maximum on
// limit is what turns this into a 400 once the handler stops
// second-guessing it). Both routes reject the same way now that the
// ceiling clamp is gone.
func TestValidatorPath_FeedAndExplore_LimitOverMaxRejected(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	for _, path := range []string{
		"/api/feed?tab=following&limit=999",
		"/api/explore?sort=recent&limit=999",
	} {
		rec := doAuthed(t, h, env, http.MethodGet, path)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400, body = %s", path, rec.Code, rec.Body)
		}
		var p struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "invalid_param" {
			t.Fatalf("%s: problem = %+v, err = %v, want invalid_param", path, p, err)
		}
	}
}
