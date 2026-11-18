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
	"sync"
	"syscall"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/boards"
	"github.com/arjunshastry16/jobtrack/internal/config"
	"github.com/arjunshastry16/jobtrack/internal/db"
	"github.com/arjunshastry16/jobtrack/internal/httpapi"
	"github.com/arjunshastry16/jobtrack/internal/ingest"
	"github.com/arjunshastry16/jobtrack/internal/jobs"
	"github.com/arjunshastry16/jobtrack/internal/notify"
	"github.com/arjunshastry16/jobtrack/internal/reminders"
	"github.com/arjunshastry16/jobtrack/internal/requestid"
	"github.com/arjunshastry16/jobtrack/internal/scheduler"
	"github.com/arjunshastry16/jobtrack/internal/store"
	"github.com/arjunshastry16/jobtrack/internal/webui"
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

	// LogHandler adds request_id to every line logged with a request context.
	logger := slog.New(requestid.LogHandler{Handler: slog.NewJSONHandler(logOut, &slog.HandlerOptions{Level: cfg.LogLevel})})

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// Runs when run returns, after the HTTP server and background work have stopped using
	// the pool (deferred calls run last-in, first-out).
	defer pool.Close()
	st := store.NewStore(pool)

	notifier, err := newNotifier(cfg, logger)
	if err != nil {
		return err
	}

	// Background work: a job worker, plus a scheduler that enqueues jobs.
	queue := jobs.New(st)
	worker := jobs.NewWorker(queue, logger, jobs.Config{
		// Board fetches can take minutes (most of that waiting, politely), so there are
		// enough slots that they don't hold up reminders.
		Concurrency: 4,
		// Jobs still running at shutdown get as long as HTTP requests do.
		DrainTimeout: cfg.ShutdownTimeout,
	})
	rem := reminders.NewService(st, queue, notifier, logger, reminders.Config{
		AfterDays: cfg.ReminderAfterDays, StartHour: cfg.ReminderStartHour, EndHour: cfg.ReminderEndHour,
	})
	rem.Register(worker)
	ing := ingest.NewService(st, queue, boards.All(nil), logger, ingest.Config{SyncHour: cfg.SyncHour})
	ing.Register(worker)
	sched := scheduler.New(logger, rem.ScanTask(time.Hour), ing.SyncTask(time.Hour))

	// bgCtx is cancelled by shutdown (it derives from ctx) or, on an early error return,
	// by the deferred stopBackground. Either way we wait for the worker to drain before
	// the pool is closed.
	bgCtx, stopBackground := context.WithCancel(ctx)
	var background sync.WaitGroup
	defer func() {
		stopBackground()
		background.Wait()
	}()
	background.Go(func() { worker.Run(bgCtx) })
	background.Go(func() { sched.Run(bgCtx) })

	if cfg.AuthOff {
		logger.Warn("JOBTRACK_AUTH=off: every request is let in without a Tailscale login (local development only)")
	}
	srv := &http.Server{
		Handler: httpapi.NewRouter(httpapi.Deps{
			Version: version, DB: pool, Store: st, Logger: logger, UI: webui.Files(),
			Auth: httpapi.Auth{Logins: cfg.AllowedLogins, Off: cfg.AuthOff},
		}),
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
	logger.Info("http server listening", "addr", ln.Addr().String(), "version", version, "web_ui", webui.Files() != nil)

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
	// The worker started draining when ctx was cancelled, alongside the HTTP shutdown.
	stopBackground()
	background.Wait()
	logger.Info("stopped cleanly")
	return nil
}

// newNotifier returns the ntfy notifier, or one that only logs if no topic is configured.
func newNotifier(cfg config.Config, logger *slog.Logger) (notify.Notifier, error) {
	if cfg.NtfyURL == "" {
		logger.Warn("JOBTRACK_NTFY_URL not set; reminders will only be logged")
		return notify.Log{Logger: logger}, nil
	}
	n, err := notify.NewNtfy(cfg.NtfyURL, cfg.NtfyToken, nil)
	if err != nil {
		return nil, fmt.Errorf("JOBTRACK_NTFY_URL: %w", err)
	}
	return n, nil
}
