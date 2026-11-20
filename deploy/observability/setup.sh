#!/usr/bin/env bash
# setup.sh: first-time setup of the observability stack, then start it. Safe to re-run.
#
#   1. .env: settings that are missing are added, the rest kept:
#      - a random password for the database's monitoring login
#      - Grafana's admin login: the first of jobtrack's JOBTRACK_ALLOWED_LOGINS, so
#        the person jobtrack lets in is the one Grafana signs in
#      - a random admin password (unused: there is no password sign-in)
#      - Grafana's address on the tailnet, from `tailscale status`
#   2. secrets/ntfy_url: the ntfy topic Alertmanager sends alerts to, which is
#      jobtrack's JOBTRACK_NTFY_URL (the one deploy notifications use). Rewritten every
#      run; owned by nobody, the user Alertmanager runs as, and readable only by it.
#   3. the monitoring login in jobtrack's database: jobtrack_monitor, a member of
#      pg_monitor only, so it reads statistics but none of the data
#   4. the data directories on the HDD, each owned by the user its service runs as
#   5. docker compose up --wait
#
# Run from ~/stacks/observability, beside compose.yml. The jobtrack stack must be up.
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin

cd "$(dirname "$0")"
jobtrack_dir=${JOBTRACK_STACK_DIR:-$HOME/stacks/jobtrack}

# env_get FILE KEY prints KEY's value in FILE (the last one, as Compose reads it).
env_get() {
	sed -n "s/^$2=//p" "$1" | tail -n 1
}

# env_default KEY VALUE adds KEY=VALUE to .env unless KEY is already set there.
env_default() {
	if [ -z "$(env_get .env "$1")" ]; then
		[ -n "$2" ] || {
			echo "no value for $1; set it in .env" >&2
			exit 1
		}
		printf '%s=%s\n' "$1" "$2" >>.env
		echo "added $1 to .env"
	fi
}

(umask 077 && touch .env)
chmod 600 .env

env_default JOBTRACK_MONITOR_DB_PASSWORD "$(openssl rand -hex 24)"
env_default GRAFANA_ADMIN_PASSWORD "$(openssl rand -hex 24)"
if [ -z "$(env_get .env GRAFANA_ADMIN_LOGIN)" ]; then
	logins=$(env_get "$jobtrack_dir/.env" JOBTRACK_ALLOWED_LOGINS)
	first=${logins%%,*}
	env_default GRAFANA_ADMIN_LOGIN "$(echo "$first" | tr -d '[:space:]')"
fi
if [ -z "$(env_get .env GRAFANA_ROOT_URL)" ]; then
	# The Pi's tailnet name, e.g. "pi.tailnet-name.ts.net." with a trailing dot.
	host=$(tailscale status --json | python3 -c 'import json, sys; print(json.load(sys.stdin)["Self"]["DNSName"].rstrip("."))')
	env_default GRAFANA_ROOT_URL "https://$host:7443/"
fi

ntfy_url=$(env_get "$jobtrack_dir/.env" JOBTRACK_NTFY_URL)
[ -n "$ntfy_url" ] || {
	echo "JOBTRACK_NTFY_URL is empty in $jobtrack_dir/.env; alerts are sent there" >&2
	exit 1
}
[ -z "$(env_get "$jobtrack_dir/.env" JOBTRACK_NTFY_TOKEN)" ] || {
	echo "JOBTRACK_NTFY_TOKEN is set, but alertmanager.yml doesn't send a token yet" >&2
	exit 1
}
# Others may pass through the directory (Alertmanager has to), but not list it.
mkdir -p secrets
chmod 711 secrets
# A previous run left it owned by nobody; the directory is ours, so it can go.
rm -f secrets/ntfy_url
(umask 077 && printf '%s' "$ntfy_url" >secrets/ntfy_url)
docker compose run --rm --no-deps --user 0 --cap-add CHOWN --volume "$PWD/secrets:/secrets" \
	--entrypoint chown alertmanager 65534:65534 /secrets/ntfy_url
echo "secrets/ntfy_url ready"

password=$(env_get .env JOBTRACK_MONITOR_DB_PASSWORD)
# The password goes in on stdin, so it never appears in a process list.
docker compose --file "$jobtrack_dir/compose.yml" exec -T db \
	psql -q -U jobtrack -d jobtrack -v ON_ERROR_STOP=1 <<SQL
\set pw '$password'
SELECT 'CREATE ROLE jobtrack_monitor' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'jobtrack_monitor') \gexec
ALTER ROLE jobtrack_monitor WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE CONNECTION LIMIT 3 PASSWORD :'pw';
GRANT pg_monitor TO jobtrack_monitor;
SQL
echo "monitoring login jobtrack_monitor ready"

# Docker creates a missing bind-mount directory as root; Prometheus and Alertmanager run
# as nobody, Grafana as 472.
docker compose run --rm --no-deps --user 0 --cap-add CHOWN --entrypoint chown prometheus 65534:65534 /prometheus
docker compose run --rm --no-deps --user 0 --cap-add CHOWN --entrypoint chown alertmanager 65534:65534 /alertmanager
docker compose run --rm --no-deps --user 0 --cap-add CHOWN --entrypoint chown grafana 472:0 /var/lib/grafana

docker compose up -d --wait
docker compose ps
