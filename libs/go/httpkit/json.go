package httpkit

import (
	"encoding/json"
	"net/http"
)

// WriteJSON sets the JSON content type, writes status, and encodes v
// as the response body.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteRawJSON serves an already-encoded JSON body verbatim, for callers replaying a cached
// response without re-marshaling it. Caller contract: body must already come from an encoder
// (e.g. a cached json.Marshal output), which HTML-escapes string values by default; this
// function does no escaping of its own, so hand-assembled bytes would defeat that guarantee.
func WriteRawJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
