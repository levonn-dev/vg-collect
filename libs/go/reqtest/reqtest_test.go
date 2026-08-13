package reqtest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
)

// runFatal runs f against a standalone *testing.T with no parent, so
// f's own t.Fatal cannot fail the caller's test - (*testing.T).Fail
// walks up the parent chain unconditionally, so a t.Run subtest is not
// enough to contain it. f runs in its own goroutine because Fatal's
// runtime.Goexit only unwinds the goroutine that called it.
func runFatal(f func(t *testing.T)) bool {
	sub := &testing.T{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(sub)
	}()
	<-done
	return sub.Failed()
}

// TestWaitFor_ReturnsAsSoonAsCheckPasses pins the poll-until-true
// contract: WaitFor must not block past the first passing check, and
// must not treat earlier failing checks as fatal.
func TestWaitFor_ReturnsAsSoonAsCheckPasses(t *testing.T) {
	calls := 0
	reqtest.WaitFor(t, time.Second, func() bool {
		calls++
		return calls >= 3
	})
	if calls != 3 {
		t.Fatalf("calls = %d, want exactly 3 (stop polling the instant check passes)", calls)
	}
}

// TestWaitFor_FailsTestWhenTimeoutElapses is the negative control: a
// check that never passes must fail the test once timeout elapses,
// not hang or silently return.
func TestWaitFor_FailsTestWhenTimeoutElapses(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		reqtest.WaitFor(st, 60*time.Millisecond, func() bool { return false })
	}) {
		t.Fatal("want WaitFor to fail the test when check never passes before timeout")
	}
}

// spyBody wraps a Reader and records whether Close was called, so a
// test can pin the body-closing contract that made item 21 pick
// enrichment's decodeBody over auth's non-closing decode.
type spyBody struct {
	io.Reader
	closed bool
}

func (s *spyBody) Close() error {
	s.closed = true
	return nil
}

type namedThing struct {
	Name string `json:"name"`
}

// TestDecodeJSON_DecodesIntoTheGivenType drives the whole point of
// the generic: an arbitrary caller-chosen type decodes correctly.
func TestDecodeJSON_DecodesIntoTheGivenType(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"name":"chrono trigger"}`))}
	got := reqtest.DecodeJSON[namedThing](t, resp)
	if got.Name != "chrono trigger" {
		t.Fatalf("got = %+v", got)
	}
}

// TestDecodeJSON_ClosesBody pins the trait item 21 explicitly chose:
// the body-closing variant, not auth's original leaky one.
func TestDecodeJSON_ClosesBody(t *testing.T) {
	body := &spyBody{Reader: strings.NewReader(`{}`)}
	resp := &http.Response{Body: body}
	reqtest.DecodeJSON[namedThing](t, resp)
	if !body.closed {
		t.Fatal("want DecodeJSON to close the response body")
	}
}

// TestDecodeJSON_FailsTestOnMalformedBody is the negative control:
// undecodable JSON must fail the test, not panic or zero-value silently.
func TestDecodeJSON_FailsTestOnMalformedBody(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{not json`))}
		reqtest.DecodeJSON[namedThing](st, resp)
	}) {
		t.Fatal("want DecodeJSON to fail the test on malformed JSON")
	}
}

// TestNewJSONRequest_NilBodyOmitsBodyAndContentType covers the
// bodyless GET/DELETE shape (auth's authReq, social/user's do(...,
// nil)): no body to read and no Content-Type announcing one.
func TestNewJSONRequest_NilBodyOmitsBodyAndContentType(t *testing.T) {
	req := reqtest.NewJSONRequest(t, http.MethodGet, "/entries", "", nil)
	if req.Method != http.MethodGet || req.URL.Path != "/entries" {
		t.Fatalf("method=%s path=%s", req.Method, req.URL.Path)
	}
	if req.Body != nil && req.Body != http.NoBody {
		b, _ := io.ReadAll(req.Body)
		if len(b) != 0 {
			t.Fatalf("body = %q, want empty", b)
		}
	}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		t.Fatalf("Content-Type = %q, want unset for a nil body", ct)
	}
	if auth := req.Header.Get("Authorization"); auth != "" {
		t.Fatalf("Authorization = %q, want unset for an empty bearer", auth)
	}
}

// TestNewJSONRequest_EmptyBearerOmitsAuthorizationHeader pins the
// "optional" half of the bearer contract: an empty string must not
// produce a header at all (the shape every do/serveUnit helper relies
// on for its unauthenticated-request tests).
func TestNewJSONRequest_EmptyBearerOmitsAuthorizationHeader(t *testing.T) {
	req := reqtest.NewJSONRequest(t, http.MethodGet, "/entries", "", nil)
	if _, ok := req.Header["Authorization"]; ok {
		t.Fatal("want no Authorization header at all for an empty bearer")
	}
}

// TestNewJSONRequest_NonEmptyBearerSetsAuthorizationHeader covers the
// other half: a non-empty bearer becomes a standard Bearer header.
func TestNewJSONRequest_NonEmptyBearerSetsAuthorizationHeader(t *testing.T) {
	req := reqtest.NewJSONRequest(t, http.MethodGet, "/entries", "tok-123", nil)
	if got := req.Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer tok-123")
	}
}

// TestNewJSONRequest_StructBodyIsJSONMarshaledWithContentType covers
// the common case every service's builder shares: a Go value becomes
// a JSON body, and the request announces it.
func TestNewJSONRequest_StructBodyIsJSONMarshaledWithContentType(t *testing.T) {
	req := reqtest.NewJSONRequest(t, http.MethodPost, "/entries", "", map[string]any{"title": "Chrono Trigger"})
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"title":"Chrono Trigger"}`+"\n" && string(b) != `{"title":"Chrono Trigger"}` {
		t.Fatalf("body = %s", b)
	}
}

// TestNewJSONRequest_StringBodyIsWrittenRaw pins the deliberate
// malformed-JSON escape hatch auth's jsonReq and user's do both rely
// on: a string body is written verbatim, not JSON-marshaled (which
// would wrap it in quotes), so a caller can hand deliberately invalid
// JSON to drive a decode-error test.
func TestNewJSONRequest_StringBodyIsWrittenRaw(t *testing.T) {
	req := reqtest.NewJSONRequest(t, http.MethodPost, "/entries", "", "{not json")
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{not json" {
		t.Fatalf("body = %q, want the raw string unquoted", b)
	}
}

// TestNewJSONRequest_ReaderBodyPassesThroughUnencoded pins collection's
// do(...) shape: a caller that already has an io.Reader (jsonBody's
// *bytes.Reader, a strings.Reader for a raw payload) must have it used
// verbatim, not re-JSON-encoded as if it were an opaque struct.
func TestNewJSONRequest_ReaderBodyPassesThroughUnencoded(t *testing.T) {
	req := reqtest.NewJSONRequest(t, http.MethodPost, "/entries", "", strings.NewReader(`{"raw":true}`))
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"raw":true}` {
		t.Fatalf("body = %s", b)
	}
}

// TestNewJSONRequest_UsableAgainstARealListener pins the requirement
// that made auth's integration-style helpers (post, postAuth, authReq)
// adopt this over httptest.NewRequest: the result must be sendable
// through a real http.Client, not just through a Handler's ServeHTTP.
// httptest.NewRequest sets RequestURI, which http.Client.Do rejects
// outright ("RequestURI can't be set in client requests") - a real
// integration-test regression this pins against recurring.
func TestNewJSONRequest_UsableAgainstARealListener(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	req := reqtest.NewJSONRequest(t, http.MethodPost, srv.URL+"/x", "tok", map[string]string{"a": "b"})
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // test helper; url is always from httptest.NewServer
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// TestNewJSONRequest_FailsTestWhenRequestIsMalformed covers
// http.NewRequest's own error return (an invalid method token), the
// other way NewJSONRequest can fail besides a bad body.
func TestNewJSONRequest_FailsTestWhenRequestIsMalformed(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		reqtest.NewJSONRequest(st, "BAD METHOD", "/entries", "", nil)
	}) {
		t.Fatal("want NewJSONRequest to fail the test when the method is invalid")
	}
}

// TestNewJSONRequest_FailsTestWhenBodyCannotMarshal is the negative
// control on the default JSON-marshal branch.
func TestNewJSONRequest_FailsTestWhenBodyCannotMarshal(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		reqtest.NewJSONRequest(st, http.MethodPost, "/entries", "", make(chan int))
	}) {
		t.Fatal("want NewJSONRequest to fail the test when the body cannot be JSON-marshaled")
	}
}

// problemResponse builds an *http.Response carrying status, the given
// content type, and a problem+json-shaped body.
func problemResponse(status int, contentType, code string) *http.Response {
	body := `{"status":` + strconv.Itoa(status) + `,"code":"` + code + `","detail":"d","revoke_jtis":["j1","j2"]}`
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestAssertProblem_HappyPathReturnsDecodedBody drives the whole
// point: status, content type and code all match, and every field of
// the problem body comes back decoded - including revoke_jtis, the
// auth-specific extension a caller like the refresh-reuse tests reads
// off the return value.
func TestAssertProblem_HappyPathReturnsDecodedBody(t *testing.T) {
	resp := problemResponse(http.StatusConflict, "application/problem+json", "refresh_reused")
	p := reqtest.AssertProblem(t, resp, http.StatusConflict, "refresh_reused")
	if p.Status != http.StatusConflict || p.Code != "refresh_reused" || p.Detail != "d" {
		t.Fatalf("p = %+v", p)
	}
	if len(p.RevokeJTIs) != 2 || p.RevokeJTIs[0] != "j1" {
		t.Fatalf("RevokeJTIs = %v", p.RevokeJTIs)
	}
}

// TestAssertProblem_FailsOnStatusMismatch is the negative control on
// the first of the three checks.
func TestAssertProblem_FailsOnStatusMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		resp := problemResponse(http.StatusBadRequest, "application/problem+json", "invalid_body")
		reqtest.AssertProblem(st, resp, http.StatusNotFound, "invalid_body")
	}) {
		t.Fatal("want AssertProblem to fail the test on a status mismatch")
	}
}

// TestAssertProblem_FailsOnContentTypeMismatch is the negative control
// on the second check: a 200 OK with a matching status but a plain
// application/json body is not a problem response.
func TestAssertProblem_FailsOnContentTypeMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		resp := problemResponse(http.StatusBadRequest, "application/json", "invalid_body")
		reqtest.AssertProblem(st, resp, http.StatusBadRequest, "invalid_body")
	}) {
		t.Fatal("want AssertProblem to fail the test on a content-type mismatch")
	}
}

// TestAssertProblem_FailsOnCodeMismatch is the negative control on the
// third check.
func TestAssertProblem_FailsOnCodeMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		resp := problemResponse(http.StatusBadRequest, "application/problem+json", "invalid_body")
		reqtest.AssertProblem(st, resp, http.StatusBadRequest, "something_else")
	}) {
		t.Fatal("want AssertProblem to fail the test on a code mismatch")
	}
}

// problemRecorder builds an httptest.ResponseRecorder carrying status,
// the given content type, and a problem+json-shaped body.
func problemRecorder(status int, contentType, code string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", contentType)
	rec.WriteHeader(status)
	_, _ = rec.WriteString(`{"status":` + strconv.Itoa(status) + `,"code":"` + code + `","detail":"d"}`)
	return rec
}

// TestAssertProblemRec_HappyPathReturnsDecodedBody is AssertProblem's
// recorder twin, same contract against a *httptest.ResponseRecorder
// instead of a real *http.Response.
func TestAssertProblemRec_HappyPathReturnsDecodedBody(t *testing.T) {
	rec := problemRecorder(http.StatusForbidden, "application/problem+json", "forbidden")
	p := reqtest.AssertProblemRec(t, rec, http.StatusForbidden, "forbidden")
	if p.Status != http.StatusForbidden || p.Code != "forbidden" || p.Detail != "d" {
		t.Fatalf("p = %+v", p)
	}
}

// TestAssertProblemRec_FailsOnStatusMismatch is the negative control
// on the first check.
func TestAssertProblemRec_FailsOnStatusMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		rec := problemRecorder(http.StatusInternalServerError, "application/problem+json", "internal")
		reqtest.AssertProblemRec(st, rec, http.StatusBadGateway, "internal")
	}) {
		t.Fatal("want AssertProblemRec to fail the test on a status mismatch")
	}
}

// TestAssertProblemRec_FailsOnContentTypeMismatch is the negative
// control on the second check.
func TestAssertProblemRec_FailsOnContentTypeMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		rec := problemRecorder(http.StatusInternalServerError, "application/json", "internal")
		reqtest.AssertProblemRec(st, rec, http.StatusInternalServerError, "internal")
	}) {
		t.Fatal("want AssertProblemRec to fail the test on a content-type mismatch")
	}
}

// TestAssertProblemRec_FailsOnCodeMismatch is the negative control on
// the third check.
func TestAssertProblemRec_FailsOnCodeMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		rec := problemRecorder(http.StatusInternalServerError, "application/problem+json", "internal")
		reqtest.AssertProblemRec(st, rec, http.StatusInternalServerError, "not_internal")
	}) {
		t.Fatal("want AssertProblemRec to fail the test on a code mismatch")
	}
}

// TestAssertProblemRec_FailsOnMalformedBody covers the recorder
// variant's own decode step (a *bytes.Buffer already in memory, so it
// unmarshals directly instead of going through DecodeJSON): a body
// that is not valid JSON at all - a proxied error page landing on a
// problem+json-labeled response - must fail the test, not panic.
func TestAssertProblemRec_FailsOnMalformedBody(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/problem+json")
		rec.WriteHeader(http.StatusInternalServerError)
		_, _ = rec.WriteString("<html>not json</html>")
		reqtest.AssertProblemRec(st, rec, http.StatusInternalServerError, "internal")
	}) {
		t.Fatal("want AssertProblemRec to fail the test when the body is not valid JSON")
	}
}

// stubClient is a minimal stand-in for a generated xxxclient.Client:
// just the base URL a real constructor like userclient.New(baseURL)
// would capture.
type stubClient struct{ baseURL string }

// TestNewTestClient_ConstructsAgainstTheBootedServer drives the whole
// point: T's constructor gets handed the URL of a live server serving
// h, matching every xxxclient.New(srv.URL) call site this replaces.
func TestNewTestClient_ConstructsAgainstTheBootedServer(t *testing.T) {
	var gotPath string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	c := reqtest.NewTestClient(t, h, func(baseURL string) *stubClient {
		return &stubClient{baseURL: baseURL}
	})
	if c.baseURL == "" {
		t.Fatal("want construct to receive a non-empty base URL")
	}
	resp, err := http.Get(c.baseURL + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || gotPath != "/ping" {
		t.Fatalf("status=%d path=%s, want the constructed client's URL to reach h", resp.StatusCode, gotPath)
	}
}

// TestNewTestClient_ClosesServerOnCleanup pins the other half of the
// "-construct-cleanup" contract every converted site relied on: the
// server must not outlive the test.
func TestNewTestClient_ClosesServerOnCleanup(t *testing.T) {
	var url string
	t.Run("inner", func(st *testing.T) {
		c := reqtest.NewTestClient(st, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}), func(baseURL string) *stubClient {
			return &stubClient{baseURL: baseURL}
		})
		url = c.baseURL
	})

	if _, err := http.Get(url); err == nil { //nolint:gosec // test helper; url is always from httptest.NewServer
		t.Fatal("want the server to be closed once the test that booted it ends")
	}
}
