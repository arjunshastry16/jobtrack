// Package testdb gives each integration test its own freshly migrated Postgres
// database, so tests never see each other's rows and can run in parallel.
//
// Tests need JOBTRACK_TEST_DATABASE_URL pointing at a Postgres server where the user
// may CREATE DATABASE (locally: the dev database from `make db-up`; in CI: a service
// container). Without it, tests are skipped locally but fail in CI, so they can never
// be skipped by accident there.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arjunshastry16/jobtrack/internal/dbmigrate"
)

// EnvURL names the variable holding the admin connection string for tests.
const EnvURL = "JOBTRACK_TEST_DATABASE_URL"

// New creates a uniquely named database, applies all migrations, and returns a pool
// connected to it. The database is dropped when the test finishes.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()

	adminURL := os.Getenv(EnvURL)
	if adminURL == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s must be set in CI", EnvURL)
		}
		t.Skipf("set %s to run database tests (see .env.example)", EnvURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "jt_test_" + randomSuffix(t)
	ident := pgx.Identifier{name}.Sanitize() // quoted, so it is safe to put in SQL
	// The admin connection is only held while creating (and later dropping) the
	// database: with many tests running in parallel, an idle connection per test adds up,
	// and the Pi's dev server allows only 30.
	err := adminExec(ctx, adminURL,
		"CREATE DATABASE "+ident,
		// Throwaway database: don't wait for each commit to reach the disk. On the Pi's USB
		// HDD that wait is ~25ms per commit and dominated test time. Never do this for real data.
		"ALTER DATABASE "+ident+" SET synchronous_commit = off",
	)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvURL, err)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 4

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	// Registered before migrating, so the database is dropped even if migration fails.
	t.Cleanup(func() {
		pool.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		if err := adminExec(dropCtx, adminURL, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})

	// Note: cfg.ConnString() would return the original admin URL, so build the test DB's URL.
	if err := dbmigrate.Up(ctx, withDatabase(t, adminURL, name)); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return pool
}

// adminExec runs statements on a short-lived admin connection.
func adminExec(ctx context.Context, adminURL string, statements ...string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect to test server: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	for _, sql := range statements {
		if _, err := conn.Exec(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}

// withDatabase returns rawURL with its database name replaced.
func withDatabase(t *testing.T, rawURL, database string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvURL, err)
	}
	u.Path = "/" + database
	return u.String()
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
