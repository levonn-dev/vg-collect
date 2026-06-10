package httpkit

import (
	"log/slog"
	"net/http"
	"time"
)

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.wroteHeader = true
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	sw.wroteHeader = true // implicit 200 on first body write
	return sw.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach Flusher/Hijacker/ReaderFrom
// on the underlying writer through this wrapper.
func (sw *statusWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }

// Recover converts panics into 500 problem+json responses. It wraps the
// writer itself so it knows whether headers were already committed;
// recovery after a partial response must not append a second body.
// http.ErrAbortHandler re-panics per stdlib convention (the server
// aborts the connection silently; ReverseProxy uses it on client gone).
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					logger.ErrorContext(r.Context(), "panic recovered", "panic", rec, "path", r.URL.Path)
					if !tw.wroteHeader {
						WriteProblem(tw, r, Problem{Status: http.StatusInternalServerError, Title: "Internal Server Error", Code: "internal"})
					}
				}
			}()
			next.ServeHTTP(tw, r)
		})
	}
}

// RequestLogger logs one line per request; trace IDs arrive via the
// slog handler installed by libs/go/otel, not here.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(sw, r)
			logger.InfoContext(r.Context(), "http request",
				"method", r.Method, "path", r.URL.Path,
				"status", sw.status, "duration_ms", time.Since(start).Milliseconds())
		})
	}
}
