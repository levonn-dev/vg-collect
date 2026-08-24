// The auth service: OIDC relying party and first-party token issuer.
// `auth migrate` runs schema migrations and exits (init container mode).
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
	"github.com/levonn-dev/vgkeep/services/auth/internal/config"
	"github.com/levonn-dev/vgkeep/services/auth/internal/oidc"
	"github.com/levonn-dev/vgkeep/services/auth/internal/server"
	"github.com/levonn-dev/vgkeep/services/auth/internal/store"
	"github.com/levonn-dev/vgkeep/services/auth/internal/token"
	"github.com/levonn-dev/vgkeep/services/auth/internal/userclient"
	"github.com/levonn-dev/vgkeep/services/auth/migrations"
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

	shutdown, err := vgotel.Setup(ctx, vgotel.Config{ServiceName: "auth", Version: cfg.Version})
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	pool, err := pgkit.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	minter, err := token.NewMinter(cfg.JWTSigningKey, cfg.JWTIssuer, cfg.JWTAudience, cfg.AccessTokenTTL)
	if err != nil {
		return err
	}
	st := store.New(pool)
	// Idempotent: the kid is derived from the key, so every replica
	// registers the same row and the JWKS serves it.
	if err := st.RegisterSigningKey(ctx, minter.Kid(), minter.PublicKey()); err != nil {
		return err
	}

	users, err := userclient.New(cfg.UserServiceURL, minter)
	if err != nil {
		return err
	}
	verifier := jwtauth.NewValidator(cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)

	providers := map[string]oidc.Provider{}
	if cfg.GoogleEnabled() {
		providers["google"] = oidc.NewGoogle(
			cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.OAuthRedirectURL, cfg.GoogleIssuerURL)
	}
	if cfg.TwitchEnabled() {
		providers["twitch"] = oidc.NewTwitch(
			cfg.TwitchClientID, cfg.TwitchClientSecret, cfg.OAuthRedirectURL, cfg.TwitchIssuerURL)
	}
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	slog.Info("auth providers", "real", names, "dev", cfg.DevProviderEnabled, "kid", minter.Kid())

	h := server.New(st, minter, users, providers, verifier, cfg.DevProviderEnabled, cfg.RefreshTokenTTL, cfg.InternalServiceSecrets, slog.Default())
	router, err := server.NewRouter(h, slog.Default(),
		func(c context.Context) error { return pgkit.Health(c, pool) })
	if err != nil {
		return err
	}

	srv := httpkit.NewServer(cfg.HTTPAddr, router)
	defer func() { _ = srv.Close() }() // idempotent after Run; closes on every exit path
	slog.Info("auth service listening", "addr", cfg.HTTPAddr)
	return httpkit.Run(ctx, srv)
}
