package metrics_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/arjunshastry16/jobtrack/internal/metrics"
	"github.com/arjunshastry16/jobtrack/internal/store"
)

// The Grafana dashboards are checked here, beside the metrics they read: renaming a
// metric breaks this test instead of leaving a panel that quietly says "No data".
const (
	dashboardsDir  = "../../deploy/observability/grafana/dashboards"
	datasourceFile = "../../deploy/observability/grafana/provisioning/datasources/prometheus.yml"
)

type uidRef struct {
	UID string `json:"uid"`
}

type panel struct {
	ID         int     `json:"id"`
	Type       string  `json:"type"`
	Title      string  `json:"title"`
	Datasource *uidRef `json:"datasource"`
	GridPos    struct {
		X int `json:"x"`
		Y int `json:"y"`
		W int `json:"w"`
		H int `json:"h"`
	} `json:"gridPos"`
	Targets []struct {
		RefID      string  `json:"refId"`
		Expr       string  `json:"expr"`
		Datasource *uidRef `json:"datasource"`
	} `json:"targets"`
	Panels []panel `json:"panels"` // a collapsed row keeps its panels inside it
}

type dashboard struct {
	UID    string  `json:"uid"`
	Title  string  `json:"title"`
	Panels []panel `json:"panels"`
}

// loadDashboards reads every dashboard file, failing the test on any that won't parse.
func loadDashboards(t *testing.T) map[string]dashboard {
	t.Helper()
	dir := os.DirFS(dashboardsDir)
	files, err := fs.Glob(dir, "*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no dashboards in %s (%v)", dashboardsDir, err)
	}
	out := map[string]dashboard{}
	for _, f := range files {
		raw, err := fs.ReadFile(dir, f)
		if err != nil {
			t.Fatal(err)
		}
		var d dashboard
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[f] = d
	}
	return out
}

// flatten lists a dashboard's panels, including those inside collapsed rows.
func flatten(panels []panel) []panel {
	var out []panel
	for _, p := range panels {
		out = append(out, p)
		out = append(out, flatten(p.Panels)...)
	}
	return out
}

func TestDashboardsAreWellFormed(t *testing.T) {
	raw, err := os.ReadFile(datasourceFile)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s+uid:\s*(\S+)`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s: no data source uid", datasourceFile)
	}
	dsUID := string(m[1])

	uids := map[string]string{}
	for file, d := range loadDashboards(t) {
		if d.UID == "" || d.Title == "" {
			t.Errorf("%s: needs a uid and a title", file)
		}
		if other, dup := uids[d.UID]; dup {
			t.Errorf("%s and %s share the uid %q", file, other, d.UID)
		}
		uids[d.UID] = file

		panels := flatten(d.Panels)
		ids := map[int]bool{}
		for i, p := range panels {
			name := file + ": panel " + p.Title
			if ids[p.ID] {
				t.Errorf("%s: id %d is used twice", name, p.ID)
			}
			ids[p.ID] = true
			g := p.GridPos
			if g.X < 0 || g.Y < 0 || g.W <= 0 || g.H <= 0 || g.X+g.W > 24 {
				t.Errorf("%s: off the 24-column grid: %+v", name, g)
			}
			for _, q := range panels[:i] {
				h := q.GridPos
				if g.X < h.X+h.W && h.X < g.X+g.W && g.Y < h.Y+h.H && h.Y < g.Y+g.H {
					t.Errorf("%s overlaps %q", name, q.Title)
				}
			}
			if p.Type == "row" {
				continue
			}
			if p.Datasource == nil || p.Datasource.UID != dsUID {
				t.Errorf("%s: data source must be uid %q", name, dsUID)
			}
			if len(p.Targets) == 0 {
				t.Errorf("%s: no queries", name)
			}
			refs := map[string]bool{}
			for _, q := range p.Targets {
				if strings.TrimSpace(q.Expr) == "" || q.RefID == "" || refs[q.RefID] {
					t.Errorf("%s: each query needs an expression and its own refId", name)
				}
				refs[q.RefID] = true
				if q.Datasource != nil && q.Datasource.UID != dsUID {
					t.Errorf("%s: query %s: data source must be uid %q", name, q.RefID, dsUID)
				}
			}
		}
	}
}

func TestDashboardsUseRealMetrics(t *testing.T) {
	// Every metric jobtrack has, each given a sample so it shows up in a scrape.
	m := metrics.New("abc1234", fakeJobs{rows: []store.CountJobsRow{
		{Kind: "watch.fetch", State: "pending", Jobs: 1},
	}}, quietLogger)
	m.ObserveRequest("GET /healthz", 200, time.Millisecond)
	m.JobFinished("watch.fetch", "done", time.Second)
	m.LeaseExpired("watch.fetch")
	m.FetchFinished("lever", "ok", time.Second)
	m.PostingsAdded("lever", 1)
	have := map[string]bool{}
	for line := range strings.Lines(scrape(t, m)) {
		if !strings.HasPrefix(line, "#") {
			have[line[:strings.IndexAny(line, "{ ")]] = true
		}
	}

	name := regexp.MustCompile(`\bjobtrack_[a-z0-9_]+`)
	used := 0
	for file, d := range loadDashboards(t) {
		for _, p := range flatten(d.Panels) {
			for _, q := range p.Targets {
				for _, metric := range name.FindAllString(q.Expr, -1) {
					used++
					if !have[metric] {
						t.Errorf("%s: panel %q uses %s, which jobtrack doesn't export", file, p.Title, metric)
					}
				}
			}
		}
	}
	if used == 0 {
		t.Error("no dashboard uses a jobtrack metric; is the pattern wrong?")
	}
}
