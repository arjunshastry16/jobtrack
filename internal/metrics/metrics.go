// Package metrics defines jobtrack's Prometheus metrics and serves them.
//
// The packages that do the work don't import Prometheus. Each declares the small
// observer interface it needs (httpapi.RequestObserver, jobs.Observer, ingest.Observer)
// and *Metrics implements them all, so metric names and labels live in one place and
// those packages stay testable without a registry.
//
// Labels are kept low-cardinality on purpose: routes are ServeMux patterns (never raw
// paths), boards are board types (never a company), and nothing personal is a label.
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/arjunshastry16/jobtrack/internal/store"
)

const namespace = "jobtrack"

// requestBuckets suit an API whose latency target is p95 < 300ms (docs/SLOs.md, step
// 5.4): fine-grained below a second, with an edge exactly at 0.3s, up to the 10s timeout.
var requestBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.3, 0.5, 1, 2.5, 5, 10}

// workBuckets cover background work, from a reminder (milliseconds) to a polite
// Workday fetch (many minutes; its lease is 20).
var workBuckets = []float64{0.01, 0.05, 0.25, 1, 5, 15, 30, 60, 120, 300, 600, 1200}

// UnmatchedRoute is the route label of a request that never matched a route pattern:
// refused by the login check (401, 403), or a method no pattern allows (405). (An unknown
// GET path matches "GET /", the web app, which also answers unknown /api/ paths with 404.)
const UnmatchedRoute = "unmatched"

// JobCounter reports the job queue's size. store.DB provides it.
type JobCounter interface {
	CountJobs(ctx context.Context) ([]store.CountJobsRow, error)
}

// Metrics holds every metric jobtrack exports, on its own registry (not Prometheus's
// global one, so tests can make as many as they like).
type Metrics struct {
	registry *prometheus.Registry

	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec

	jobs          *prometheus.CounterVec
	jobDuration   *prometheus.HistogramVec
	leasesExpired *prometheus.CounterVec

	fetches       *prometheus.CounterVec
	fetchDuration *prometheus.HistogramVec
	newPostings   *prometheus.CounterVec
}

// New registers jobtrack's metrics, plus the Go runtime's and the process's, and the
// build info for version. The queue's size is read from jobs at every scrape.
func New(version string, jobs JobCounter, logger *slog.Logger) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),

		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "http_requests_total",
			Help: "HTTP requests answered, by route pattern and status code.",
		}, []string{"route", "code"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "http_request_duration_seconds",
			Help:    "Time to answer HTTP requests, by route pattern.",
			Buckets: requestBuckets,
		}, []string{"route"}),

		jobs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "jobs_processed_total",
			Help: "Background jobs run, by kind and outcome (done, retry, dead, lease_lost, record_failed).",
		}, []string{"kind", "outcome"}),
		jobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "job_duration_seconds",
			Help:    "Time background jobs took to run, by kind.",
			Buckets: workBuckets,
		}, []string{"kind"}),
		leasesExpired: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "job_leases_expired_total",
			Help: "Jobs whose worker vanished (crash, kill) and were put back on the queue, by kind.",
		}, []string{"kind"}),

		fetches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "board_fetches_total",
			Help: "Job board fetches, by board and outcome (ok, not_found, error).",
		}, []string{"board", "outcome"}),
		fetchDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "board_fetch_duration_seconds",
			Help:    "Time to fetch a company's postings from a job board, by board.",
			Buckets: workBuckets,
		}, []string{"board"}),
		newPostings: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "postings_new_total",
			Help: "Postings stored for the first time, by board.",
		}, []string{"board"}),
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Name: "build_info",
		Help: "Always 1; its labels say which build is running.",
	}, []string{"version", "goversion"})
	buildInfo.WithLabelValues(version, runtime.Version()).Set(1)

	m.registry.MustRegister(
		m.requests, m.requestDuration,
		m.jobs, m.jobDuration, m.leasesExpired,
		m.fetches, m.fetchDuration, m.newPostings,
		buildInfo,
		newQueueCollector(jobs, logger),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler serves the metrics in Prometheus's text format.
func (m *Metrics) Handler() http.Handler {
	// Registry makes the handler count its own failures (promhttp_metric_handler_errors_total).
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// ObserveRequest records one answered HTTP request. pattern is the ServeMux pattern that
// matched (e.g. "GET /api/v1/companies/{id}"), or "" if none did.
func (m *Metrics) ObserveRequest(pattern string, status int, took time.Duration) {
	route := pattern
	if route == "" {
		route = UnmatchedRoute
	}
	m.requests.WithLabelValues(route, strconv.Itoa(status)).Inc()
	m.requestDuration.WithLabelValues(route).Observe(took.Seconds())
}

// JobFinished records one run of a background job.
func (m *Metrics) JobFinished(kind, outcome string, took time.Duration) {
	m.jobs.WithLabelValues(kind, outcome).Inc()
	m.jobDuration.WithLabelValues(kind).Observe(took.Seconds())
}

// LeaseExpired records a job reclaimed after its worker stopped renewing it.
func (m *Metrics) LeaseExpired(kind string) {
	m.leasesExpired.WithLabelValues(kind).Inc()
}

// FetchFinished records one fetch from a job board.
func (m *Metrics) FetchFinished(board, outcome string, took time.Duration) {
	m.fetches.WithLabelValues(board, outcome).Inc()
	m.fetchDuration.WithLabelValues(board).Observe(took.Seconds())
}

// PostingsAdded records postings stored for the first time.
func (m *Metrics) PostingsAdded(board string, n int) {
	m.newPostings.WithLabelValues(board).Add(float64(n))
}

// queueQueryTimeout bounds the query behind each scrape, well inside Prometheus's
// default 10s scrape timeout.
const queueQueryTimeout = 3 * time.Second

// jobStates are the jobs table's states (its CHECK constraint).
var jobStates = []string{"pending", "running", "done", "dead"}

// queueCollector reads the queue's size from the database when Prometheus scrapes, so
// the numbers are always current and nothing has to keep them up to date.
type queueCollector struct {
	jobs      JobCounter
	logger    *slog.Logger
	count     *prometheus.Desc
	oldestDue *prometheus.Desc
}

func newQueueCollector(jobs JobCounter, logger *slog.Logger) *queueCollector {
	return &queueCollector{
		jobs:   jobs,
		logger: logger,
		count: prometheus.NewDesc(namespace+"_jobs", "Jobs in the queue, by kind and state.",
			[]string{"kind", "state"}, nil),
		oldestDue: prometheus.NewDesc(namespace+"_jobs_oldest_due_seconds",
			"How long the longest-waiting due pending job has waited, by kind; 0 if none.",
			[]string{"kind"}, nil),
	}
}

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.count
	ch <- c.oldestDue
}

func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), queueQueryTimeout)
	defer cancel()
	rows, err := c.jobs.CountJobs(ctx)
	if err != nil {
		// The rest of the scrape still works; the queue's series go missing, which an
		// absent() alert can catch.
		c.logger.Warn("metrics: counting jobs failed", "err", err)
		return
	}
	counts := map[string]map[string]int64{}
	oldest := map[string]float64{}
	for _, r := range rows {
		if counts[r.Kind] == nil {
			counts[r.Kind] = map[string]int64{}
		}
		counts[r.Kind][r.State] = r.Jobs
		if r.State == "pending" {
			oldest[r.Kind] = r.OldestDueSeconds
		}
	}
	// Every state of every kind, so a count that drops to zero reads 0 instead of
	// vanishing (an alert on dead > 0 must see it clear).
	for kind, byState := range counts {
		for _, s := range jobStates {
			ch <- prometheus.MustNewConstMetric(c.count, prometheus.GaugeValue, float64(byState[s]), kind, s)
		}
		ch <- prometheus.MustNewConstMetric(c.oldestDue, prometheus.GaugeValue, oldest[kind], kind)
	}
}
