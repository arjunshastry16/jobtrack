# jobtrack

[![CI](https://github.com/arjunshastry16/jobtrack/actions/workflows/ci.yml/badge.svg)](https://github.com/arjunshastry16/jobtrack/actions/workflows/ci.yml)

A self-hosted job-search tracker, written in Go and run like a production service on a Raspberry Pi 5.

> 🚧 Work in progress. It is being built in small, reviewable steps.

## What it does (planned)

- Track companies, applications and every stage change (applied → screen → onsite → offer)
- Follow-up reminders pushed to your phone
- Watch company job boards (Greenhouse, Lever, Ashby, SmartRecruiters, Workday) and collect new matching postings
- Funnel stats: response rate, interview rate, time in each stage

## Job boards

Postings come from the public JSON APIs that companies' applicant tracking systems publish:
Greenhouse, Lever, Ashby and SmartRecruiters. A watch names the company's id on the board,
e.g. `stripe`.

**Workday is unofficial.** It has no public jobs API, so jobtrack reads the JSON that a
company's public careers page loads in the browser. A Workday watch is set up by pasting the
careers-page URL, filtered first (e.g. to one country) because those filters are kept. The
client is deliberately slow and polite:

- one request at a time, 5 s apart
- it backs off when asked (HTTP 429 / `Retry-After`)
- it runs only in the nightly sync
- it refuses unfiltered sites with more than 800 postings
- nothing tries to get past bot protection

It may stop working whenever Workday changes its site.

Not supported: LinkedIn and Naukri. Their terms forbid scraping, so those applications are
entered by hand.

## How it runs (planned)

- **Backend:** Go (`net/http`, `pgx`, `sqlc`, `goose`), Postgres, and a Postgres-backed job queue (`FOR UPDATE SKIP LOCKED`)
- **Frontend:** React + TypeScript, embedded in the Go binary
- **Delivery:** GitHub Actions → arm64 image on GHCR → pull-based deploy on the Pi with health-gated automatic rollback
- **Operations:** Prometheus, Grafana and Alertmanager; SLOs with burn-rate alerts; nightly backups with automated restore tests

## Production on the Pi

[`deploy/compose.yml`](deploy/compose.yml) runs in `~/stacks/jobtrack` next to a private
`.env` (template: [`deploy/.env.example`](deploy/.env.example)):

| Service | What it does | Cap |
|---|---|---|
| `db` | Postgres 18. Data on the USB HDD; on an internal network with no internet access. | 256 MB, 1 CPU |
| `migrate` | `jobtrack migrate up`, once the database is healthy | 64 MB |
| `api` | Starts only if migrate succeeded. Published on `127.0.0.1:8090` only. | 128 MB, 1 CPU |

All containers have read-only filesystems, drop all Linux capabilities, and keep at most
30 MB of logs. `tailscale serve --https=9443` puts the app on the tailnet and adds the
caller's login. The image is pinned by digest in `.env`.

### Deploys

The Pi pulls; nothing pushes to it, so CI never holds credentials for it.
[`deploy/deploy.sh`](deploy/deploy.sh) runs from cron every 2 minutes and asks GHCR
which digest `:main` points at. If that differs from `.env`, it:

1. pulls the image and pins it in `.env`;
2. runs `docker compose up -d --wait`, so migrations must succeed and the API must turn
   healthy;
3. checks from the host, on the published port, that `/readyz` answers 200 from the new
   image.

If any of that fails, it puts the previous image back, brings it up, and checks it the
same way. Every deploy and rollback goes to ntfy and `deploys.log`.

A failed digest isn't retried every 2 minutes: the next push is tried as normal, and
`deploy.sh --force` retries the failed one. A lock stops runs from overlapping, and only
the current and previous images are kept.

[`scripts/test-deploy.sh`](scripts/test-deploy.sh) (`make test-deploy`) runs `deploy.sh`
against a throwaway stack and a local registry. It checks:

- a deploy, and a no-op when nothing changed;
- a rollback from a build whose migrations fail;
- a rollback from a build that is healthy inside its container but unreachable on the
  published port;
- that a failed build is skipped, `--force`, the lock, and a revert.

Uptime Kuma watches the app through [`deploy/heartbeat.sh`](deploy/heartbeat.sh), which
pushes the result of `/readyz` every minute. The runbook, covering the three gates a
commit passes, day-to-day commands, rolling back by hand, migration rules and the
bad-build drill, is [`docs/deploy.md`](docs/deploy.md).

## API

REST + JSON under `/api/v1`, described in [`docs/openapi.yaml`](docs/openapi.yaml) (also served at
`/api/v1/openapi.yaml`). Tests keep the spec honest: it must be valid OpenAPI, list exactly the
routes the server registers, and every response produced by the integration tests is validated
against it.

| Resource | Endpoints |
|---|---|
| Companies | `GET/POST /companies`, `GET/PUT/DELETE /companies/{id}` |
| Applications | `GET/POST /applications` (filters, cursor pagination), `GET/PUT/DELETE /applications/{id}` |
| Stage changes | `POST /applications/{id}/transitions`, `GET /applications/{id}/timeline` |
| Contacts | `GET/POST /contacts`, `GET/PUT/DELETE /contacts/{id}`, `PUT/DELETE /applications/{id}/contacts/{contact_id}` |
| Notes | `GET/POST /applications/{id}/notes`, `PUT/DELETE /notes/{id}` |
| Job-board watches | `GET/POST /watches`, `GET/PUT/DELETE /watches/{id}` |
| Postings inbox | `GET /postings` (by watch or company, cursor pagination; matches from the nightly board sync), `POST /postings/{id}/application` (save to the wishlist; idempotent) |
| Stats | `GET /stats` (counts by stage, funnel, response rate, median/p90 days in each stage, where applications end; all SQL over the stage history) |
| Health | `GET /healthz` (liveness), `GET /readyz` (database reachable) |

### Access

The app is only reachable over my tailnet. It listens on `127.0.0.1` behind
`tailscale serve`, which passes each caller's Tailscale login in a header. A
middleware lets in only logins on an allowlist (`JOBTRACK_ALLOWED_LOGINS`):

- **No login:** 401. The request didn't come through serve, or came from a tagged
  device.
- **Any other login:** 403.
- **Health probes:** stay open.

It fails closed: the server won't start without an allowlist. Why this rather than
passwords or OIDC, and what it trusts, is in
[ADR 001](docs/adr/001-tailscale-identity-auth.md).

## Metrics

With `JOBTRACK_METRICS_ADDR` set, the service serves Prometheus metrics at `GET /metrics`
on that address. It's a separate listener: tailscale serve never exposes it, and it needs
no login. Defined in [`internal/metrics`](internal/metrics/metrics.go):

| Metric | What it measures |
|---|---|
| `jobtrack_http_requests_total{route,code}` | Requests answered, by route pattern (e.g. `GET /api/v1/companies/{id}`) and status |
| `jobtrack_http_request_duration_seconds{route}` | Response time; has a bucket edge at the 300 ms latency target |
| `jobtrack_jobs{kind,state}` | The job queue's size, read from Postgres at each scrape |
| `jobtrack_jobs_oldest_due_seconds{kind}` | How long the longest-waiting due job has waited |
| `jobtrack_jobs_processed_total{kind,outcome}`, `jobtrack_job_duration_seconds{kind}` | Background job runs: done, retry, dead, lease lost |
| `jobtrack_job_leases_expired_total{kind}` | Jobs recovered from a worker that died mid-job |
| `jobtrack_board_fetches_total{board,outcome}`, `jobtrack_board_fetch_duration_seconds{board}` | Job board fetches: ok, not found, error |
| `jobtrack_postings_new_total{board}` | Postings stored for the first time |
| `jobtrack_build_info{version,goversion}` | Which build is running |

Plus the Go runtime's and the process's standard metrics (`go_*`, `process_*`). Labels
are kept low-cardinality: routes are patterns, never raw paths, and no label holds a
company or anything personal.

### Collecting them

On the Pi, [`deploy/observability`](deploy/observability) runs in `~/stacks/observability`,
with no internet access:

| Service | What it does | Cap |
|---|---|---|
| `prometheus` | Prometheus 3.13 LTS. Scrapes everything every 15 s; keeps 15 days or 2 GB, on the USB HDD. | 300 MB, 1 CPU |
| `node-exporter` | The Pi: CPU, memory, disks, temperature. Reads `/proc`, `/sys` and `/` read-only. | 64 MB |
| `postgres-exporter` | The database, through `jobtrack_monitor`: a login with `pg_monitor` only, so it reads statistics but no data. | 64 MB |
| `grafana` | Grafana 13, with the data source and dashboards provisioned from git. On the tailnet at `:7443`. | 384 MB, 1 CPU |

Prometheus and the exporters sit on jobtrack's internal network, with no published ports;
the api's metrics listener is on `api:9091` there, never on the host. Grafana is the one
that takes outside input, so it can reach Prometheus and nothing else. It signs people in
the same way as the app: it trusts the Tailscale login from `tailscale serve`, and only
the allowlisted login has an account. [`setup.sh`](deploy/observability/setup.sh) does
the first-time setup; [docs/deploy.md](docs/deploy.md#metrics-and-grafana) has the details.

### Dashboards

Three, in [`deploy/observability/grafana/dashboards`](deploy/observability/grafana/dashboards):

- **jobtrack:** request rate, 5xx share and p95 against the 300 ms target, response
  time by route; the job queue, job runs and run time; job board fetches and new
  postings; the process's memory against its limit.
- **Pi (vertex):** CPU, load, memory, temperature, disk space, disk activity.
- **Postgres:** connections, transactions, rows, cache hit ratio, table sizes, dead rows.

They're read-only in Grafana: git is their source. A test
([`dashboards_test.go`](internal/metrics/dashboards_test.go)) checks each one's layout
and data source, and fails if a panel uses a jobtrack metric the code doesn't export.

## Development

Requires Go 1.27+, Docker, [golangci-lint](https://golangci-lint.run/) v2, and Node 22.12+
for the frontend.

```sh
cp .env.example .env
make dev          # start Postgres (Docker), apply migrations, run the API on 127.0.0.1:8091
make test         # unit + integration tests (integration tests use the dev Postgres)
make lint         # run linters
make web-install  # frontend dependencies (exact locked versions, no install scripts)
make web-dev      # frontend on 127.0.0.1:5173, proxying API calls to :8091
make web-test     # frontend unit tests (Vitest)
make build        # bin/jobtrack: one binary serving the API and the web app
make image        # the container image (jobtrack:local)
make lint-ops     # lint the GitHub workflows, shell scripts and production compose file
make test-deploy  # test the deploy script, rollbacks included, on a throwaway stack
make help         # list all tasks
```

The frontend (`web/`, React + TypeScript + Vite) calls the API through a client typed
from `docs/openapi.yaml` (`make web-generate`), so using an endpoint or field the API
doesn't have is a compile error. CI fails if the generated types are out of date.

`make build` embeds the built frontend in the Go binary (`go:embed`, behind the `webui`
build tag), so production is a single file: API calls go to `/api/v1/…`, and every other
path is the app, routed in the browser. Without the tag (plain `go build`/`go test`),
nothing needs Node.

### Container image

The [`Dockerfile`](Dockerfile) builds the same single binary into a `distroless/static`
image: about 26 MB, no shell, running as a non-root user. The build stages run on the
builder's own platform and Go cross-compiles, so an x86_64 CI runner builds the Pi's arm64
image without emulation. Base images are pinned by digest and kept current by Dependabot.

```sh
docker run IMAGE                  # serve on :8090 (JOBTRACK_ALLOWED_LOGINS is required)
docker run IMAGE migrate up       # apply migrations (needs only JOBTRACK_DATABASE_URL)
docker run IMAGE healthcheck      # exit 0 if the server is ready: the image's HEALTHCHECK
```

### Releases

Every commit on main that passes CI is released by
[`release.yml`](.github/workflows/release.yml):

1. **Build:** the arm64 image is built on an x86_64 runner and pushed to GHCR as
   `ghcr.io/arjunshastry16/jobtrack:sha-<commit>`, with build provenance and an SBOM
   attached.
2. **Smoke test:** on an arm64 runner, [`scripts/smoke-image.sh`](scripts/smoke-image.sh)
   starts that exact image (by digest) against a throwaway Postgres. It checks:
   - migrations apply and the server becomes ready;
   - the login check returns 401 and 403;
   - a company can be created and listed;
   - the web app is embedded;
   - SIGTERM shuts it down cleanly.
3. **Promote:** only then does `:main` move to it. The Pi deploys `:main`, so it only ever
   gets an image that passed CI and actually started.

The smoke test runs the same way locally: `make image && scripts/smoke-image.sh jobtrack:local`.

## License

[MIT](LICENSE)
