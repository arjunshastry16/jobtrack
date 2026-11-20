# Service level objectives and alerts

jobtrack has one user, so these targets aren't promises to anyone. They're here for the
reason any team writes them down: so "is it working?" has a measured answer, and so
alerts fire when users would notice, rather than whenever a number twitches.

The rules are in [`deploy/observability/rules/`](../deploy/observability/rules), with unit
tests beside them; Alertmanager sends what fires to ntfy
([`alertmanager.yml`](../deploy/observability/alertmanager.yml)).

## The objectives

| SLO | Target | Measured by | Budget |
|---|---|---|---|
| **Availability** | 99.5% of requests don't fail with a 5xx, over 30 days | `jobtrack_http_requests_total` by `code` | 0.5%: about 3.6 hours of total outage a month |
| **Latency** | 95% of requests are answered within 300 ms, over 30 days | `jobtrack_http_request_duration_seconds`, which has a bucket edge at 0.3 s | 5% of requests may be slower |
| **Reminders** | A follow-up reminder starts within 10 minutes of being due | `jobtrack_jobs_oldest_due_seconds{kind="reminder.follow_up"}` | none: every late reminder alerts |

**What counts as a request.** Everything the api answers, including the health probes.
With one user, the probes are most of the traffic: the heartbeat's `/readyz` every minute
and Docker's healthcheck every 30 seconds, about 180 requests an hour. That's deliberate.
`/readyz` checks the database, so the probes are a steady test of the path a real
request takes, day and night. Requests turned away by the login check (401, 403) aren't
failures; they're the check working.

**What it can't see.** When the api is down, it answers nothing, so there are no
requests to count. `JobtrackDown` covers that case.

Prometheus keeps 31 days, so a whole 30-day window is always there to look at.

## How the SLO alerts work

Alerting on "error rate > X%" either pages for every blip or misses a slow leak. These
alerts instead watch how fast the **error budget** is being spent: the *burn rate*. A
burn rate of 1 uses exactly the month's budget in 30 days; 14.4 uses 2% of it in an
hour. This is the multiwindow, multi-burn-rate method from
[Google's SRE workbook](https://sre.google/workbook/alerting-on-slos/):

| Alert | Fires when the burn rate is over… | …in both the long and short window | Budget spent by then | Severity |
|---|---|---|---|---|
| `ErrorBudgetBurningFast`, `LatencyBudgetBurningFast` | 14.4 | 1 hour and 5 minutes | 2% | critical |
| | 6 | 6 hours and 30 minutes | 5% | critical |
| `ErrorBudgetBurningSlowly`, `LatencyBudgetBurningSlowly` | 3 | 1 day and 2 hours | 10% | warning |
| | 1 | 3 days and 6 hours | 10% | warning |

The long window makes sure the budget really is going, not just a few unlucky requests;
the short window makes the alert clear soon after the problem does. Some examples, with
the probes' 3 requests a minute (all checked by the unit tests):

- **The database goes away.** `ErrorBudgetBurningFast` fires within about 8 minutes. Ten
  minutes after the fix it's still firing: half an hour of failures used 14% of the
  month's budget, which is worth knowing. It clears when the failures leave the 30-minute
  window.
- **Everything is slow.** `LatencyBudgetBurningFast` takes about 45 minutes, since 5% of
  requests are allowed to be slow and 72% of an hour's must be before it fires.
- **1% of requests fail, steadily.** Only the 3-day window catches it, as
  `ErrorBudgetBurningSlowly`.

The ratios are recorded once (`jobtrack:http_errors:ratio_rate5m` and so on) in the same
rule group as the alerts, so the alerts always read the current evaluation's numbers.

## Where alerts go

Alertmanager sends them to the same private ntfy topic as deploy notifications. ntfy
formats them with its built-in Alertmanager template.

- **critical:** high priority, repeated every 2 hours while firing.
- **warning:** default priority, repeated every 12 hours.
- Each sends a second notification when it resolves.
- Alerts with the same name are grouped into one notification.
- When a fast-burn alert fires, the slow one for the same SLO is held back. While
  `JobtrackDown` fires, the SLO and job queue alerts are held back too: with the api
  gone, they can't be measured.

**Silencing.** For planned work, or an alert already being dealt with:

```bash
cd ~/stacks/observability
docker compose exec alertmanager amtool --alertmanager.url=http://localhost:9093 \
    silence add alertname=DeadJobs --duration=24h --comment="looking at it"
docker compose exec alertmanager amtool --alertmanager.url=http://localhost:9093 alert query
```

## What to do when an alert fires

### ErrorBudgetBurningFast, ErrorBudgetBurningSlowly

Requests are failing with 5xx. The **jobtrack** dashboard's *Requests by status* shows
when it started. Check, in order:

1. Is the database up? (`PostgresDown` usually fires too.) `docker compose ps` in
   `~/stacks/jobtrack`.
2. Did a deploy just happen? `tail ~/stacks/jobtrack/deploys.log`. The deploy gate
   only checks `/readyz` for a minute; to roll back, see
   [deploy.md](deploy.md#migrations-and-rollback).
3. The api's logs: `docker compose logs --since 1h api | grep '"level":"ERROR"'`.

### LatencyBudgetBurningFast, LatencyBudgetBurningSlowly

Requests are slow. The **jobtrack** dashboard's *p95 response time by route* says
which. Then the **Pi** dashboard: is the CPU pegged, the Pi hot (throttling), or the
SD card busy? The **Postgres** dashboard: long transactions, or many connections?

### JobtrackDown

Prometheus can't reach the api. Uptime Kuma's heartbeat alerts for the same thing.
`docker compose ps` and `docker compose logs --tail 50 api` in `~/stacks/jobtrack`. If
a deploy was rolling back, it ends within a minute or two.

### ReminderLate

A follow-up reminder is more than 10 minutes late, so the workers aren't keeping up or
aren't running. The **jobtrack** dashboard's *Queue* and *Longest wait* show what's
waiting. Are jobs failing and retrying (*Runs per minute*)? Is the nightly board sync
hogging the workers?

### JobsStuck

Board-fetch (or other) jobs have waited over an hour. The nightly sync is paced on
purpose, but even all of Workday takes minutes, not an hour. Check the api's logs for
the worker.

### DeadJobs

Jobs failed every attempt and were given up on. They stay in the table, so this keeps
firing (every 12 hours) until they're dealt with:

```bash
cd ~/stacks/jobtrack
docker compose exec db psql -U jobtrack -d jobtrack -c \
    "SELECT id, kind, attempts, finished_at, last_error FROM jobs WHERE state = 'dead'"
# Fixed the cause? Run it again:
docker compose exec db psql -U jobtrack -d jobtrack -c \
    "UPDATE jobs SET state = 'pending', attempts = 0, run_at = now(), last_error = NULL WHERE id = <id>"
# Not worth running again (e.g. a watch for a board that's gone):
docker compose exec db psql -U jobtrack -d jobtrack -c "DELETE FROM jobs WHERE id = <id>"
```

### PostgresDown

postgres_exporter can't query the database. `docker compose ps db` and
`docker compose logs --tail 50 db` in `~/stacks/jobtrack`. Is `/mnt/cnimbus` mounted
(the data lives there)?

### DiskAlmostFull

`/` (the SD card) or `/mnt/cnimbus` (the USB disk, all of jobtrack's data) is over 85%.
`sudo du -xh --max-depth=2 <mountpoint> | sort -h | tail` finds what's big. Docker's
images and logs live on the SD card: `docker system df`.

### PiHot

Over 75 °C for 5 minutes; the Pi throttles at 80 °C. Is something using all the CPU
(**Pi** dashboard)? Is the fan running, and the case's airflow clear?

### MemoryLow

Under 10% of memory available for 10 minutes. `docker stats --no-stream` shows which
container. Every jobtrack container has a memory cap, so it's more likely Nextcloud or
something outside Docker.

### TargetDown

Prometheus can't scrape node_exporter, postgres_exporter or Alertmanager, so the alerts
that depend on it can't fire. `docker compose ps` in `~/stacks/observability`.

### RulesFailing

Prometheus couldn't evaluate a rule group. `docker compose logs prometheus`. Usually a
rule edited on the Pi without `make lint-ops`.

### AlertsNotDelivered

Alertmanager couldn't send to ntfy. This one may not arrive either, but it's there to
see on the dashboards. `docker compose logs alertmanager`: is ntfy.sh reachable, and
is `secrets/ntfy_url` right? Re-running `./setup.sh` rewrites it.

## What isn't covered

- **Prometheus or Alertmanager themselves down:** nothing alerts. Uptime Kuma's
  heartbeat still catches jobtrack itself being down; a heartbeat for the monitoring
  stack would close the gap.
- **ntfy.sh down:** alerts are lost (Alertmanager retries for a while).
- **Low traffic:** with about 180 requests an hour, one failed request is 0.6% of an
  hour. The long windows and `for` durations keep single failures from paging.

## Alert drill (2026-11-20)

To check the whole path, from a rule firing to the phone, rather than just the rules:

1. **A test alert** sent straight to Alertmanager with `amtool alert add` (the command is
   in [deploy.md](deploy.md#metrics-and-grafana)): "🚨 Alert: TestAlert" arrived on ntfy
   within 30 seconds, and "✅ Resolved" five minutes later, when it expired.
2. **A real rule firing:** postgres_exporter stopped at 23:06:20. `TargetDown` fired
   after its 5 minutes; "Alert: TargetDown" arrived at 23:11:59, 5 m 39 s after the
   stop (5 minutes of `for`, the next scrape, and the 30-second `group_wait`). The
   exporter was started again once it had arrived; "Resolved: TargetDown" came at
   23:16:59, on Alertmanager's next 5-minute `group_interval`. jobtrack itself was
   never affected.

What the first attempt taught: it restarted the exporter as soon as Prometheus showed
the alert firing, which was inside the 30-second `group_wait`. The alert resolved
before Alertmanager's first send, so it never sent anything, which is correct (nothing
to tell anyone any more) but means a drill has to wait for the notification itself, not
just for Prometheus.
