package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arjunshastry16/jobtrack/internal/store"
	"github.com/arjunshastry16/jobtrack/internal/testdb"
)

func TestInTxCommitsAndRollsBack(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	s := store.NewStore(pool)
	ctx := t.Context()

	// Commit on success.
	err := s.InTx(ctx, func(q store.Querier) error {
		_, err := q.CreateCompany(ctx, store.CreateCompanyParams{Name: "Kept"})
		return err
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}

	// Roll back on error: the insert inside must disappear.
	boom := errors.New("boom")
	err = s.InTx(ctx, func(q store.Querier) error {
		if _, err := q.CreateCompany(ctx, store.CreateCompanyParams{Name: "Discarded by error"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx error = %v, want boom", err)
	}

	// Roll back on panic, and let the panic continue.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic inside InTx was swallowed")
			}
		}()
		_ = s.InTx(ctx, func(q store.Querier) error {
			_, _ = q.CreateCompany(ctx, store.CreateCompanyParams{Name: "Discarded by panic"})
			panic("boom")
		})
	}()

	got, err := s.ListCompanies(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Kept" {
		t.Errorf("companies = %+v, want only \"Kept\"", got)
	}
}

func TestApplicationEventsAreAppendOnly(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	s := store.NewStore(pool)
	ctx := context.Background()

	co, err := s.CreateCompany(ctx, store.CreateCompanyParams{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	appID, err := s.CreateApplication(ctx, store.CreateApplicationParams{CompanyID: co.ID, Role: "SWE", Status: "applied"})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := s.CreateApplicationEvent(ctx, store.CreateApplicationEventParams{ApplicationID: appID, ToStatus: "applied"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = pool.Exec(ctx, "UPDATE application_events SET note = 'rewritten' WHERE id = $1", ev.ID)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("UPDATE error = %v, want the append-only trigger to refuse it", err)
	}

	// Deleting the application still removes its history (ON DELETE CASCADE).
	if _, err := s.DeleteApplication(ctx, appID); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListApplicationEvents(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("%d events left after deleting the application, want 0", len(events))
	}
}
