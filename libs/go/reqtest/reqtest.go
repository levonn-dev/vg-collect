// Package reqtest provides shared HTTP test mechanics: request building with an
// optional bearer and JSON body, a constructed client behind an httptest.Server,
// problem+json assertions, JSON body decoding, and async condition polling.
package reqtest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// NewJSONRequest builds a request for method and url with an optional Bearer header
// (empty bearer omits it). body dispatches by type: nil none, string verbatim (for
// malformed-JSON tests), io.Reader passthrough, else JSON-marshaled with Content-Type set.
// Uses http.NewRequest: httptest.NewRequest sets RequestURI, which http.Client.Do rejects.
func NewJSONRequest(t *testing.T, method, url, bearer string, body any) *http.Request {
	t.Helper()
	var r io.Reader
	switch v := body.(type) {
	case nil:
		// no body
	case string:
		r = strings.NewReader(v)
	case io.Reader:
		r = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// ProblemBody is the problem+json shape AssertProblem and AssertProblemRec decode into.
// RevokeJTIs is auth's refresh-reuse extension; other services leave it zero.
type ProblemBody struct {
	Status     int      `json:"status"`
	Code       string   `json:"code"`
	Detail     string   `json:"detail"`
	RevokeJTIs []string `json:"revoke_jtis"`
}

// AssertProblem asserts resp is a problem+json response with the given status
// and code, failing the test on mismatch, and returns the decoded body.
func AssertProblem(t *testing.T, resp *http.Response, status int, code string) ProblemBody {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d", resp.StatusCode, status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
	p := DecodeJSON[ProblemBody](t, resp)
	if p.Code != code {
		t.Fatalf("problem code = %q, want %q", p.Code, code)
	}
	return p
}

// AssertProblemRec is AssertProblem for a recorder-driven handler call.
func AssertProblemRec(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) ProblemBody {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
	var p ProblemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v (%s)", err, rec.Body.String())
	}
	if p.Code != code {
		t.Fatalf("problem code = %q, want %q", p.Code, code)
	}
	return p
}

// NewTestClient boots an httptest.Server serving h, closes it on cleanup, and hands
// its URL to construct to build the client under test. construct returns T without
// an error since real constructors disagree on shape; call t.Fatal inside it if needed.
func NewTestClient[T any](t *testing.T, h http.Handler, construct func(baseURL string) T) T {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return construct(srv.URL)
}

// DecodeJSON decodes resp's body as JSON into T, closing the body, and fails the test on a decode error.
func DecodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// WaitFor polls check every 25ms until it returns true, failing the test if timeout elapses first.
func WaitFor(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}
