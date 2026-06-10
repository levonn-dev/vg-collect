package httpkit

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// Run serves until ctx is cancelled, then shuts down gracefully (10s
// grace, then force-close). When Run returns, the server is closed.
func Run(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		// If cancellation races server start, Shutdown flags the server
		// closed and the pending ListenAndServe returns ErrServerClosed;
		// the drain below still completes.
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			// Grace period exceeded: force-close lingering connections
			// so the server is fully closed on every path Run returns.
			_ = srv.Close()
			return err
		}
		return <-errCh
	}
}
