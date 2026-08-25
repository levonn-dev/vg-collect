package httpkit_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

func TestDecodeBody_Success(t *testing.T) {
	r := httptest.NewRequest("POST", "/things", strings.NewReader(`{"name":"abc"}`))
	w := httptest.NewRecorder()
	var v struct {
		Name string `json:"name"`
	}
	if ok := httpkit.DecodeBody(w, r, 1024, &v); !ok {
		t.Fatalf("DecodeBody = false, want true")
	}
	if v.Name != "abc" {
		t.Fatalf("decoded name = %q", v.Name)
	}
}

func TestDecodeBody_MalformedJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/things", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	var v struct{}
	if ok := httpkit.DecodeBody(w, r, 1024, &v); ok {
		t.Fatalf("DecodeBody = true, want false")
	}
	assertMalformedBodyProblem(t, w)
}

func TestDecodeBody_OverCap(t *testing.T) {
	r := httptest.NewRequest("POST", "/things", strings.NewReader(`{"name":"way too long for the cap"}`))
	w := httptest.NewRecorder()
	var v struct{}
	if ok := httpkit.DecodeBody(w, r, 4, &v); ok {
		t.Fatalf("DecodeBody = true, want false")
	}
	// An over-cap body surfaces as a decode error, same as malformed JSON.
	assertMalformedBodyProblem(t, w)
}

func assertMalformedBodyProblem(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "invalid_body" || got["detail"] != "malformed JSON body" {
		t.Fatalf("body = %v", got)
	}
}

func TestReadCapped_Success(t *testing.T) {
	r := httptest.NewRequest("POST", "/raw", strings.NewReader(`hello`))
	w := httptest.NewRecorder()
	body, ok := httpkit.ReadCapped(w, r, 1024)
	if !ok {
		t.Fatalf("ReadCapped = false, want true")
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q", body)
	}
}

func TestReadCapped_OverCap(t *testing.T) {
	r := httptest.NewRequest("POST", "/raw", strings.NewReader(`way too long for the cap`))
	w := httptest.NewRecorder()
	body, ok := httpkit.ReadCapped(w, r, 4)
	if ok {
		t.Fatalf("ReadCapped = true, want false")
	}
	if body != nil {
		t.Fatalf("body = %v, want nil", body)
	}
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "invalid_body" || got["detail"] != "unreadable body" {
		t.Fatalf("body = %v", got)
	}
}

// TestReadCapped_OverCap_CustomDetail covers callers needing their own wording instead of the "unreadable body" default.
func TestReadCapped_OverCap_CustomDetail(t *testing.T) {
	r := httptest.NewRequest("POST", "/raw", strings.NewReader(`way too long for the cap`))
	w := httptest.NewRecorder()
	body, ok := httpkit.ReadCapped(w, r, 4, "request body unreadable or over 1MiB")
	if ok {
		t.Fatalf("ReadCapped = true, want false")
	}
	if body != nil {
		t.Fatalf("body = %v, want nil", body)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "invalid_body" || got["detail"] != "request body unreadable or over 1MiB" {
		t.Fatalf("body = %v", got)
	}
}
