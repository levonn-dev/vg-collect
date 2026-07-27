// The social service: follows, likes, comments, and the activity
// feed. `social migrate` runs schema migrations and exits (init
// container mode).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	vgotel "github.com/levonn-dev/vg-collect/libs/go/otel"
	"github.com/levonn-dev/vg-collect/libs/go/pgkit"
	"github.com/levonn-dev/vg-collect/services/social/internal/collectionclient"
	"github.com/levonn-dev/vg-collect/services/social/internal/config"
	"github.com/levonn-dev/vg-collect/services/social/internal/server"
	"github.com/levonn-dev/vg-collect/services/social/internal/store"
	"github.com/levonn-dev/vg-collect/services/social/internal/userclient"
	"github.com/levonn-dev/vg-collect/services/social/migrations"
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

	shutdown, err := vgotel.Setup(ctx, vgotel.Config{ServiceName: "social", Version: cfg.Version})
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	pool, err := pgkit.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	col, err := collectionclient.New(cfg.CollectionServiceURL)
	if err != nil {
		return err
	}
	users, err := userclient.New(cfg.UserServiceURL)
	if err != nil {
		return err
	}
	v := jwtauth.NewValidator(cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)
	h := server.New(store.New(pool), col, users, server.Options{
		Logger:      slog.Default(),
		CapComments: cfg.CapComments24h,
		CapFollows:  cfg.CapFollows24h,
		CapLikes:    cfg.CapLikes24h,
	})
	router := server.NewRouter(h, v, slog.Default(),
		func(c context.Context) error { return pgkit.Health(c, pool) })

	srv := httpkit.NewServer(cfg.HTTPAddr, router)
	defer func() { _ = srv.Close() }() // idempotent after Run; closes on every exit path
	slog.Info("social service listening", "addr", cfg.HTTPAddr)
	return httpkit.Run(ctx, srv)
}
