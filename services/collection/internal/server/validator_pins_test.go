// Validator-path pins: each case drives a request through the FULL handler
// stack (real router, real jwtauth) and asserts the status + problem code
// specval's request-validation middleware answers with.
package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestValidatorPath_CreateEntry_OversizeRegion pins the region
// maxLength(32) contract cap: 33 characters must 400 invalid_body.
func TestValidatorPath_CreateEntry_OversizeRegion(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	productID := uuid.New()
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
		createBody(productID, func(m map[string]any) { m["region"] = strings.Repeat("a", 33) }))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_CreateEntry_BadPackagingEnum pins the packaging
// enum contract.
func TestValidatorPath_CreateEntry_BadPackagingEnum(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	productID := uuid.New()
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
		createBody(productID, func(m map[string]any) { m["packaging"] = "boxed" }))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_CreateEntry_OversizeNotes pins the notes
// maxLength(10000) contract cap.
func TestValidatorPath_CreateEntry_OversizeNotes(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	productID := uuid.New()
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
		createBody(productID, func(m map[string]any) { m["notes"] = strings.Repeat("x", 10001) }))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_BulkUpdateEntries_TooManyEntryIDs pins the
// entry_ids maxItems(200) contract cap.
func TestValidatorPath_BulkUpdateEntries_TooManyEntryIDs(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries/bulk-update", a.token(t, uuid.NewString()),
		jsonBody(map[string]any{"entry_ids": manyUUIDStrings(201), "status": "playing"}))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_ListEntries_LimitOverMax pins the limit maximum(500)
// contract cap: specval rejects an out-of-range limit before listParams runs.
// Unlike enrichment's community-list clamp reversal, this outcome never changed, only which layer enforces it.
func TestValidatorPath_ListEntries_LimitOverMax(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/entries?limit=9999", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
}
