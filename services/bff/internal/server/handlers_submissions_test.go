// Tests for catalog-candidate submissions and the admin verdict queue.

package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/collectionapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
)

// TestUnitSubmissionRelays_FidelityAndNoSession covers the three user
// submission ops: a create relays the 201 body verbatim and forwards
// the session's own bearer, a read relays a problem body verbatim,
// and a mutation with no session answers 401 before the handler runs.
func TestUnitSubmissionRelays_FidelityAndNoSession(t *testing.T) {
	const sub = `{"id":"s1","entry_id":"e1","status":"pending","created_at":"2026-07-17T00:00:00Z","updated_at":"2026-07-17T00:00:00Z"}`
	var gotBearer string
	coll := &stubCollection{
		createSubmission: func(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
			gotBearer = bearer
			return collectionclient.Result{Status: 201, ContentType: "application/json", Body: []byte(sub)}, nil
		},
		getSubmission: func(context.Context, string, uuid.UUID) (collectionclient.Result, error) {
			return collectionclient.Result{Status: 404, ContentType: "application/problem+json",
				Body: []byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"submission_not_found"}`)}, nil
		},
	}
	h, env := newTestHandlersWithCollection(t, coll)
	entry := uuid.NewString()

	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/entries/"+entry+"/submission", "")
	if rec.Code != 201 || rec.Body.String() != sub {
		t.Fatalf("create relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}

	rec = doAuthed(t, h, env, http.MethodGet, "/api/entries/"+entry+"/submission")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "submission_not_found") {
		t.Fatalf("problem relay: %d %s", rec.Code, rec.Body.String())
	}

	rec = doUnauthed(t, h, env, http.MethodPost, "/api/entries/"+entry+"/submission")
	if rec.Code != 401 {
		t.Fatalf("no session: %d", rec.Code)
	}
}

// TestUnitVerdictRelay_BodyPassthroughAnd409 proves the admin verdict
// forwards the browser's body untouched and relays a conflict
// (another admin already resolved the row) verbatim.
func TestUnitVerdictRelay_BodyPassthroughAnd409(t *testing.T) {
	var gotBody []byte
	coll := &stubCollection{submitVerdict: func(_ context.Context, _ string, _ uuid.UUID, body []byte) (collectionclient.Result, error) {
		gotBody = body
		return collectionclient.Result{Status: 409, ContentType: "application/problem+json",
			Body: []byte(`{"type":"about:blank","title":"Conflict","status":409,"code":"submission_resolved"}`)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, coll)

	payload := `{"action":"reject","reason":"not shared"}`
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/submissions/"+uuid.NewString()+"/verdict", payload)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "submission_resolved") {
		t.Fatalf("verdict relay: %d %s", rec.Code, rec.Body.String())
	}
	if string(gotBody) != payload {
		t.Fatalf("body must pass through untouched: %s", gotBody)
	}
}

// TestUnitCancelSubmission_RelaysAndForwardsBearer proves the pending-
// submission cancel forwards the session's own bearer and relays the
// upstream's answer verbatim; a request with no session never reaches
// the handler.
func TestUnitCancelSubmission_RelaysAndForwardsBearer(t *testing.T) {
	var gotBearer string
	coll := &stubCollection{cancelSubmission: func(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
		gotBearer = bearer
		return collectionclient.Result{Status: http.StatusNoContent}, nil
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	entry := uuid.NewString()

	rec := doAuthed(t, h, env, http.MethodDelete, "/api/entries/"+entry+"/submission")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}

	rec = doUnauthed(t, h, env, http.MethodDelete, "/api/entries/"+entry+"/submission")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

// TestUnitListSubmissions_RelaysAndForwardsParams proves the admin
// queue read forwards its query params (limit/offset) and relays the
// upstream body verbatim; collection enforces the role, so the bff
// holds no gate of its own here beyond the session.
func TestUnitListSubmissions_RelaysAndForwardsParams(t *testing.T) {
	const page = `{"submissions":[],"total_count":0}`
	var gotBearer string
	var gotParams *collectionapi.ListSubmissionsParams
	coll := &stubCollection{listSubmissions: func(_ context.Context, bearer string, params *collectionapi.ListSubmissionsParams) (collectionclient.Result, error) {
		gotBearer, gotParams = bearer, params
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(page)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/submissions?limit=5&offset=10")
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

// The four tests below mirror TestUnitFxRelay_ClientErrorAnswers502: each
// covers one new handler's own upstream-failure branch (a dead client,
// not an upstream answer) which no other test above happens to exercise.

func TestUnitCancelSubmission_ClientErrorAnswers502(t *testing.T) {
	coll := &stubCollection{cancelSubmission: func(context.Context, string, uuid.UUID) (collectionclient.Result, error) {
		return collectionclient.Result{}, collectionclient.ErrUpstream
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	rec := doAuthed(t, h, env, http.MethodDelete, "/api/entries/"+uuid.NewString()+"/submission")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

func TestUnitListSubmissions_ClientErrorAnswers502(t *testing.T) {
	coll := &stubCollection{listSubmissions: func(context.Context, string, *collectionapi.ListSubmissionsParams) (collectionclient.Result, error) {
		return collectionclient.Result{}, collectionclient.ErrUpstream
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/submissions")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

func TestUnitAckSubmission_RelaysAndForwardsBearer(t *testing.T) {
	var gotBearer string
	coll := &stubCollection{ackSubmission: func(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
		gotBearer = bearer
		return collectionclient.Result{Status: http.StatusNoContent}, nil
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	entry := uuid.NewString()

	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/entries/"+entry+"/submission/ack", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ack relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}
	rec = doUnauthed(t, h, env, http.MethodPost, "/api/entries/"+entry+"/submission/ack")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}
