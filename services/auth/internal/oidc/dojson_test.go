package oidc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// doJSONOut is the shared decode target; doJSON is generic over out.
type doJSONOut struct {
	Msg string `json:"msg"`
}

// TestDoJSON pins doJSON's contract: every failure classifies as *ProviderError
// with the caller's op; a clean response decodes into out.
func TestDoJSON(t *testing.T) {
	const op = "the-op"

	tests := []struct {
		name string
		// handler serves the response; nil means a pre-closed server (transport-error case).
		handler    http.HandlerFunc
		wantErr    bool
		wantStatus int // asserted only when non-zero
	}{
		{
			name: "happy path decodes into out",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"msg":"hi"}`))
			},
		},
		{
			name: "non-2xx status carries the status and the caller's op",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			},
			wantErr:    true,
			wantStatus: http.StatusTeapot,
		},
		{
			name: "decode failure",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("{not json"))
			},
			wantErr: true,
		},
		{
			name:    "transport error",
			handler: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Transport-error case closes the server immediately: a deterministic
			// connection-refused beats a hostname that may or may not resolve.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.handler != nil {
					tt.handler(w, r)
				}
			}))
			url := srv.URL
			hc := srv.Client()
			if tt.handler == nil {
				srv.Close()
			} else {
				defer srv.Close()
			}

			var out doJSONOut
			err := doJSON(context.Background(), hc, http.MethodGet, url, nil, nil, op, &out)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("doJSON: %v", err)
				}
				if out.Msg != "hi" {
					t.Fatalf("out = %+v, want decoded {hi}", out)
				}
				return
			}
			if err == nil {
				t.Fatal("doJSON: want a *ProviderError, got nil")
			}
			if err.Op != op {
				t.Fatalf("Op = %q, want %q", err.Op, op)
			}
			if tt.wantStatus != 0 {
				if err.Status != tt.wantStatus {
					t.Fatalf("Status = %d, want %d", err.Status, tt.wantStatus)
				}
				if err.Err != nil {
					t.Fatalf("Err = %v, want nil on a status failure", err.Err)
				}
				return
			}
			// Transport and decode failures carry the underlying error instead of a status.
			if err.Status != 0 {
				t.Fatalf("Status = %d, want 0", err.Status)
			}
			if err.Err == nil {
				t.Fatal("Err = nil, want the underlying transport/decode error")
			}
		})
	}
}
