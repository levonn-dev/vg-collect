// Validator-path pins drive requests through the FULL bff stack (real
// router, stubbed upstreams), proving specval rejects bad bodies/params
// before any upstream hop. Valid-body relay is already proven by
// TestUnitCreateEntryPassThrough_OpenWorldRegionRoundTrips; no new case needed here.
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

// TestValidatorPath_CreateEntry_OversizeDeveloperName pins EntryCreate's
// developers[] maxLength(120): a violating body 400s invalid_body AT THE BFF, collection never called.
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

// TestValidatorPath_UpdateMe_HandleTooShort pins Handle's minLength(2)
// (common.yaml); the stub answers 200 for anything, so only specval blocks this body.
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

// TestValidatorPath_UpdateMe_AvatarUrlOversize pins avatar_url's
// maxLength(2048): an oversize URL 400s invalid_body AT THE BFF, user-service stub never called.
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

// TestValidatorPath_CreateEntry_MalformedJSON pins that non-JSON bodies
// 400 invalid_body too: specval's encoder maps every unparseable body to the same detail.
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

// TestValidatorPath_Explore_BadSortEnum pins GetExplore's sort enum (recent|top), enforced solely by specval.
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

// TestValidatorPath_FeedAndExplore_LimitOverMaxRejected guards against a
// handler-side clamp silently absorbing an over-max limit instead of specval's 400.
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
