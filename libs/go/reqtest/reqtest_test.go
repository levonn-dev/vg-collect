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

// runFatal runs f against a standalone *testing.T in its own goroutine.
// A t.Run subtest would still propagate Fail to the caller; this doesn't.
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

// TestWaitFor_ReturnsAsSoonAsCheckPasses pins that WaitFor stops polling on the first passing check.
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

// TestWaitFor_FailsTestWhenTimeoutElapses is the negative control: a check that never passes fails the test.
func TestWaitFor_FailsTestWhenTimeoutElapses(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		reqtest.WaitFor(st, 60*time.Millisecond, func() bool { return false })
	}) {
		t.Fatal("want WaitFor to fail the test when check never passes before timeout")
	}
}

// spyBody wraps a Reader and records whether Close was called.
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

// TestDecodeJSON_DecodesIntoTheGivenType decodes into an arbitrary caller-chosen type.
func TestDecodeJSON_DecodesIntoTheGivenType(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"name":"chrono trigger"}`))}
	got := reqtest.DecodeJSON[namedThing](t, resp)
	if got.Name != "chrono trigger" {
		t.Fatalf("got = %+v", got)
	}
}

// TestDecodeJSON_ClosesBody pins that DecodeJSON closes the response body.
func TestDecodeJSON_ClosesBody(t *testing.T) {
	body := &spyBody{Reader: strings.NewReader(`{}`)}
	resp := &http.Response{Body: body}
	reqtest.DecodeJSON[namedThing](t, resp)
	if !body.closed {
		t.Fatal("want DecodeJSON to close the response body")
	}
}

// TestDecodeJSON_FailsTestOnMalformedBody is the negative control: malformed JSON fails the test.
func TestDecodeJSON_FailsTestOnMalformedBody(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{not json`))}
		reqtest.DecodeJSON[namedThing](st, resp)
	}) {
		t.Fatal("want DecodeJSON to fail the test on malformed JSON")
	}
}

// TestNewJSONRequest_NilBodyOmitsBodyAndContentType covers a nil body: no body and no Content-Type.
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

// TestNewJSONRequest_EmptyBearerOmitsAuthorizationHeader pins that an empty bearer omits the header entirely.
func TestNewJSONRequest_EmptyBearerOmitsAuthorizationHeader(t *testing.T) {
	req := reqtest.NewJSONRequest(t, http.MethodGet, "/entries", "", nil)
	if _, ok := req.Header["Authorization"]; ok {
		t.Fatal("want no Authorization header at all for an empty bearer")
	}
}

// TestNewJSONRequest_NonEmptyBearerSetsAuthorizationHeader pins a standard Bearer header for a non-empty bearer.
func TestNewJSONRequest_NonEmptyBearerSetsAuthorizationHeader(t *testing.T) {
	req := reqtest.NewJSONRequest(t, http.MethodGet, "/entries", "tok-123", nil)
	if got := req.Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer tok-123")
	}
}

// TestNewJSONRequest_StructBodyIsJSONMarshaledWithContentType pins a struct body JSON-marshaled with Content-Type set.
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

// TestNewJSONRequest_StringBodyIsWrittenRaw pins that a string body is written verbatim, not JSON-marshaled,
// so a caller can pass deliberately malformed JSON to drive a decode-error test.
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

// TestNewJSONRequest_ReaderBodyPassesThroughUnencoded pins that an io.Reader body passes through unencoded.
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

// TestNewJSONRequest_UsableAgainstARealListener pins that the result works through a real http.Client,
// unlike httptest.NewRequest, whose RequestURI field http.Client.Do rejects outright.
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

// TestNewJSONRequest_FailsTestWhenRequestIsMalformed covers http.NewRequest's own error path.
func TestNewJSONRequest_FailsTestWhenRequestIsMalformed(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		reqtest.NewJSONRequest(st, "BAD METHOD", "/entries", "", nil)
	}) {
		t.Fatal("want NewJSONRequest to fail the test when the method is invalid")
	}
}

// TestNewJSONRequest_FailsTestWhenBodyCannotMarshal is the negative control on the JSON-marshal branch.
func TestNewJSONRequest_FailsTestWhenBodyCannotMarshal(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		reqtest.NewJSONRequest(st, http.MethodPost, "/entries", "", make(chan int))
	}) {
		t.Fatal("want NewJSONRequest to fail the test when the body cannot be JSON-marshaled")
	}
}

// problemResponse builds an *http.Response with the given status, content type, and a problem+json body.
func problemResponse(status int, contentType, code string) *http.Response {
	body := `{"status":` + strconv.Itoa(status) + `,"code":"` + code + `","detail":"d","revoke_jtis":["j1","j2"]}`
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestAssertProblem_HappyPathReturnsDecodedBody pins that all body fields, including revoke_jtis, decode correctly.
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

// TestAssertProblem_FailsOnStatusMismatch is the negative control on the status check.
func TestAssertProblem_FailsOnStatusMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		resp := problemResponse(http.StatusBadRequest, "application/problem+json", "invalid_body")
		reqtest.AssertProblem(st, resp, http.StatusNotFound, "invalid_body")
	}) {
		t.Fatal("want AssertProblem to fail the test on a status mismatch")
	}
}

// TestAssertProblem_FailsOnContentTypeMismatch is the negative control on the content-type check.
func TestAssertProblem_FailsOnContentTypeMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		resp := problemResponse(http.StatusBadRequest, "application/json", "invalid_body")
		reqtest.AssertProblem(st, resp, http.StatusBadRequest, "invalid_body")
	}) {
		t.Fatal("want AssertProblem to fail the test on a content-type mismatch")
	}
}

// TestAssertProblem_FailsOnCodeMismatch is the negative control on the code check.
func TestAssertProblem_FailsOnCodeMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		resp := problemResponse(http.StatusBadRequest, "application/problem+json", "invalid_body")
		reqtest.AssertProblem(st, resp, http.StatusBadRequest, "something_else")
	}) {
		t.Fatal("want AssertProblem to fail the test on a code mismatch")
	}
}

// problemRecorder builds an httptest.ResponseRecorder with the given status, content type, and problem+json body.
func problemRecorder(status int, contentType, code string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", contentType)
	rec.WriteHeader(status)
	_, _ = rec.WriteString(`{"status":` + strconv.Itoa(status) + `,"code":"` + code + `","detail":"d"}`)
	return rec
}

// TestAssertProblemRec_HappyPathReturnsDecodedBody is AssertProblem's recorder twin.
func TestAssertProblemRec_HappyPathReturnsDecodedBody(t *testing.T) {
	rec := problemRecorder(http.StatusForbidden, "application/problem+json", "forbidden")
	p := reqtest.AssertProblemRec(t, rec, http.StatusForbidden, "forbidden")
	if p.Status != http.StatusForbidden || p.Code != "forbidden" || p.Detail != "d" {
		t.Fatalf("p = %+v", p)
	}
}

// TestAssertProblemRec_FailsOnStatusMismatch is the negative control on the status check.
func TestAssertProblemRec_FailsOnStatusMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		rec := problemRecorder(http.StatusInternalServerError, "application/problem+json", "internal")
		reqtest.AssertProblemRec(st, rec, http.StatusBadGateway, "internal")
	}) {
		t.Fatal("want AssertProblemRec to fail the test on a status mismatch")
	}
}

// TestAssertProblemRec_FailsOnContentTypeMismatch is the negative control on the content-type check.
func TestAssertProblemRec_FailsOnContentTypeMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		rec := problemRecorder(http.StatusInternalServerError, "application/json", "internal")
		reqtest.AssertProblemRec(st, rec, http.StatusInternalServerError, "internal")
	}) {
		t.Fatal("want AssertProblemRec to fail the test on a content-type mismatch")
	}
}

// TestAssertProblemRec_FailsOnCodeMismatch is the negative control on the code check.
func TestAssertProblemRec_FailsOnCodeMismatch(t *testing.T) {
	if !runFatal(func(st *testing.T) {
		rec := problemRecorder(http.StatusInternalServerError, "application/problem+json", "internal")
		reqtest.AssertProblemRec(st, rec, http.StatusInternalServerError, "not_internal")
	}) {
		t.Fatal("want AssertProblemRec to fail the test on a code mismatch")
	}
}

// TestAssertProblemRec_FailsOnMalformedBody pins that a non-JSON body fails the test, not panics.
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

// stubClient is a minimal stand-in for a generated client, capturing just the base URL.
type stubClient struct{ baseURL string }

// TestNewTestClient_ConstructsAgainstTheBootedServer pins that construct receives the booted server's URL.
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

// TestNewTestClient_ClosesServerOnCleanup pins that the server does not outlive the test that booted it.
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
