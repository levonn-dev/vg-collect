// Browser telemetry relay: proxies OTLP trace/metric batches from the frontend to the collector agent.

package server

import (
	"bytes"
	"io"
	"net/http"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

// proxyOTLP relays a browser OTLP batch to the collector verbatim (session-
// gated, capped, never cached); real status/body pass through so the web SDK sees actual OTLP semantics.
func (h *Handlers) proxyOTLP(w http.ResponseWriter, r *http.Request, signal string) {
	if _, _, ok := h.requireSession(w, r); !ok {
		return
	}
	body, ok := httpkit.ReadCapped(w, r, 1<<20, "request body unreadable or over 1MiB")
	if !ok {
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
	res, err := h.otlpHTTP.Do(req) //nolint:gosec // G704: destination is h.otlpProxyURL, a fixed operator-configured collector address never derived from the request; only the opaque, size-capped body is caller-supplied
	if err != nil {
		// The 502 shows in RED metrics; this line carries the cause (DNS, refused, timeout) the status loses.
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
