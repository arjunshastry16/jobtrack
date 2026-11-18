// Command api is the jobtrack binary (built as bin/jobtrack, and the container image's
// entrypoint):
//
//	jobtrack                         run the HTTP service and background work
//	jobtrack migrate up|down|status  apply, roll back or list database migrations
//	jobtrack healthcheck             exit 0 if the running service is ready (for Docker)
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
	_ "time/tzdata" // the time zone database, so TZ (e.g. Asia/Kolkata) works in any image

	"github.com/arjunshastry16/jobtrack/internal/boards"
	"github.com/arjunshastry16/jobtrack/internal/config"
	"github.com/arjunshastry16/jobtrack/internal/db"
	"github.com/arjunshastry16/jobtrack/internal/dbmigrate"
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
	err := dispatch(ctx, os.Args[1:], os.Getenv, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jobtrack:", err)
		os.Exit(1)
	}
}

// dispatch runs the subcommand named by args (none = the service).
func dispatch(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	if len(args) == 0 {
		return run(ctx, getenv, out)
	}
	switch args[0] {
	case "migrate":
		// Only needs the database, not the service's other settings.
		return dbmigrate.Command(ctx, args[1:], getenv("JOBTRACK_DATABASE_URL"), out)
	case "healthcheck":
		if len(args) != 1 {
			return errors.New("usage: jobtrack healthcheck")
		}
		return healthcheck(ctx, getenv("JOBTRACK_ADDR"))
	default:
		return fmt.Errorf("unknown command %q (want no command, migrate or healthcheck)", args[0])
	}
}

// healthcheckTimeout is well under Docker's default 30s health check timeout.
const healthcheckTimeout = 3 * time.Second

// healthcheck asks the service listening on addr whether it's ready (/readyz, which
// needs no login). It's the container's health check: the distroless image has no
// shell or curl to do it with.
func healthcheck(ctx context.Context, addr string) error {
	url, err := readyzURL(addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: %s answered %s", url, resp.Status)
	}
	return nil
}

// readyzURL is the /readyz URL of a server listening on addr (JOBTRACK_ADDR, "" for the
// default). A server listening on every interface (":8090", "0.0.0.0:8090") is asked on
// loopback.
func readyzURL(addr string) (string, error) {
	if addr == "" {
		addr = config.DefaultAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("JOBTRACK_ADDR: %w", err)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/readyz", nil
}

// run holds the real program. Taking its dependencies as arguments (instead of
// reading globals like os.Getenv directly) is what lets main_test.go run it.
func run(ctx context.Context, getenv func(string) string, logOut io.Writer) error {
	// DRILL (step 4.5, reverted straight after): a new required setting that CI and the
	// smoke test set but production's .env doesn't. Config drift: CI passes, the Pi fails.
	if getenv("JOBTRACK_DRILL_SETTING") == "" {
		return errors.New("JOBTRACK_DRILL_SETTING is required")
	}
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
