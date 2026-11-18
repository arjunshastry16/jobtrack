package httpapi

import (
	"math"
	"net/http"
	"testing"
	"time"
)

func getStatsJSON(t *testing.T, h http.Handler) statsJSON {
	t.Helper()
	var s statsJSON
	wantStatus(t, call(t, h, http.MethodGet, "/api/v1/stats", nil, &s), http.StatusOK)
	return s
}

func TestStatsEmpty(t *testing.T) {
	t.Parallel()
	s := getStatsJSON(t, newDBRouter(t))
	if s.Applications != 0 || s.Responded != 0 {
		t.Errorf("stats = %+v, want zeros", s)
	}
	// Every stage is there even with no data, so a chart has its axes.
	if len(s.ByStatus) != 8 || len(s.Funnel) != 5 || len(s.TimeInStage) != 6 || len(s.Exits) != 6 {
		t.Fatalf("list lengths = %d %d %d %d, want 8 5 6 6", len(s.ByStatus), len(s.Funnel), len(s.TimeInStage), len(s.Exits))
	}
	for _, st := range s.TimeInStage {
		if st.Stays != 0 || st.MedianDays != nil || st.P90Days != nil {
			t.Errorf("time in %s = %+v, want no stays and null times", st.Stage, st)
		}
	}
}

func TestStats(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	base := time.Now().Add(-60 * 24 * time.Hour).Truncate(time.Second)
	day := func(n int) string { return base.Add(time.Duration(n) * 24 * time.Hour).Format(time.RFC3339) }

	// history adds an application that starts in the first status on day 0 and then
	// makes each move on the given day.
	type move struct {
		to  string
		day int
	}
	history := func(start string, moves ...move) {
		t.Helper()
		app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE", "status": start, "occurred_at": day(0)})
		for _, m := range moves {
			wantStatus(t, call(t, h, http.MethodPost, transitionPath(app.ID), map[string]any{"to": m.to, "occurred_at": day(m.day)}, nil), http.StatusCreated)
		}
	}
	history("applied", move{"screening", 4}, move{"interviewing", 10}, move{"offer", 20}) // still at offer
	history("applied", move{"rejected", 6})
	history("applied", move{"interviewing", 2}, move{"interviewing", 5}, move{"rejected", 9}) // skipped screening
	history("applied")                                                                        // waiting
	history("wishlist", move{"applied", 3}, move{"withdrawn", 4})
	history("wishlist")
	history("rejected") // added after the fact: no earlier stage

	s := getStatsJSON(t, h)
	if s.Applications != 7 {
		t.Errorf("applications = %d, want 7", s.Applications)
	}
	wantByStatus := map[string]int64{"wishlist": 1, "applied": 1, "offer": 1, "rejected": 3, "withdrawn": 1}
	for _, c := range s.ByStatus {
		if c.Applications != wantByStatus[c.Status] {
			t.Errorf("by_status %s = %d, want %d", c.Status, c.Applications, wantByStatus[c.Status])
		}
	}

	wantFunnel := []funnelStep{{"applied", 5}, {"screening", 2}, {"interviewing", 2}, {"offer", 1}, {"accepted", 0}}
	if len(s.Funnel) != len(wantFunnel) {
		t.Fatalf("funnel = %+v", s.Funnel)
	}
	for i, f := range wantFunnel {
		if s.Funnel[i] != f {
			t.Errorf("funnel[%d] = %+v, want %+v", i, s.Funnel[i], f)
		}
	}
	// Heard back: the offer, and the two rejections. Not the one waiting, not the one
	// withdrawn, not the one added as rejected without applying.
	if s.Responded != 3 {
		t.Errorf("responded = %d, want 3", s.Responded)
	}

	// Finished stays, in days. Stays still going (the offer, the waiting application,
	// the wishlist one) aren't counted.
	type times struct {
		stays           int64
		median, p90     float64
		noTimesExpected bool
	}
	wantTimes := map[string]times{
		"wishlist":     {stays: 1, median: 3, p90: 3},
		"applied":      {stays: 4, median: 3, p90: 5.4}, // 1 2 4 6
		"screening":    {stays: 1, median: 6, p90: 6},   // 4 -> 10
		"interviewing": {stays: 3, median: 4, p90: 8.8}, // rounds of 3, 4 and 10 days
		"offer":        {noTimesExpected: true},
		"accepted":     {noTimesExpected: true},
	}
	for _, st := range s.TimeInStage {
		w, ok := wantTimes[st.Stage]
		if !ok {
			t.Errorf("unexpected stage %q in time_in_stage", st.Stage)
			continue
		}
		if w.noTimesExpected {
			if st.Stays != 0 || st.MedianDays != nil || st.P90Days != nil {
				t.Errorf("time in %s = %+v, want none", st.Stage, st)
			}
			continue
		}
		if st.Stays != w.stays || st.MedianDays == nil || st.P90Days == nil ||
			math.Abs(*st.MedianDays-w.median) > 1e-6 || math.Abs(*st.P90Days-w.p90) > 1e-6 {
			t.Errorf("time in %s = %d stays, median %v, p90 %v; want %d, %v, %v",
				st.Stage, st.Stays, deref64(st.MedianDays), deref64(st.P90Days), w.stays, w.median, w.p90)
		}
	}

	wantExits := map[string]stageExits{
		"applied":      {Stage: "applied", Rejected: 1, Withdrawn: 1},
		"interviewing": {Stage: "interviewing", Rejected: 1},
	}
	for _, e := range s.Exits {
		want := wantExits[e.Stage]
		want.Stage = e.Stage
		if e != want {
			t.Errorf("exits from %s = %+v, want %+v", e.Stage, e, want)
		}
	}
}

func deref64(f *float64) float64 {
	if f == nil {
		return math.NaN()
	}
	return *f
}
