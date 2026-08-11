// Browser telemetry relay: proxies OTLP trace and metric
// batches from the frontend to the collector agent.

package server

import (
	"bytes"
	"io"
	"net/http"

	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
)

// proxyOTLP relays a browser OTLP batch to the collector agent
// verbatim; ProxyTraces and ProxyMetrics are thin wrappers selecting
// the signal. Session-gated like every /api route; the body is
// capped; the collector's response status and body pass through so
// the web SDK sees real OTLP semantics. Never cached.
func (h *Handlers) proxyOTLP(w http.ResponseWriter, r *http.Request, signal string) {
	if _, _, ok := session.FromContext(r.Context()); !ok {
		h.unauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_body", "request body unreadable or over 1MiB")
		return
	}
	if h.otlpProxyURL == "" {
		// Accept and drop: telemetry must never break the app.
		w.WriteHeader(http.StatusOK)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.otlpProxyURL+"/v1/"+signal, bytes.NewReader(body))
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collector request could not be built")
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	if enc := r.Header.Get("Content-Encoding"); enc != "" {
		req.Header.Set("Content-Encoding", enc)
	}
	res, err := h.otlpHTTP.Do(req)
	if err != nil {
		// The 502 shows in RED metrics; the line carries the cause
		// (DNS, refused, timeout) that the status alone loses.
		h.logger.WarnContext(r.Context(), "browser telemetry relay failed", "err", err)
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collector unavailable")
		return
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		writeProblem(w, r, http.StatusBadGateway, "upstream_error", "collector response unreadable")
		return
	}
	writeRelay(w, res.StatusCode, res.Header.Get("Content-Type"), out)
}

// ProxyTraces relays browser OTLP trace batches to the collector agent.
func (h *Handlers) ProxyTraces(w http.ResponseWriter, r *http.Request) {
	h.proxyOTLP(w, r, "traces")
}

// ProxyMetrics relays browser OTLP metric batches to the collector agent.
func (h *Handlers) ProxyMetrics(w http.ResponseWriter, r *http.Request) {
	h.proxyOTLP(w, r, "metrics")
}
