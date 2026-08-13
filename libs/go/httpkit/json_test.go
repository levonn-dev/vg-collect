package httpkit_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	httpkit.WriteJSON(w, 201, map[string]string{"id": "abc"})
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	if w.Code != 201 {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "abc" {
		t.Fatalf("body = %v", got)
	}
}

func TestWriteRawJSON(t *testing.T) {
	w := httptest.NewRecorder()
	body := []byte(`{"id":"abc"}`)
	httpkit.WriteRawJSON(w, 200, body)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Body.String() != string(body) {
		t.Fatalf("body = %q, want %q (raw, not re-encoded)", w.Body.String(), body)
	}
}

func TestWriteRawJSON_StatusHonored(t *testing.T) {
	w := httptest.NewRecorder()
	httpkit.WriteRawJSON(w, 404, []byte(`{}`))
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
