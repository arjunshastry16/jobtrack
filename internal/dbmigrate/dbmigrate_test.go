package dbmigrate_test

import (
	"bytes"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/arjunshastry16/jobtrack/internal/dbmigrate"
	"github.com/arjunshastry16/jobtrack/internal/testdb"
)

// TestCommand runs migrate status, down and up against a migrated test database.
func TestCommand(t *testing.T) {
	pool := testdb.New(t)
	u, err := url.Parse(os.Getenv(testdb.EnvURL))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + pool.Config().ConnConfig.Database
	dbURL := u.String()

	run := func(cmd string) string {
		t.Helper()
		var out bytes.Buffer
		if err := dbmigrate.Command(t.Context(), []string{cmd}, dbURL, &out); err != nil {
			t.Fatalf("migrate %s: %v", cmd, err)
		}
		return out.String()
	}

	if out := run("up"); !strings.Contains(out, "no pending migrations") {
		t.Errorf("up on a migrated database printed %q, want no pending migrations", out)
	}
	status := run("status")
	if !strings.HasPrefix(status, "VERSION") || strings.Contains(status, "pending") {
		t.Errorf("status on a migrated database:\n%s\nwant a table with everything applied", status)
	}
	rows := strings.Split(strings.TrimSpace(status), "\n")
	latest := strings.Fields(rows[len(rows)-1])[0] // the last row's VERSION

	if out := run("down"); !strings.Contains(out, latest) {
		t.Errorf("down printed %q, want it to roll back version %s", out, latest)
	}
	if out := run("status"); !strings.Contains(out, "pending") {
		t.Errorf("status after down:\n%s\nwant the latest migration pending", out)
	}
	if out := run("up"); !strings.Contains(out, latest) {
		t.Errorf("up printed %q, want it to re-apply version %s", out, latest)
	}
}
