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
| Postings inbox | `GET /postings` (by watch or company, cursor pagination; matches from the nightly board sync) |
| Health | `GET /healthz` (liveness), `GET /readyz` (database reachable) |

## Development

Requires Go 1.27+, Docker, [golangci-lint](https://golangci-lint.run/) v2, and Node 22.12+
for the frontend.

```sh
cp .env.example .env
make dev          # start Postgres (Docker), apply migrations, run the API on 127.0.0.1:8090
make test         # unit + integration tests (integration tests use the dev Postgres)
make lint         # run linters
make web-install  # frontend dependencies (exact locked versions, no install scripts)
make web-dev      # frontend on 127.0.0.1:5173, proxying API calls to :8090
make web-test     # frontend unit tests (Vitest)
make build        # bin/jobtrack: one binary serving the API and the web app
make help         # list all tasks
```

The frontend (`web/`, React + TypeScript + Vite) calls the API through a client typed
from `docs/openapi.yaml` (`make web-generate`), so using an endpoint or field the API
doesn't have is a compile error. CI fails if the generated types are out of date.

`make build` embeds the built frontend in the Go binary (`go:embed`, behind the `webui`
build tag), so production is a single file: API calls go to `/api/v1/…`, and every other
path is the app, routed in the browser. Without the tag (plain `go build`/`go test`),
nothing needs Node.

## License

[MIT](LICENSE)
