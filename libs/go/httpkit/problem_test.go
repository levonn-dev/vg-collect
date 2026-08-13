package httpkit_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

func TestWriteProblem(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/things/1", nil)
	httpkit.WriteProblem(w, r, httpkit.Problem{Status: 404, Title: "Not Found", Code: "thing_not_found", Detail: "no such thing"})
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q", ct)
	}
	if w.Code != 404 {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "about:blank" || got["instance"] != "/things/1" || got["code"] != "thing_not_found" {
		t.Fatalf("body = %v", got)
	}
}

func TestWriteProblemFields(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/things/1", nil)
	httpkit.WriteProblemFields(w, r, 404, "thing_not_found", "no such thing")
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q", ct)
	}
	if w.Code != 404 {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["title"] != "Not Found" || got["code"] != "thing_not_found" || got["detail"] != "no such thing" {
		t.Fatalf("body = %v", got)
	}
}
