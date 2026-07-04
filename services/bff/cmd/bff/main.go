// The bff: the only public vg-collect service. It owns the browser
// session (sealed cookie, refresh, denylist, origin checks) and serves
// the SPA bundle in cluster; every other service stays internal
// behind it.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	vgotel "github.com/levonn-dev/vg-collect/libs/go/otel"
	"github.com/levonn-dev/vg-collect/libs/go/valkeykit"
	"github.com/levonn-dev/vg-collect/services/bff/internal/authclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/cache"
	"github.com/levonn-dev/vg-collect/services/bff/internal/config"
	"github.com/levonn-dev/vg-collect/services/bff/internal/enrichmentclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/server"
	"github.com/levonn-dev/vg-collect/services/bff/internal/session"
	"github.com/levonn-dev/vg-collect/services/bff/internal/static"
	"github.com/levonn-dev/vg-collect/services/bff/internal/userclient"
)

// The server package defines its dependency surfaces; prove the real
// implementations satisfy them.
var (
	_ server.SessionCache  = (*cache.Cache)(nil)
	_ server.AuthAPI       = (*authclient.Client)(nil)
	_ server.UserAPI       = (*userclient.Client)(nil)
	_ server.EnrichmentAPI = (*enrichmentclient.Client)(nil)
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	shutdown, err := vgotel.Setup(ctx, vgotel.Config{ServiceName: "bff", Version: cfg.Version})
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	// Valkey is required at startup (a deploy-ordering fact); runtime
	// outages fail open per-request instead.
	var rdb *redis.Client
	if cfg.ValkeyCAFile != "" {
		rdb, err = valkeykit.ConnectTLS(ctx, cfg.ValkeyURL, cfg.ValkeyCAFile)
	} else {
		rdb, err = valkeykit.Connect(ctx, cfg.ValkeyURL)
	}
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	codec, err := session.NewCodec(cfg.CookieKey, cfg.CookieSecure)
	if err != nil {
		return err
	}
	auth, err := authclient.New(cfg.AuthServiceURL)
	if err != nil {
		return err
	}
	users, err := userclient.New(cfg.UserServiceURL)
	if err != nil {
		return err
	}
	enrichment, err := enrichmentclient.New(cfg.EnrichmentServiceURL)
	if err != nil {
		return err
	}

	h := server.New(codec, cache.New(rdb), auth, users, enrichment, server.Options{
		AccessTokenTTL: cfg.AccessTokenTTL,
		RefreshWindow:  cfg.RefreshWindow,
		MeCacheTTL:     cfg.MeCacheTTL,
		PublicOrigins:  cfg.PublicOrigins,
		Logger:         slog.Default(),
	})

	var staticHandler http.Handler
	if cfg.ServeStatic {
		staticHandler = static.Handler()
	}
	router := server.NewRouter(h, staticHandler, slog.Default())

	srv := httpkit.NewServer(cfg.HTTPAddr, router)
	defer func() { _ = srv.Close() }() // idempotent after Run; closes on every exit path
	slog.Info("bff listening", "addr", cfg.HTTPAddr, "serve_static", cfg.ServeStatic)
	return httpkit.Run(ctx, srv)
}
