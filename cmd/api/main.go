// Command api runs the jobtrack HTTP service.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/config"
	"github.com/arjunshastry16/jobtrack/internal/db"
	"github.com/arjunshastry16/jobtrack/internal/httpapi"
	"github.com/arjunshastry16/jobtrack/internal/store"
)

// version is overwritten at build time with -ldflags "-X main.version=<git sha>".
var version = "dev"

func main() {
	// ctx is cancelled on Ctrl-C (SIGINT) or `docker stop` (SIGTERM), which starts a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Getenv, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jobtrack:", err)
		os.Exit(1)
	}
}

// run holds the real program. Taking its dependencies as arguments (instead of
// reading globals like os.Getenv directly) is what lets main_test.go run it.
func run(ctx context.Context, getenv func(string) string, logOut io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(logOut, &slog.HandlerOptions{Level: cfg.LogLevel}))

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// Runs when run returns, after the HTTP server has stopped using the pool.
	defer pool.Close()

	srv := &http.Server{
		Handler: httpapi.NewRouter(httpapi.Deps{Version: version, DB: pool, Store: store.New(pool), Logger: logger}),
		// Timeouts stop slow or stuck clients from holding connections open forever.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	// Listening separately from serving lets us log the real address (useful with port 0 in tests).
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}
	logger.Info("http server listening", "addr", ln.Addr().String(), "version", version)

	// Serve blocks, so it runs in its own goroutine and reports back on a channel.
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		// Serve only returns early on a real failure.
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down", "timeout", cfg.ShutdownTimeout.String())
	// ctx is already cancelled, so derive one that isn't but still has a deadline.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()

	// Shutdown stops accepting new connections and waits for in-flight requests to finish.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	logger.Info("stopped cleanly")
	return nil
}
