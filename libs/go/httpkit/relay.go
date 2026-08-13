package httpkit

import (
	"fmt"
	"net/http"
	"slices"
)

// RelayResult is one relayable upstream answer: the status, content
// type, and raw body a client passes through to its own caller
// untouched, without re-decoding or re-encoding it.
type RelayResult struct {
	Status      int
	ContentType string
	Body        []byte
}

// Relay admits status as a relayable answer when it appears in
// allowed, returning the raw envelope. Otherwise it wraps upstream -
// the caller's own sentinel error, so errors.Is keeps matching that
// package's identity - with the status that fell outside the relayed
// contract.
func Relay(status int, contentType string, body []byte, upstream error, allowed ...int) (RelayResult, error) {
	if slices.Contains(allowed, status) {
		return RelayResult{Status: status, ContentType: contentType, Body: body}, nil
	}
	return RelayResult{}, fmt.Errorf("%w: status %d", upstream, status)
}

// ContentType reads the Content-Type header off an upstream response -
// the one-line read every relay call site otherwise repeats.
func ContentType(resp *http.Response) string {
	return resp.Header.Get("Content-Type")
}
