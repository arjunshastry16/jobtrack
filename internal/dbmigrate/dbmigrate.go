// Package dbmigrate applies the embedded schema migrations. It is shared by the
// `jobtrack migrate` command and by tests, so both always run exactly the same migrations.
package dbmigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"github.com/arjunshastry16/jobtrack/migrations"
)

// NewProvider returns a goose provider for the embedded migrations.
func NewProvider(db *sql.DB) (*goose.Provider, error) {
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	return p, nil
}

// Up applies all pending migrations to the database at url.
func Up(ctx context.Context, url string) error {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	p, err := NewProvider(db)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Usage describes the migrate command.
const Usage = "usage: jobtrack migrate up|down|status"

// Command runs `migrate up|down|status` against the database at url, writing what it
// did to out:
//
//	up       apply all pending migrations
//	down     roll back the most recent migration
//	status   list migrations and whether each is applied
func Command(ctx context.Context, args []string, url string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New(Usage)
	}
	switch args[0] {
	case "up", "down", "status":
	default:
		return fmt.Errorf("unknown migrate command %q (%s)", args[0], Usage)
	}
	if url == "" {
		return errors.New("JOBTRACK_DATABASE_URL is required")
	}

	db, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	provider, err := NewProvider(db)
	if err != nil {
		return err
	}

	switch args[0] {
	case "up":
		results, err := provider.Up(ctx)
		for _, r := range results {
			_, _ = fmt.Fprintln(out, r)
		}
		if err != nil {
			return fmt.Errorf("up: %w", err)
		}
		if len(results) == 0 {
			_, _ = fmt.Fprintln(out, "no pending migrations")
		}
	case "down":
		r, err := provider.Down(ctx)
		if err != nil {
			return fmt.Errorf("down: %w", err)
		}
		_, _ = fmt.Fprintln(out, r)
	default: // status
		statuses, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("status: %w", err)
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "VERSION\tSTATE\tAPPLIED AT\tFILE")
		for _, s := range statuses {
			applied := "-"
			if !s.AppliedAt.IsZero() {
				applied = s.AppliedAt.Local().Format(time.DateTime)
			}
			_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", s.Source.Version, s.State, applied, s.Source.Path)
		}
		return tw.Flush()
	}
	return nil
}
