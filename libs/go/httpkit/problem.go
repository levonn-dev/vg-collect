// Package httpkit provides HTTP server lifecycle, middleware, and RFC 9457
// problem+json responses shared by all vgkeep services.
package httpkit

import (
	"encoding/json"
	"net/http"
)

// Problem is an RFC 9457 problem details body. Code is a vgkeep
// extension carrying a stable machine-readable error code.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
	Code     string `json:"code,omitempty"`
}

// WriteProblem writes p as an RFC 9457 problem+json response, defaulting
// Type to "about:blank" and Instance to r.URL.Path when either is unset.
func WriteProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	if p.Type == "" {
		p.Type = "about:blank"
	}
	if p.Instance == "" && r != nil {
		p.Instance = r.URL.Path
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// WriteProblemFields builds a Problem from status, code, and detail, deriving Title from
// status via http.StatusText, and writes it via WriteProblem.
func WriteProblemFields(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	WriteProblem(w, r, Problem{Status: status, Title: http.StatusText(status), Code: code, Detail: detail})
}
