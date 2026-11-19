#!/usr/bin/env bash
# setup.sh: first-time setup of the observability stack, then start it. Safe to re-run.
#
#   1. .env with a random password for the database's monitoring login (kept if present)
#   2. the monitoring login in jobtrack's database: jobtrack_monitor, a member of
#      pg_monitor only, so it reads statistics but none of the data
#   3. Prometheus's data directory on the HDD, owned by the user Prometheus runs as
#   4. docker compose up --wait
#
# Run from ~/stacks/observability, beside compose.yml. The jobtrack stack must be up.
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin

cd "$(dirname "$0")"
jobtrack_dir=${JOBTRACK_STACK_DIR:-$HOME/stacks/jobtrack}

if [ ! -f .env ]; then
	(umask 077 && printf 'JOBTRACK_MONITOR_DB_PASSWORD=%s\n' "$(openssl rand -hex 24)" >.env)
	echo "created .env"
fi
chmod 600 .env
password=$(sed -n 's/^JOBTRACK_MONITOR_DB_PASSWORD=//p' .env | tail -n 1)
[ -n "$password" ] || {
	echo "JOBTRACK_MONITOR_DB_PASSWORD is empty in .env" >&2
	exit 1
}

# The password goes in on stdin, so it never appears in a process list.
docker compose --file "$jobtrack_dir/compose.yml" exec -T db \
	psql -q -U jobtrack -d jobtrack -v ON_ERROR_STOP=1 <<SQL
\set pw '$password'
SELECT 'CREATE ROLE jobtrack_monitor' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'jobtrack_monitor') \gexec
ALTER ROLE jobtrack_monitor WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE CONNECTION LIMIT 3 PASSWORD :'pw';
GRANT pg_monitor TO jobtrack_monitor;
SQL
echo "monitoring login jobtrack_monitor ready"

# Docker creates a missing bind-mount directory as root; Prometheus runs as nobody.
docker compose run --rm --no-deps --user 0 --cap-add CHOWN --entrypoint chown prometheus 65534:65534 /prometheus

docker compose up -d --wait
docker compose ps
