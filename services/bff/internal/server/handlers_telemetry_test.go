// Tests for the browser telemetry relay.

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubRoundTripper lets a test assert whether the otlp relay's upstream
// http.Client was ever dialed.
type stubRoundTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (s *stubRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return s.fn(r) }

// newTestHandlersForOtlp builds Handlers with a fresh session ready to
// drive the otlp relay route; otlpProxyURL configures the relay target
// ("" is drop mode).
func newTestHandlersForOtlp(t *testing.T, otlpProxyURL string) (*Handlers, *testEnv) {
	t.Helper()
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.otlpProxyURL = otlpProxyURL
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
}

func TestUnitProxyTraces_RequiresSession(t *testing.T) {
	h, env := newTestHandlersForOtlp(t, "")
	rec := doUnauthed(t, h, env, http.MethodPost, "/api/otlp/v1/traces")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}
}

func TestUnitProxyTraces_DropModeAnswers200(t *testing.T) {
	h, env := newTestHandlersForOtlp(t, "")
	h.otlpHTTP = &http.Client{Transport: &stubRoundTripper{fn: func(*http.Request) (*http.Response, error) {
		t.Fatal("drop mode must never dial the upstream")
		return nil, nil
	}}}
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(`{"resourceSpans":[]}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("drop mode: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestUnitProxyTraces_RelaysVerbatim proves the relay is a pure pass-
// through in both directions: the collector sees the exact method, path,
// body, and Content-Type/Content-Encoding the browser sent (but never the
// session cookie), and the browser sees the collector's exact status,
// content type, and body back.
func TestUnitProxyTraces_RelaysVerbatim(t *testing.T) {
	const marker = "collector-response-marker"
	const sentBody = `{"resourceSpans":[{"resource":{}}]}`
	var gotMethod, gotPath, gotContentType, gotContentEncoding, gotCookie string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotContentEncoding = r.Header.Get("Content-Encoding")
		gotCookie = r.Header.Get("Cookie")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(marker))
	}))
	defer upstream.Close()

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(sentBody))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-protobuf" || rec.Body.String() != marker {
		t.Fatalf("relay to caller: code=%d content-type=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/traces" {
		t.Fatalf("upstream request line: method=%s path=%s", gotMethod, gotPath)
	}
	if string(gotBody) != sentBody {
		t.Fatalf("upstream body = %q, want %q", gotBody, sentBody)
	}
	if gotContentType != "application/json" || gotContentEncoding != "gzip" {
		t.Fatalf("forwarded headers: content-type=%q content-encoding=%q", gotContentType, gotContentEncoding)
	}
	if gotCookie != "" {
		t.Fatalf("the session cookie must never ride the upstream hop, got %q", gotCookie)
	}
}

func TestUnitProxyTraces_UpstreamPartialStatusPassesThrough(t *testing.T) {
	const problemBody = `{"type":"about:blank","title":"Bad Request","status":400,"code":"bad_export"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(problemBody))
	}))
	defer upstream.Close()

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(`{}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != problemBody {
		t.Fatalf("partial status relay: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnitProxyTraces_UpstreamDownAnswers502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstream.Close() // closed before any request: dialing it must fail

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(`{}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream down: code = %d", rec.Code)
	}
}

func TestUnitProxyTraces_OversizeBodyAnswers400(t *testing.T) {
	h, env := newTestHandlersForOtlp(t, "") // drop mode: the cap check must still run first
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(strings.Repeat("a", 1<<20+1)))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize body: code = %d", rec.Code)
	}
}

func TestUnitProxyMetrics_RequiresSession(t *testing.T) {
	h, env := newTestHandlersForOtlp(t, "")
	rec := doUnauthed(t, h, env, http.MethodPost, "/api/otlp/v1/metrics")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}
}

func TestUnitProxyMetrics_DropModeAnswers200(t *testing.T) {
	h, env := newTestHandlersForOtlp(t, "")
	h.otlpHTTP = &http.Client{Transport: &stubRoundTripper{fn: func(*http.Request) (*http.Response, error) {
		t.Fatal("drop mode must never dial the upstream")
		return nil, nil
	}}}
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/metrics", strings.NewReader(`{"resourceMetrics":[]}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("drop mode: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestUnitProxyMetrics_RelaysVerbatim proves the relay is a pure pass-
// through in both directions: the collector sees the exact method, path,
// body, and Content-Type/Content-Encoding the browser sent (but never the
// session cookie), and the browser sees the collector's exact status,
// content type, and body back.
func TestUnitProxyMetrics_RelaysVerbatim(t *testing.T) {
	const marker = "collector-response-marker"
	const sentBody = `{"resourceMetrics":[{"resource":{}}]}`
	var gotMethod, gotPath, gotContentType, gotContentEncoding, gotCookie string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotContentEncoding = r.Header.Get("Content-Encoding")
		gotCookie = r.Header.Get("Cookie")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(marker))
	}))
	defer upstream.Close()

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/metrics", strings.NewReader(sentBody))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-protobuf" || rec.Body.String() != marker {
		t.Fatalf("relay to caller: code=%d content-type=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/metrics" {
		t.Fatalf("upstream request line: method=%s path=%s", gotMethod, gotPath)
	}
	if string(gotBody) != sentBody {
		t.Fatalf("upstream body = %q, want %q", gotBody, sentBody)
	}
	if gotContentType != "application/json" || gotContentEncoding != "gzip" {
		t.Fatalf("forwarded headers: content-type=%q content-encoding=%q", gotContentType, gotContentEncoding)
	}
	if gotCookie != "" {
		t.Fatalf("the session cookie must never ride the upstream hop, got %q", gotCookie)
	}
}

func TestUnitProxyMetrics_UpstreamPartialStatusPassesThrough(t *testing.T) {
	const problemBody = `{"type":"about:blank","title":"Bad Request","status":400,"code":"bad_export"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(problemBody))
	}))
	defer upstream.Close()

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/metrics", strings.NewReader(`{}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != problemBody {
		t.Fatalf("partial status relay: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnitProxyMetrics_UpstreamDownAnswers502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstream.Close() // closed before any request: dialing it must fail

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/metrics", strings.NewReader(`{}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream down: code = %d", rec.Code)
	}
}

func TestUnitProxyMetrics_OversizeBodyAnswers400(t *testing.T) {
	h, env := newTestHandlersForOtlp(t, "") // drop mode: the cap check must still run first
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/metrics", strings.NewReader(strings.Repeat("a", 1<<20+1)))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize body: code = %d", rec.Code)
	}
}
