package httpkit_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

// errRelayUpstream stands in for a client package's own ErrUpstream sentinel; Relay takes it
// as a parameter so each caller keeps its own distinct error identity for errors.Is.
var errRelayUpstream = errors.New("relay_test: upstream failure")

func TestRelay_AllowedStatusPassesThrough(t *testing.T) {
	res, err := httpkit.Relay(200, "application/json", []byte(`{"ok":true}`), errRelayUpstream, 200, 404)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || res.ContentType != "application/json" || string(res.Body) != `{"ok":true}` {
		t.Fatalf("result = %+v", res)
	}
}

func TestRelay_UndeclaredStatusWrapsCallersUpstream(t *testing.T) {
	_, err := httpkit.Relay(500, "", nil, errRelayUpstream, 200, 404)
	if !errors.Is(err, errRelayUpstream) {
		t.Fatalf("want wrapped errRelayUpstream, got %v", err)
	}
}

func TestRelay_ZeroResultOnRejection(t *testing.T) {
	res, err := httpkit.Relay(500, "application/json", []byte("junk"), errRelayUpstream, 200)
	if err == nil {
		t.Fatal("want an error for an undeclared status")
	}
	if res.Status != 0 || res.ContentType != "" || res.Body != nil {
		t.Fatalf("result = %+v, want the zero value on rejection", res)
	}
}

func TestContentType_ReadsHeader(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/problem+json")
	if got := httpkit.ContentType(w.Result()); got != "application/problem+json" {
		t.Fatalf("content type = %q", got)
	}
}

func TestContentType_MissingHeaderIsEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	if got := httpkit.ContentType(w.Result()); got != "" {
		t.Fatalf("content type = %q, want empty", got)
	}
}
