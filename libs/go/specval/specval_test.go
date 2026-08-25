package specval_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/levonn-dev/vgkeep/libs/go/specval"
)

// recordingHandler is the final handler in the chain: it records what it was called with, so
// tests can prove a request reached it or never did. Guarded by a mutex: httptest runs it on a
// different goroutine than the one issuing the request.
type recordingHandler struct {
	mu     sync.Mutex
	called bool
	method string
	path   string
	body   []byte
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	h.mu.Lock()
	h.called = true
	h.method = r.Method
	h.path = r.URL.Path
	h.body = body
	h.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *recordingHandler) snapshot() (called bool, method, path string, body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.called, h.method, h.path, h.body
}

func loadTestSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("testdata/spec.yaml")
	if err != nil {
		t.Fatalf("load testdata/spec.yaml: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("testdata/spec.yaml is not a valid spec: %v", err)
	}
	return doc
}

// newTestServer builds an httptest server wrapping a fresh recordingHandler with
// specval.Middleware over a fresh spec copy (the middleware mutates Spec.Servers).
func newTestServer(t *testing.T, maxBodyBytes int64) (*httptest.Server, *recordingHandler) {
	t.Helper()
	rec := &recordingHandler{}
	mw := specval.Middleware(specval.Options{Spec: loadTestSpec(t), MaxBodyBytes: maxBodyBytes})
	srv := httptest.NewServer(mw(rec))
	t.Cleanup(srv.Close)
	return srv, rec
}

type problemBody struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func postJSON(t *testing.T, srv *httptest.Server, path, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decodeProblem(t *testing.T, resp *http.Response) problemBody {
	t.Helper()
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
	var p problemBody
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode problem+json body: %v", err)
	}
	return p
}

func TestMiddleware_DoesNotMutateCallerSpec(t *testing.T) {
	doc := loadTestSpec(t)
	if doc.Servers == nil {
		t.Fatal("test spec must declare servers for this test to be meaningful")
	}
	// Installing the middleware disables Host validation internally, but must do so on a copy:
	// doc.Servers may be shared with e.g. a docs endpoint that still needs it intact.
	_ = specval.Middleware(specval.Options{Spec: doc, MaxBodyBytes: 1 << 20})(&recordingHandler{})
	if doc.Servers == nil {
		t.Fatal("Middleware mutated the caller's Spec.Servers to nil; it must build the validator from a copy")
	}
}

func TestMiddleware_ValidRequestPassesAndBodyStaysReadable(t *testing.T) {
	srv, rec := newTestServer(t, 1<<20)

	const body = `{"name":"ab","status":"active","developers":["a","b"]}`
	resp := postJSON(t, srv, "/items", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	called, method, path, gotBody := rec.snapshot()
	if !called {
		t.Fatal("handler was not called for a valid request")
	}
	if method != http.MethodPost || path != "/items" {
		t.Fatalf("handler saw %s %s, want POST /items", method, path)
	}
	if string(gotBody) != body {
		t.Fatalf("handler body = %q, want %q (validator must restore a readable body)", gotBody, body)
	}
}

// TestMiddleware_DefaultsAreNotInjectedIntoTheBody pins SkipSettingDefaults: testdata's status
// property carries a schema default, and this body omits it, so the handler must see the body
// exactly as sent, not with the default injected.
func TestMiddleware_DefaultsAreNotInjectedIntoTheBody(t *testing.T) {
	srv, rec := newTestServer(t, 1<<20)

	const body = `{"name":"ab"}`
	resp := postJSON(t, srv, "/items", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	called, _, _, gotBody := rec.snapshot()
	if !called {
		t.Fatal("handler was not called for a valid request")
	}
	if string(gotBody) != body {
		t.Fatalf("handler body = %q, want %q (status carries a schema default; it must NOT be injected into an absent field)", gotBody, body)
	}
}

func TestMiddleware_ZeroMaxBodyBytesMeansUnbounded(t *testing.T) {
	srv, rec := newTestServer(t, 0)
	body := `{"name":"ab","status":"active","developers":["a","b"]}`
	resp := postJSON(t, srv, "/items", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (MaxBodyBytes <= 0 must not cap the body)", resp.StatusCode)
	}
	if called, _, _, gotBody := rec.snapshot(); !called || string(gotBody) != body {
		t.Fatalf("handler snapshot = called=%v body=%q, want called=true body=%q", called, gotBody, body)
	}
}

func TestMiddleware_BodySchemaFailuresWriteHouseProblem(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantDetail string
	}{
		{
			name:       "required field missing",
			body:       `{"status":"active"}`,
			wantDetail: "name is required",
		},
		{
			name:       "maxLength exceeded",
			body:       `{"name":"way-too-long-a-name"}`,
			wantDetail: "name must be at most 10 characters",
		},
		{
			name:       "enum violated",
			body:       `{"name":"ab","status":"bogus"}`,
			wantDetail: "status must be one of active, inactive",
		},
		{
			name:       "maxItems exceeded",
			body:       `{"name":"ab","developers":["a","b","c"]}`,
			wantDetail: "developers must be at most 2 items",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, rec := newTestServer(t, 1<<20)
			resp := postJSON(t, srv, "/items", tt.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			p := decodeProblem(t, resp)
			if p.Code != "invalid_body" {
				t.Errorf("code = %q, want invalid_body", p.Code)
			}
			if p.Detail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", p.Detail, tt.wantDetail)
			}
			if called, _, _, _ := rec.snapshot(); called {
				t.Error("handler must not be called for a rejected request")
			}
		})
	}
}

func TestMiddleware_MalformedJSONBodyUsesHouseString(t *testing.T) {
	srv, _ := newTestServer(t, 1<<20)
	resp := postJSON(t, srv, "/items", `{not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	p := decodeProblem(t, resp)
	if p.Code != "invalid_body" || p.Detail != "malformed JSON body" {
		t.Fatalf("problem = %+v, want code=invalid_body detail=%q", p, "malformed JSON body")
	}
}

func TestMiddleware_BodyOverMaxBodyBytesRejected(t *testing.T) {
	// Cap far smaller than a valid body, so the rejection can only be the byte-count cap, not the schema.
	srv, rec := newTestServer(t, 10)
	body := `{"name":"ab","status":"active","developers":["a","b"]}`
	resp := postJSON(t, srv, "/items", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	p := decodeProblem(t, resp)
	if p.Code != "invalid_body" {
		t.Errorf("code = %q, want invalid_body", p.Code)
	}
	if p.Detail != "malformed JSON body" {
		t.Errorf("detail = %q, want %q", p.Detail, "malformed JSON body")
	}
	if called, _, _, _ := rec.snapshot(); called {
		t.Error("handler must not be called when the body exceeds MaxBodyBytes")
	}
}

// TestMiddleware_NilBodyDoesNotPanic pins the MaxBodyBytes wrap against a nil r.Body:
// http.NewRequest with a nil io.Reader leaves Body literally nil (unlike a real net/http server
// or httptest.NewRequest, which fill in a non-nil Body). MaxBytesReader always wraps regardless
// and panics on read/close of a nil underlying reader. The target must be /secure, not /items:
// kin-openapi only reads the body unconditionally for an operation with a security requirement.
func TestMiddleware_NilBodyDoesNotPanic(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := specval.Middleware(specval.Options{Spec: loadTestSpec(t), MaxBodyBytes: 1 << 20})(next)

	req, err := http.NewRequest(http.MethodGet, "/secure", nil)
	if err != nil {
		t.Fatalf("build GET /secure: %v", err)
	}
	if req.Body != nil {
		t.Fatal("test setup: http.NewRequest with a nil io.Reader must leave Body nil")
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a nil Body must not panic the MaxBytesReader wrap)", rec.Code)
	}
	if !called {
		t.Fatal("the wrapped handler was not reached for a valid nil-Body GET request")
	}
}

func TestMiddleware_BadQueryParamsWriteHouseProblem(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantDetail string
	}{
		{name: "enum violated", query: "status=bogus", wantDetail: "status must be one of active, inactive"},
		{name: "out of range", query: "page=999", wantDetail: "page is invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, rec := newTestServer(t, 1<<20)
			resp, err := http.Get(srv.URL + "/items?" + tt.query)
			if err != nil {
				t.Fatalf("GET /items?%s: %v", tt.query, err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			p := decodeProblem(t, resp)
			if p.Code != "invalid_param" {
				t.Errorf("code = %q, want invalid_param", p.Code)
			}
			if p.Detail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", p.Detail, tt.wantDetail)
			}
			if called, _, _, _ := rec.snapshot(); called {
				t.Error("handler must not be called for a rejected request")
			}
		})
	}
}

func TestMiddleware_BadPathAndHeaderParamsWriteHouseProblem(t *testing.T) {
	t.Run("path parameter", func(t *testing.T) {
		srv, rec := newTestServer(t, 1<<20)
		// itemId is constrained to enum [foo, bar]; "baz" violates it. No X-Priority header
		// isolates this from the header parameter on the same operation.
		resp, err := http.Get(srv.URL + "/items/baz")
		if err != nil {
			t.Fatalf("GET /items/baz: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		p := decodeProblem(t, resp)
		if p.Code != "invalid_param" {
			t.Errorf("code = %q, want invalid_param", p.Code)
		}
		if want := "itemId must be one of foo, bar"; p.Detail != want {
			t.Errorf("detail = %q, want %q", p.Detail, want)
		}
		if called, _, _, _ := rec.snapshot(); called {
			t.Error("handler must not be called for a rejected request")
		}
	})

	t.Run("header parameter", func(t *testing.T) {
		srv, rec := newTestServer(t, 1<<20)
		// A valid itemId (foo) isolates this to X-Priority, constrained to 1-5; 99 violates the max.
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/items/foo", nil)
		if err != nil {
			t.Fatalf("build GET /items/foo: %v", err)
		}
		req.Header.Set("X-Priority", "99")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /items/foo: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		p := decodeProblem(t, resp)
		if p.Code != "invalid_param" {
			t.Errorf("code = %q, want invalid_param", p.Code)
		}
		if want := "X-Priority is invalid"; p.Detail != want {
			t.Errorf("detail = %q, want %q", p.Detail, want)
		}
		if called, _, _, _ := rec.snapshot(); called {
			t.Error("handler must not be called for a rejected request")
		}
	})
}

func TestMiddleware_SecuredOperationPassesWithoutAuth(t *testing.T) {
	srv, rec := newTestServer(t, 1<<20)
	// No Authorization header: a real AuthenticationFunc would 401. specval always installs the
	// no-op since jwtauth, ahead of it in the real chain, is the only layer enforcing auth.
	resp, err := http.Get(srv.URL + "/secure")
	if err != nil {
		t.Fatalf("GET /secure: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no-op auth must not reject)", resp.StatusCode)
	}
	if called, _, _, _ := rec.snapshot(); !called {
		t.Error("handler was not called for the secured operation")
	}
}

func TestMiddleware_UnmatchedRoutesPassThroughUntouched(t *testing.T) {
	t.Run("unknown path", func(t *testing.T) {
		srv, rec := newTestServer(t, 1<<20)
		resp, err := http.Get(srv.URL + "/nope")
		if err != nil {
			t.Fatalf("GET /nope: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		// recordingHandler always answers 200; seeing that (not problem+json) proves specval did not intercept.
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (the mux, not specval, owns unmatched routes)", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct == "application/problem+json" {
			t.Fatal("specval wrote a problem+json response for an unmatched route")
		}
		if called, method, path, _ := rec.snapshot(); !called || method != http.MethodGet || path != "/nope" {
			t.Fatalf("handler snapshot = called=%v %s %s, want called=true GET /nope", called, method, path)
		}
	})

	t.Run("known path, wrong method", func(t *testing.T) {
		srv, rec := newTestServer(t, 1<<20)
		req, err := http.NewRequest(http.MethodDelete, srv.URL+"/items", nil)
		if err != nil {
			t.Fatalf("build DELETE /items: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("DELETE /items: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (the mux, not specval, owns wrong-method requests too)", resp.StatusCode)
		}
		if called, method, _, _ := rec.snapshot(); !called || method != http.MethodDelete {
			t.Fatalf("handler snapshot = called=%v method=%s, want called=true DELETE", called, method)
		}
	})
}
