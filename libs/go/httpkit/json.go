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

// WriteRawJSON serves an already-encoded JSON body verbatim, for
// callers replaying a cached response without re-marshaling it.
//
// Caller contract: body must be JSON this process (or a trusted
// upstream already serving application/json) produced through an
// encoder, such as an earlier json.Marshal output cached and replayed
// - json.Marshal HTML-escapes string values by default, so field text
// that originated from a caller-supplied string is already safe by
// the time it lands in body. This function performs no escaping of
// its own: passing bytes assembled by hand, or any bytes not already
// run through an encoder, would defeat that guarantee.
func WriteRawJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
