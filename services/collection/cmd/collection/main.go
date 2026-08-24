// The collection service: the granular CRUD core of vgkeep
// (entries, tags, saved views, backlog ordering, dashboard).
// `collection migrate` runs schema migrations and exits (init
// container mode).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/services/collection/internal/cache"
	"github.com/levonn-dev/vgkeep/services/collection/internal/config"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/server"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
	"github.com/levonn-dev/vgkeep/services/collection/migrations"
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

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		return pgkit.Migrate(cfg.DatabaseURL, migrations.FS, ".")
	}

	shutdown, err := vgotel.Setup(ctx, vgotel.Config{ServiceName: "collection", Version: cfg.Version})
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	pool, err := pgkit.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Valkey is required at startup (deploy-ordering); runtime outages fail open.
	rdb, err := valkeykit.ConnectFromConfig(ctx, cfg.ValkeyURL, cfg.ValkeyCAFile)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	enrich, err := enrichmentclient.New(cfg.EnrichmentServiceURL)
	if err != nil {
		return err
	}

	v := jwtauth.NewValidator(cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)
	h := server.New(store.New(pool), enrich, cache.New(rdb), server.Options{
		DashboardCacheTTL: cfg.DashboardCacheTTL,
		Logger:            slog.Default(),
	})
	router, err := server.NewRouter(h, v, slog.Default(),
		func(c context.Context) error { return pgkit.Health(c, pool) })
	if err != nil {
		return err
	}

	srv := httpkit.NewServer(cfg.HTTPAddr, router)
	defer func() { _ = srv.Close() }() // idempotent after Run; closes on every exit path
	slog.Info("collection service listening", "addr", cfg.HTTPAddr)
	return httpkit.Run(ctx, srv)
}
