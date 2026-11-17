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

## Development

Requires Go 1.27+ and [golangci-lint](https://golangci-lint.run/) v2.

```sh
make help   # list tasks
make test   # run tests
make lint   # run linters
```

## License

[MIT](LICENSE)
