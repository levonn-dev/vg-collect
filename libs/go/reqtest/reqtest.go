// Package reqtest holds the HTTP test mechanics that kept getting
// reinvented per service: building a request with an optional bearer
// and JSON body, booting an httptest.Server behind a constructed
// client, asserting a problem+json response, decoding a JSON body,
// and polling for an async condition. It sits beside httpkit the way
// pgtest sits beside pgkit.
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

// NewJSONRequest builds a request for method and url carrying an
// optional Bearer authorization header (empty bearer omits the header
// entirely) and an optional body. body is handled by its Go type, the
// union of what real call sites across five services actually pass:
// nil for no body; a string written verbatim, so a caller can hand
// deliberately invalid JSON to drive a decode-error test; an
// io.Reader (collection's jsonBody and friends already build one)
// passed through unencoded; anything else JSON-marshaled. A non-nil
// body gets a Content-Type: application/json header.
//
// http.NewRequest, not httptest.NewRequest: about half of the sites
// this replaces send the result through a real http.Client against a
// live httptest.Server, and httptest.NewRequest sets RequestURI, which
// http.Client.Do refuses outright ("RequestURI can't be set in client
// requests"). The other half hand the result straight to a handler or
// router via httptest.ResponseRecorder with no real network round
// trip; every one of those routes on req.URL, which http.NewRequest
// populates the same way, so it serves both styles.
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

// ProblemBody is the shape AssertProblem and AssertProblemRec decode a
// problem+json response into: the fields real call sites across auth,
// collection, social and user actually read off it. RevokeJTIs is
// auth's token-endpoint extension (the jtis a refresh-reuse response
// carries for the BFF to denylist); it stays zero for every other
// service's problem responses, which never set it.
type ProblemBody struct {
	Status     int      `json:"status"`
	Code       string   `json:"code"`
	Detail     string   `json:"detail"`
	RevokeJTIs []string `json:"revoke_jtis"`
}

// AssertProblem asserts resp is a problem+json answer with the given
// status and machine code, failing the test on any mismatch, and
// returns the decoded body for a caller that needs more (Detail, or
// auth's RevokeJTIs).
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

// AssertProblemRec is AssertProblem for a recorder-driven handler
// call: same three checks (status, problem+json content type,
// machine code), same decoded return.
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

// NewTestClient boots an httptest.Server serving h, closes it on
// cleanup, and hands the server's URL to construct to build the
// client under test. construct has no error return because real
// constructors disagree on shape (most are func(string) (T, error),
// pricecharting's is func(string) T plus a couple of field
// overrides); a caller wanting the common (T, error) constructor
// calls t.Fatal on error inside its own closure.
func NewTestClient[T any](t *testing.T, h http.Handler, construct func(baseURL string) T) T {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return construct(srv.URL)
}

// DecodeJSON decodes resp's body as JSON into T, closing the body
// once decoded (the trait that made this the surviving variant over
// auth's original decode[T], which never closed it). Fails the test
// on a decode error.
func DecodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// WaitFor polls check every 25ms until it returns true or timeout
// elapses, failing the test in the latter case. For asserting on
// state a detached goroutine mutates asynchronously (a background
// refresh, a queued write) where there is no signal to block on
// directly.
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
