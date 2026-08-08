// The enrichment service: the catalog + pricing quarantine for all
// third-party data (IGDB metadata, PriceCharting prices).
// `enrichment migrate` runs schema migrations and exits (init
// container mode).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/cache"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/config"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/db"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/fx"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/server"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
	"github.com/levonn-dev/vgkeep/services/enrichment/migrations"
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
	mongoURL, err := db.ComposeURL(cfg.MongoURL, cfg.MongoUsername, cfg.MongoPassword)
	if err != nil {
		return err
	}

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		return db.Migrate(ctx, mongoURL, cfg.MongoDB, migrations.FS, ".")
	}

	shutdown, err := vgotel.Setup(ctx, vgotel.Config{ServiceName: "enrichment", Version: cfg.Version})
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	client, err := db.Connect(ctx, mongoURL)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

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

	var games server.GameProvider
	if cfg.IGDBMode == "real" {
		games = igdb.NewClient(cfg.IGDBClientID, cfg.IGDBClientSecret)
	} else {
		games, err = igdb.NewStub()
		if err != nil {
			return err
		}
	}
	var prices server.PriceProvider
	if cfg.PriceChartingMode == "real" {
		prices = pricecharting.NewClient(cfg.PriceChartingAPIKey)
	} else {
		prices, err = pricecharting.NewStub()
		if err != nil {
			return err
		}
	}
	var rates server.FXProvider
	if cfg.FXMode == "real" {
		rates = fx.NewClient()
	} else {
		rates, err = fx.NewStub()
		if err != nil {
			return err
		}
	}

	st := store.New(client.Database(cfg.MongoDB))
	v := jwtauth.NewValidator(cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)
	h := server.New(st, games, prices, rates, cache.New(rdb), server.Options{
		SearchCacheTTL:   cfg.SearchCacheTTL,
		ProductCacheTTL:  cfg.ProductCacheTTL,
		IGDBRefreshAfter: cfg.IGDBRefreshAfter,
		Logger:           slog.Default(),
	})
	// Readiness = Mongo only: the catalog is a hard dependency, the
	// cache fails open per-request.
	router := server.NewRouter(h, v, slog.Default(),
		func(c context.Context) error { return db.Health(c, client) })

	srv := httpkit.NewServer(cfg.HTTPAddr, router)
	defer func() { _ = srv.Close() }() // idempotent after Run; closes on every exit path
	slog.Info("enrichment service listening", "addr", cfg.HTTPAddr,
		"igdb_mode", cfg.IGDBMode, "pricecharting_mode", cfg.PriceChartingMode)
	return httpkit.Run(ctx, srv)
}
