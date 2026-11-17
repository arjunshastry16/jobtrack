# jobtrack

[![CI](https://github.com/arjunshastry16/jobtrack/actions/workflows/ci.yml/badge.svg)](https://github.com/arjunshastry16/jobtrack/actions/workflows/ci.yml)

A self-hosted job-search tracker, written in Go and run like a production service on a Raspberry Pi 5.

> 🚧 Work in progress. It is being built in small, reviewable steps.

## What it does (planned)

- Track companies, applications and every stage change (applied → screen → onsite → offer)
- Follow-up reminders pushed to your phone
- Watch company job boards (Greenhouse, Lever, Ashby) and collect new matching postings
- Funnel stats: response rate, interview rate, time in each stage

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
| Health | `GET /healthz` (liveness), `GET /readyz` (database reachable) |

## Development

Requires Go 1.27+, Docker, and [golangci-lint](https://golangci-lint.run/) v2.

```sh
cp .env.example .env
make dev    # start Postgres (Docker), apply migrations, run the API on 127.0.0.1:8090
make test   # unit + integration tests (integration tests use the dev Postgres)
make lint   # run linters
make help   # list all tasks
```

## License

[MIT](LICENSE)
