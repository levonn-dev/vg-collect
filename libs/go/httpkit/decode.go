package httpkit

import (
	"encoding/json"
	"io"
	"net/http"
)

// DecodeBody caps the request body at maxBytes and JSON-decodes it into v. On any failure
// (including an over-cap body) it writes a 400 problem itself and returns false.
func DecodeBody(w http.ResponseWriter, r *http.Request, maxBytes int64, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteProblemFields(w, r, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return false
	}
	return true
}

// ReadCapped caps the request body at maxBytes and reads it whole, for callers passing the
// bytes on rather than decoding them here. A false return means a 400 was already written,
// detail "unreadable body" unless the caller supplies one (only the first is used).
func ReadCapped(w http.ResponseWriter, r *http.Request, maxBytes int64, detail ...string) ([]byte, bool) {
	msg := "unreadable body"
	if len(detail) > 0 {
		msg = detail[0]
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteProblemFields(w, r, http.StatusBadRequest, "invalid_body", msg)
		return nil, false
	}
	return body, true
}
