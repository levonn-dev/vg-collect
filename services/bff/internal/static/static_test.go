package static

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestServesIndexUncached(t *testing.T) {
	for _, path := range []string{"/", "/index.html"} {
		rec := get(t, path)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "vgkeep") {
			t.Fatalf("%s: code=%d", path, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("%s: cache-control = %q", path, cc)
		}
	}
}

func TestClientRouteFallsBackToIndex(t *testing.T) {
	rec := get(t, "/login")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "vgkeep") {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("cache-control = %q", cc)
	}
}

func TestHashedAssetsAreImmutable(t *testing.T) {
	rec := get(t, "/assets/placeholder.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("cache-control = %q", cc)
	}
}

func TestMissingFilesWithExtensionsAre404(t *testing.T) {
	for _, path := range []string{"/assets/missing.js", "/missing.png"} {
		if rec := get(t, path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: code = %d", path, rec.Code)
		}
	}
}

func TestDirectoryListingIs404(t *testing.T) {
	for _, path := range []string{"/assets/", "/assets"} {
		if rec := get(t, path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: code = %d, want 404 (no directory listing)", path, rec.Code)
		}
	}
}
