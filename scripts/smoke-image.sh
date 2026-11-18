#!/usr/bin/env bash
# smoke-image.sh IMAGE [VERSION]
#
# Starts a jobtrack image against a throwaway Postgres and checks that it really works:
# migrations apply, the server becomes ready, the login check holds, the API stores and
# returns data, the web app is embedded, and it shuts down cleanly. Release CI runs it
# before an image may become :main; it runs the same on the Pi.
#
# Everything runs on a private Docker network; the app's port is published on
# 127.0.0.1 only, on a random free port. All containers and the network are removed on
# exit, pass or fail.
set -euo pipefail

image=${1:?usage: smoke-image.sh IMAGE [VERSION]}
want_version=${2:-}

# Same Postgres as CI's tests and the dev database.
postgres_image=postgres:18.7-alpine3.24
login=smoke@test
run_id=jobtrack-smoke-$$
net=$run_id
db=$run_id-db
app=$run_id-app
db_url="postgres://jobtrack:smoke@$db:5432/jobtrack?sslmode=disable"

fail() {
	echo "FAIL: $*" >&2
	echo "--- app logs:" >&2
	docker logs "$app" 2>&1 | tail -n 40 >&2 || true
	exit 1
}
cleanup() {
	docker rm -f "$app" "$db" >/dev/null 2>&1 || true
	docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "smoke test: $image"
docker network create "$net" >/dev/null

# Postgres on a RAM disk: nothing is written to the host's disk.
docker run -d --name "$db" --network "$net" --tmpfs /var/lib/postgresql \
	-e POSTGRES_USER=jobtrack -e POSTGRES_PASSWORD=smoke -e POSTGRES_DB=jobtrack \
	"$postgres_image" >/dev/null
# Over TCP: during first-time setup the image runs a temporary server on the Unix socket
# only, then restarts it; only the final server listens on TCP.
pg_ready() { docker exec "$db" pg_isready -q -h 127.0.0.1 -U jobtrack -d jobtrack 2>/dev/null; }
for _ in $(seq 60); do
	pg_ready && break
	sleep 1
done
pg_ready || fail "postgres didn't start"

echo "- migrate up"
docker run --rm --network "$net" -e JOBTRACK_DATABASE_URL="$db_url" "$image" migrate up >/dev/null ||
	fail "migrate up"
docker run --rm --network "$net" -e JOBTRACK_DATABASE_URL="$db_url" "$image" migrate status | grep -q pending &&
	fail "migrations still pending after up"

echo "- start the server"
docker run -d --name "$app" --network "$net" -p 127.0.0.1::8090 \
	--read-only --cap-drop ALL --security-opt no-new-privileges --memory 128m \
	-e JOBTRACK_DATABASE_URL="$db_url" -e JOBTRACK_ALLOWED_LOGINS="$login" -e JOBTRACK_DRILL_SETTING=set "$image" >/dev/null
base="http://$(docker port "$app" 8090/tcp | head -n 1)"

# The image's own health check, run by hand rather than waiting for Docker's interval.
ready=
for _ in $(seq 30); do
	if docker exec "$app" /jobtrack healthcheck 2>/dev/null; then
		ready=1
		break
	fi
	sleep 1
done
[ -n "$ready" ] || fail "server not ready within 30s"

# status METHOD PATH [curl args...]: prints the HTTP status; the body goes to $body.
body=$(mktemp)
trap 'rm -f "$body"; cleanup' EXIT
status() {
	local method=$1 path=$2
	shift 2
	curl -sS -o "$body" -w '%{http_code}' -X "$method" "$@" "$base$path"
}
expect() { # expect WANT GOT WHAT
	[ "$2" = "$1" ] || fail "$3: got $2, want $1; body: $(head -c 300 "$body")"
	echo "- $3: $2"
}
as_user=(-H "Tailscale-User-Login: $login")

expect 200 "$(status GET /healthz)" "healthz without a login"
if [ -n "$want_version" ]; then
	grep -q "\"version\":\"$want_version\"" "$body" || fail "healthz version: $(cat "$body"), want $want_version"
	echo "- version: $want_version"
fi
expect 200 "$(status GET /readyz)" "readyz without a login"
expect 401 "$(status GET /api/v1/companies)" "API without a login"
expect 403 "$(status GET /api/v1/companies -H 'Tailscale-User-Login: stranger@test')" "API with a login not allowed"
expect 201 "$(status POST /api/v1/companies "${as_user[@]}" -H 'Content-Type: application/json' -d '{"name":"Smoke Test Co"}')" \
	"create a company"
expect 200 "$(status GET /api/v1/companies "${as_user[@]}")" "list companies"
grep -q '"Smoke Test Co"' "$body" || fail "the company isn't listed: $(cat "$body")"
expect 200 "$(status GET /stats "${as_user[@]}")" "web app page"
grep -qi '<!doctype html>' "$body" || fail "web app not embedded: $(head -c 200 "$body")"

echo "- stop (SIGTERM)"
docker stop -t 15 "$app" >/dev/null
code=$(docker inspect "$app" --format '{{.State.ExitCode}}')
[ "$code" = 0 ] || fail "exit code $code after SIGTERM, want 0"
docker logs "$app" 2>&1 | grep -q '"msg":"stopped cleanly"' || fail "no clean shutdown in the logs"
if docker logs "$app" 2>&1 | grep -q '"level":"ERROR"'; then
	fail "errors in the logs"
fi

echo "PASS: $image"
