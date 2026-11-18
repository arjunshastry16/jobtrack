package httpapi

import (
	"net/http"

	"github.com/arjunshastry16/jobtrack/internal/stages"
)

type statusCount struct {
	Status       string `json:"status"`
	Applications int64  `json:"applications"`
}

type funnelStep struct {
	Stage   string `json:"stage"`
	Reached int64  `json:"reached"` // applications that got to this stage or a later one
}

type stageTime struct {
	Stage string `json:"stage"`
	Stays int64  `json:"stays"` // finished stays measured; the times are null when there are none
	// MedianDays and P90Days: half of the stays took at most MedianDays, nine in ten at most P90Days.
	MedianDays *float64 `json:"median_days"`
	P90Days    *float64 `json:"p90_days"`
}

type stageExits struct {
	Stage     string `json:"stage"`
	Rejected  int64  `json:"rejected"`
	Withdrawn int64  `json:"withdrawn"`
}

// statsJSON is the job search at a glance. Every list has an entry for each stage it
// covers, in pipeline order, zeros included, so a client can chart it as is.
type statsJSON struct {
	Applications int64         `json:"applications"`
	ByStatus     []statusCount `json:"by_status"`
	Funnel       []funnelStep  `json:"funnel"`
	// Responded is how many of the applications that reached "applied" heard back.
	Responded   int64        `json:"responded"`
	TimeInStage []stageTime  `json:"time_in_stage"`
	Exits       []stageExits `json:"exits"`
}

// getStats returns funnel counts, response rate, time in each stage and where
// applications end, all computed in SQL from the stage history.
//
//	GET /api/v1/stats
func (a *api) getStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var out statsJSON

	byStatus, err := a.store.StatsByStatus(ctx)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	counts := map[string]int64{}
	for _, row := range byStatus {
		counts[row.Status] = row.Applications
		out.Applications += row.Applications
	}
	for _, s := range stages.All {
		out.ByStatus = append(out.ByStatus, statusCount{Status: s, Applications: counts[s]})
	}

	funnel, err := a.store.StatsFunnel(ctx, stages.Pipeline)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	for _, row := range funnel {
		out.Funnel = append(out.Funnel, funnelStep{Stage: row.Stage, Reached: row.Reached})
	}

	if out.Responded, err = a.store.StatsResponded(ctx, stages.Pipeline); err != nil {
		a.internalError(w, r, err)
		return
	}

	times, err := a.store.StatsTimeInStage(ctx)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	exits, err := a.store.StatsExits(ctx)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	// Stays and exits are per stage an application can leave: every status but the
	// final ones.
	byStage := map[string]stageTime{}
	for _, row := range times {
		byStage[row.Stage] = stageTime{Stage: row.Stage, Stays: row.Stays, MedianDays: &row.MedianDays, P90Days: &row.P90Days}
	}
	exitsByStage := map[string]stageExits{}
	for _, row := range exits {
		e := exitsByStage[row.Stage]
		switch row.Outcome {
		case stages.Rejected:
			e.Rejected = row.Applications
		case stages.Withdrawn:
			e.Withdrawn = row.Applications
		}
		exitsByStage[row.Stage] = e
	}
	for _, s := range stages.All {
		if stages.Terminal(s) {
			continue
		}
		t, ok := byStage[s]
		if !ok {
			t = stageTime{Stage: s}
		}
		out.TimeInStage = append(out.TimeInStage, t)
		e := exitsByStage[s]
		e.Stage = s
		out.Exits = append(out.Exits, e)
	}

	writeJSON(w, http.StatusOK, out)
}
