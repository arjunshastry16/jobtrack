# Deploying and operating jobtrack

How a commit reaches the Pi, what stops a bad one, and what to do when something goes
wrong. The README's [Production on the Pi](../README.md#production-on-the-pi) section
has the overview; this is the runbook.

## From push to production

```
git push main
  └─ CI (ci.yml, ~1.5 min)            lint, unit + integration tests, web build, script lint
      └─ Release (release.yml, ~2 min) build arm64 image → smoke test it on arm64 → tag :main
          └─ Pi cron (every 2 min)     deploy.sh: new :main digest? → pull → compose up --wait
                                       → host gate (/readyz on 127.0.0.1:8090) → else roll back
```

A push to main is live 3–6 minutes later with nothing done by hand. Each stage only sees
what the previous one passed, so a bad commit has to get through three gates:

| Gate | Where | Catches | Doesn't catch |
|---|---|---|---|
| **1. CI** | GitHub, every push and PR | Broken code, failing tests, stale generated code, bad workflows | Anything about the built image |
| **2. Smoke test** | GitHub arm64 runner, before `:main` moves | An image that won't migrate, start, serve, log in or shut down cleanly | Differences between the test setup and production |
| **3. Health gate** | The Pi, in `deploy.sh` | Anything that stops *production* from becoming ready: its config, its data, its network | Bugs that leave `/readyz` green |

A build that fails gate 1 or 2 never becomes `:main`, so the Pi never sees it. Gate 3 is
the last line, and it rolls back automatically.

## On the Pi

Everything lives in `~/stacks/jobtrack`:

| File | What it is |
|---|---|
| `compose.yml`, `deploy.sh`, `heartbeat.sh` | Copies of the repo's [`deploy/`](../deploy) files. Re-copy them when they change. |
| `.env` (mode 600) | Image digest, database password, allowed logins, ntfy and Kuma URLs. Never committed. |
| `deploys.log` | One line per deploy or rollback. |
| `.deploy-failed` | The digest of the last build that failed its gate. It isn't retried until `:main` moves or you pass `--force`. |
| `.deploy.lock` | Held while a deploy runs, so cron runs never overlap. |

The user crontab runs:

```
*/2 * * * * /home/admin/stacks/jobtrack/deploy.sh 2>&1 | logger -t jobtrack-deploy
* * * * *   /home/admin/stacks/jobtrack/heartbeat.sh 2>&1 | logger -t jobtrack-heartbeat
```

## Watching it

- **ntfy** (phone): every deploy, every rollback (high priority) and a failed rollback
  (urgent).
- **Uptime Kuma** (`:8443`): the *jobtrack* push monitor. `heartbeat.sh` checks `/readyz`
  from the host every minute and pushes up or down. If pushes stop, Kuma alerts anyway.
  Kuma can't poll jobtrack itself: see the comment at the top of
  [`heartbeat.sh`](../deploy/heartbeat.sh).
- **Homepage** (`:10000`): a jobtrack tile that links to the app.

## Common tasks

What's running, and what happened lately:

```bash
cd ~/stacks/jobtrack
docker compose ps
tail deploys.log
journalctl -t jobtrack-deploy --since today
curl -s 127.0.0.1:8090/healthz      # {"status":"ok","version":"<git sha>"}
```

Why a deploy failed: `deploy.sh` prints the last 20 lines of the migrate and api logs
into the journal before it rolls back.

```bash
journalctl -t jobtrack-deploy -n 80
```

Retry a build that was rolled back (after fixing whatever production was missing, say a
setting in `.env`):

```bash
~/stacks/jobtrack/deploy.sh --force
```

Roll back on purpose. The normal way is `git revert` on main: it goes through all three
gates and deploys like any change. To roll back *now*, pin the previous image and park
`:main` so cron doesn't redeploy it:

```bash
cd ~/stacks/jobtrack
grep -o 'sha256:[0-9a-f]*' .env      # what runs now; the previous is in deploys.log
docker buildx imagetools inspect ghcr.io/arjunshastry16/jobtrack:main --format '{{.Manifest.Digest}}' > .deploy-failed
sed -i 's|^JOBTRACK_IMAGE=.*|JOBTRACK_IMAGE=ghcr.io/arjunshastry16/jobtrack:sha-<previous>@sha256:<digest>|' .env
docker compose up -d --wait
```

The next push to main moves `:main` and deploys as normal.

Pause deploys (say, while debugging on the Pi): comment out the `deploy.sh` line with
`crontab -e`. Put it back when done.

## Migrations and rollback

A rollback swaps the image, not the database: migrations that already ran stay. So every
migration has to work with the previous release too (expand, then contract):

- Add columns and tables freely; give new `NOT NULL` columns a default.
- Drop or rename in a *later* release, once nothing running uses the old shape.
- A migration that fails is rolled back by its transaction, so a release whose migration
  fails leaves the database as it was.

## Testing the deploy machinery

`make test-deploy` runs `deploy.sh` and `heartbeat.sh` against a throwaway stack
(project `jobtrack-deploytest`, port 8092) and a local registry, next to production
without touching it. It covers a deploy, a no-op, two kinds of rollback, the failed-build
skip, `--force`, the lock, a revert, and the Kuma heartbeat.

## Bad-build drill (2026-11-18)

A rehearsal of the failure gate 3 exists for: a build that passes CI and the smoke test
but can't run in production. The first plan was a `/readyz` that always fails, but that
never reaches the Pi: the unit tests and the smoke test both require `/readyz` to answer
200. So the drill was **config drift**, the usual way a "green" build breaks production.
[#3](https://github.com/arjunshastry16/jobtrack/pull/3) made the service refuse to start
without a new setting, `JOBTRACK_DRILL_SETTING`. The tests and the smoke test set it; the
Pi's `.env` didn't.

Timeline (IST), from `journalctl -t jobtrack-deploy` and a probe of `/readyz` every second:

| Time | What happened |
|---|---|
| 23:23:52 | #3 merged; CI had passed on the PR (gate 1 can't see production's config) |
| 23:27:03 | Release: the smoke test passed (gate 2 sets the setting too), `:main` moved |
| 23:28:02 | Pi cron saw the new digest and pulled it; the old build kept serving |
| 23:28:09 | Compose replaced the api container; it exited with `JOBTRACK_DRILL_SETTING is required` |
| 23:28:11 | `compose up --wait` reported it unhealthy: gate 3 failed, `deploy.sh` rolled back |
| 23:28:15 | The previous build answered `/readyz` again |
| 23:28:17 | Rollback verified; `deploys.log` and a high-priority ntfy alert said so |
| 23:28:39 | The revert pushed to main |
| 23:32:13 | The revert deployed through all three gates; the failed marker cleared |

**Impact:** 6 seconds of failed requests. The deploy of the revert, a normal deploy, cost
1 second.

What it showed:

- **The gates work in layers.** Gates 1 and 2 passed the bad build, as designed, and gate
  3 caught it and rolled back with no one touching the Pi. The alert arrived 15 seconds
  after the deploy started.
- **Short outages slip past Uptime Kuma.** Its once-a-minute heartbeat saw nothing (the
  outage fell between 23:28:01 and 23:29:01). The deploy alert covers deploys; request
  metrics and SLOs (Phase 5) will count failed requests whatever their cause.
- **Downtime comes from replacing the container in place.** Compose stops the running
  api before the new one has to prove itself, so even a rollback costs seconds.

Follow-ups:

- Check the new image's config against production's `.env` *before* replacing the running
  container (a `jobtrack` subcommand that loads the config and exits), so config drift
  costs no downtime at all.
- At larger scale: run old and new side by side behind a proxy and switch traffic only
  once the new one is ready (blue-green). Not worth a proxy for one user on one Pi.
