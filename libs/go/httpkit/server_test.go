package httpkit_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
)

func TestRun_GracefulShutdown(t *testing.T) {
	srv := httpkit.NewServer("127.0.0.1:0", http.NewServeMux())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- httpkit.Run(ctx, srv) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not shut down")
	}
}

func TestRun_ListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	srv := httpkit.NewServer(ln.Addr().String(), http.NewServeMux())
	if err := httpkit.Run(context.Background(), srv); err == nil {
		t.Fatal("want listen error for already-bound port")
	}
}
