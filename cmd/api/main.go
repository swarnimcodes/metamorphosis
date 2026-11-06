// Command api is the Metamorph HTTP API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/smarniw/metamorphosis/internal/config"
	"github.com/smarniw/metamorphosis/internal/google"
	"github.com/smarniw/metamorphosis/internal/httpapi"
	"github.com/smarniw/metamorphosis/internal/migrate"
	"github.com/smarniw/metamorphosis/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	_ = godotenv.Load()
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	// The privacy policy promises the server logs only what matters: at the default level there is no request
	// logging, and handlers never log bodies, emails or tokens.
	logOpts := &slog.HandlerOptions{Level: cfg.LogLevel}
	var logHandler slog.Handler = slog.NewTextHandler(os.Stderr, logOpts)
	if cfg.LogJSON {
		logHandler = slog.NewJSONHandler(os.Stderr, logOpts)
	}
	slog.SetDefault(slog.New(logHandler))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	poolCfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}

	if cfg.RunMigrations {
		if err := migrate.Run(ctx, pool, migrations.FS); err != nil {
			return err
		}
		slog.Info("database migrations are up to date")
	}
	if cfg.DevLogin {
		slog.Warn("DEV_LOGIN is enabled: POST /auth/dev-login issues tokens without credentials")
	}

	deps := httpapi.Deps{DB: pool, Cfg: cfg}
	if len(cfg.GoogleClientIDs) > 0 {
		deps.Google = google.NewRemote(cfg.GoogleClientIDs, google.JWKSURL)
		slog.Info("Google sign-in enabled")
	} else {
		slog.Info("Google sign-in disabled (GOOGLE_CLIENT_IDS unset)")
	}

	httpSrv := &http.Server{Addr: cfg.BindAddr, Handler: httpapi.NewHandler(deps), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	slog.Info("listening", "addr", cfg.BindAddr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
