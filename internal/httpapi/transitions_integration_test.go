package httpapi

import (
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"
)

type transitionResponse struct {
	Application applicationJSON `json:"application"`
	Event       eventJSON       `json:"event"`
}

type timelineResponse struct {
	ApplicationID int64       `json:"application_id"`
	Status        string      `json:"status"`
	Events        []eventJSON `json:"events"`
}

func transitionPath(id int64) string { return appPath(id) + "/transitions" }
func timelinePath(id int64) string   { return appPath(id) + "/timeline" }

func timeline(t *testing.T, h http.Handler, id int64) timelineResponse {
	t.Helper()
	var tl timelineResponse
	wantStatus(t, call(t, h, http.MethodGet, timelinePath(id), nil, &tl), http.StatusOK)
	return tl
}

func TestTransitionsHappyPathAndTimeline(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})

	if !slices.Equal(app.NextStatuses, []string{"screening", "interviewing", "offer", "rejected", "withdrawn"}) {
		t.Errorf("next_statuses after create = %v", app.NextStatuses)
	}

	// Creating the application recorded its first event.
	tl := timeline(t, h, app.ID)
	if len(tl.Events) != 1 || tl.Events[0].FromStatus != nil || tl.Events[0].ToStatus != "applied" {
		t.Fatalf("initial timeline = %+v, want one creation event (null -> applied)", tl.Events)
	}

	steps := []struct{ to, note string }{
		{"screening", "recruiter call"},
		{"interviewing", "round 1: system design"},
		{"interviewing", "round 2: coding"}, // another round is a valid self-transition
		{"offer", ""},
		{"accepted", "start date agreed"},
	}
	for _, s := range steps {
		body := map[string]any{"to": s.to}
		if s.note != "" {
			body["note"] = s.note
		}
		var resp transitionResponse
		wantStatus(t, call(t, h, http.MethodPost, transitionPath(app.ID), body, &resp), http.StatusCreated)
		if resp.Application.Status != s.to || resp.Event.ToStatus != s.to {
			t.Fatalf("after -> %s: application %q, event %q", s.to, resp.Application.Status, resp.Event.ToStatus)
		}
	}

	tl = timeline(t, h, app.ID)
	if tl.Status != "accepted" || len(tl.Events) != 6 {
		t.Fatalf("timeline status %q with %d events, want accepted with 6", tl.Status, len(tl.Events))
	}
	var path []string
	for i, e := range tl.Events {
		path = append(path, e.ToStatus)
		if i > 0 && (e.FromStatus == nil || *e.FromStatus != tl.Events[i-1].ToStatus) {
			t.Errorf("event %d from_status = %v, want %q (chain broken)", i, e.FromStatus, tl.Events[i-1].ToStatus)
		}
	}
	if want := []string{"applied", "screening", "interviewing", "interviewing", "offer", "accepted"}; !slices.Equal(path, want) {
		t.Errorf("timeline = %v, want %v", path, want)
	}
	if tl.Events[3].Note == nil || *tl.Events[3].Note != "round 2: coding" {
		t.Errorf("round 2 note = %v", tl.Events[3].Note)
	}
}

func TestInvalidTransitionChangesNothing(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})

	wantStatus(t, call(t, h, http.MethodPost, transitionPath(app.ID), map[string]any{"to": "rejected"}, nil), http.StatusCreated)

	var errResp errorResponse
	rec := call(t, h, http.MethodPost, transitionPath(app.ID), map[string]any{"to": "offer"}, &errResp)
	wantStatus(t, rec, http.StatusConflict)
	if errResp.Error.Code != codeInvalidTransition {
		t.Errorf("code = %q, want %q (%s)", errResp.Error.Code, codeInvalidTransition, errResp.Error.Message)
	}

	tl := timeline(t, h, app.ID)
	if tl.Status != "rejected" || len(tl.Events) != 2 {
		t.Errorf("after refused move: status %q, %d events; want rejected, 2", tl.Status, len(tl.Events))
	}
}

func TestTransitionValidationAndNotFound(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})

	tests := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"unknown status", map[string]any{"to": "hired"}, "to"},
		{"missing status", map[string]any{}, "to"},
		{"future date", map[string]any{"to": "screening", "occurred_at": time.Now().Add(24 * time.Hour)}, "occurred_at"},
		{"before creation", map[string]any{"to": "screening", "occurred_at": time.Now().Add(-24 * time.Hour)}, "occurred_at"},
	}
	for _, tt := range tests {
		var errResp errorResponse
		wantStatus(t, call(t, h, http.MethodPost, transitionPath(app.ID), tt.body, &errResp), http.StatusUnprocessableEntity)
		if _, ok := errResp.Error.Fields[tt.field]; !ok {
			t.Errorf("%s: fields = %v, want %q", tt.name, errResp.Error.Fields, tt.field)
		}
	}
	if n := len(timeline(t, h, app.ID).Events); n != 1 {
		t.Errorf("%d events after only rejected requests, want 1", n)
	}

	wantStatus(t, call(t, h, http.MethodPost, transitionPath(99999), map[string]any{"to": "screening"}, nil), http.StatusNotFound)
	wantStatus(t, call(t, h, http.MethodGet, timelinePath(99999), nil, nil), http.StatusNotFound)
}

func TestTransitionBackdating(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})

	// Slightly in the future (within clock-skew tolerance) is accepted and stored as given.
	when := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	var resp transitionResponse
	wantStatus(t, call(t, h, http.MethodPost, transitionPath(app.ID),
		map[string]any{"to": "screening", "occurred_at": when}, &resp), http.StatusCreated)
	if !resp.Event.OccurredAt.Equal(when) {
		t.Errorf("occurred_at = %v, want %v", resp.Event.OccurredAt, when)
	}

	// Anything earlier than that event is now refused.
	wantStatus(t, call(t, h, http.MethodPost, transitionPath(app.ID),
		map[string]any{"to": "interviewing", "occurred_at": when.Add(-time.Second)}, nil), http.StatusUnprocessableEntity)
}

// Two requests racing to make the same move: the row lock (SELECT ... FOR UPDATE) makes
// them run one after the other, so exactly one succeeds and the other sees the new
// status and is refused. Without the lock both could read "applied" and both succeed.
func TestConcurrentTransitionsAreSerialized(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})

	const racers = 5
	codes := make([]int, racers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once
			codes[i] = call(t, h, http.MethodPost, transitionPath(app.ID), map[string]any{"to": "screening"}, nil).Code
		}()
	}
	close(start)
	wg.Wait()

	var created, conflicts int
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		}
	}
	if created != 1 || conflicts != racers-1 {
		t.Errorf("status codes = %v, want exactly one 201 and %d 409s", codes, racers-1)
	}
	if n := len(timeline(t, h, app.ID).Events); n != 2 {
		t.Errorf("%d events, want 2 (creation + one transition)", n)
	}
}

func TestDeletingApplicationRemovesTimeline(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})
	wantStatus(t, call(t, h, http.MethodPost, transitionPath(app.ID), map[string]any{"to": "screening"}, nil), http.StatusCreated)

	wantStatus(t, call(t, h, http.MethodDelete, appPath(app.ID), nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodGet, timelinePath(app.ID), nil, nil), http.StatusNotFound)
	// The company is deletable again: no application (or orphaned event) references it.
	wantStatus(t, call(t, h, http.MethodDelete, pathFor(co), nil, nil), http.StatusNoContent)
}
