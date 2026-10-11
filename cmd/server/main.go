// Command server runs the service scheduler HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pyaethu-aung/service-scheduler/internal/api"
	"github.com/pyaethu-aung/service-scheduler/internal/config"
)

// shutdownTimeout bounds how long in-flight requests may drain after SIGTERM.
const shutdownTimeout = 15 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := runUntilSignal(os.Getenv, logger, nil); err != nil {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}

// runUntilSignal runs the server until SIGINT or SIGTERM, then shuts it down
// gracefully.
func runUntilSignal(getenv func(string) string, logger *slog.Logger, ready chan<- net.Addr) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, getenv, logger, ready)
}

// run serves HTTP until ctx is cancelled, then drains in-flight requests.
// When ready is non-nil it receives the bound address once the listener is up.
func run(ctx context.Context, getenv func(string) string, logger *slog.Logger, ready chan<- net.Addr) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}

	// pgxpool connects lazily, so startup does not wait on the database.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database pool: %w", err)
	}
	defer pool.Close()

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	srv := &http.Server{
		Handler:           api.NewRouter(&api.Server{}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	logger.InfoContext(ctx, "listening", "addr", ln.Addr().String())
	if ready != nil {
		ready <- ln.Addr()
	}

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	logger.InfoContext(ctx, "shutting down", "timeout", shutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
