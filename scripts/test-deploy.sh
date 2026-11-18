#!/usr/bin/env bash
# test-deploy.sh [SOURCE_IMAGE]
#
# Tests deploy/deploy.sh end to end against a throwaway stack (project jobtrack-deploytest
# on 127.0.0.1:8092) and a throwaway registry (127.0.0.1:5555), beside production and
# without touching it. From SOURCE_IMAGE (default: ghcr.io/arjunshastry16/jobtrack:main)
# it makes four releases, then moves :main between them and checks what deploy.sh does
# (and last, what heartbeat.sh reports to a stand-in for Uptime Kuma):
#
#   v1, v2       good builds (v2 differs only by a label, so it's a new digest)
#   bad-migrate  can't even run its migrations
#   bad-port     migrates and passes its own container health check, but doesn't answer
#                on the published port: only the host-side gate can catch it
#
# Needs Docker, curl, flock and python3. Everything it creates is removed on exit.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
src=${1:-ghcr.io/arjunshastry16/jobtrack:main}
project=jobtrack-deploytest
reg_port=5555
app_port=8092
kuma_port=5556
registry=$project-registry
repo=127.0.0.1:$reg_port/jobtrack
registry_image=registry:3.0.0@sha256:6c5666b861f3505b116bb9aa9b25175e71210414bd010d92035ff64018f9457e
postgres_image=postgres:18.7-alpine3.24
work=$(mktemp -d)
stack=$work/stack
data=$work/data
mkdir -p "$stack" "$data"
kuma_pid=

cleanup() {
	[ -z "$kuma_pid" ] || kill "$kuma_pid" 2>/dev/null || true
	(cd "$stack" && docker compose down --volumes --remove-orphans >/dev/null 2>&1) || true
	docker rm -f "$registry" >/dev/null 2>&1 || true
	# Postgres made its files as its own user; remove them as root, inside a container.
	docker run --rm -v "$data:/data" "$postgres_image" rm -rf /data/postgres >/dev/null 2>&1 || true
	docker images --no-trunc --quiet "$repo" | sort -u | xargs -r docker image rm -f >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

failures=0
pass() { echo "  ok: $*"; }
fail() {
	echo "  FAIL: $*"
	failures=$((failures + 1))
}
check() { # check DESCRIPTION COMMAND...
	local what=$1
	shift
	if "$@"; then pass "$what"; else fail "$what"; fi
}

accept='application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'
digest_of() { # the registry's digest for a tag
	curl -sSI -H "Accept: $accept" "http://127.0.0.1:$reg_port/v2/jobtrack/manifests/$1" |
		grep -i '^docker-content-digest:' | awk '{print $2}' | tr -d '\r'
}
release() { # release TAG: point :main at it (deploy.sh prunes local copies, so pull first)
	docker pull -q "$repo:$1" >/dev/null
	docker tag "$repo:$1" "$repo:main"
	docker push -q "$repo:main" >/dev/null
}
deploy() { # runs deploy.sh; sets $out and $rc
	set +e
	out=$("$stack/deploy.sh" "$@" 2>&1)
	rc=$?
	set -e
}
running_tag() { # which of our releases the api container runs, by registry digest
	local id digests t
	id=$(cd "$stack" && docker compose ps -q api)
	digests=$(docker image inspect --format '{{join .RepoDigests " "}}' "$(docker inspect --format '{{.Image}}' "$id")")
	for t in v1 v2 bad-migrate bad-port; do
		[[ " $digests " == *"@$(digest_of "$t") "* ]] && echo "$t" && return
	done
	echo unknown
}
image_in_env() { grep -q "^JOBTRACK_IMAGE=.*@$(digest_of "$1")\$" "$stack/.env"; }
serving() { curl -fsS -o /dev/null --max-time 3 "http://127.0.0.1:$app_port/readyz"; }
runs() { [ "$(running_tag)" = "$1" ]; }
said() { grep -q -- "$1" <<<"$out"; }
logged() { tail -n 1 "$stack/deploys.log" 2>/dev/null | grep -q -- "$1"; }

echo "setup: registry, releases and a test stack"
docker run -d --name "$registry" -p "127.0.0.1:$reg_port:5000" "$registry_image" >/dev/null
for _ in $(seq 30); do
	curl -fsS -o /dev/null "http://127.0.0.1:$reg_port/v2/" 2>/dev/null && break
	sleep 1
done
docker pull -q "$src" >/dev/null
build() { # build TAG DOCKERFILE-LINES...
	local tag=$1
	shift
	printf '%s\n' "FROM $src" "$@" | docker build -q -t "$repo:$tag" - >/dev/null
	docker push -q "$repo:$tag" >/dev/null
}
build v1 'LABEL org.opencontainers.image.version=1111111'
build v2 'LABEL org.opencontainers.image.version=2222222'
build bad-migrate 'LABEL org.opencontainers.image.version=badbad1' 'ENTRYPOINT ["/jobtrack", "no-such-command"]'
build bad-port 'LABEL org.opencontainers.image.version=badbad2' 'ENV JOBTRACK_ADDR=0.0.0.0:9999'

cp "$repo_root/deploy/compose.yml" "$repo_root/deploy/deploy.sh" "$repo_root/deploy/heartbeat.sh" "$stack/"
(
	umask 077
	cat >"$stack/.env" <<EOF
JOBTRACK_IMAGE=$repo:sha-1111111@$(digest_of v1)
JOBTRACK_PROJECT=$project
JOBTRACK_PORT=$app_port
JOBTRACK_DATA_DIR=$data
JOBTRACK_DB_PASSWORD=test
JOBTRACK_ALLOWED_LOGINS=test@test
JOBTRACK_NTFY_URL=
EOF
)
release v1
(cd "$stack" && docker compose up -d --wait --wait-timeout 120 >/dev/null 2>&1)
check "test stack serves v1" runs v1

echo "1. :main unchanged: nothing to do"
deploy
check "exit 0" [ "$rc" = 0 ]
check "silent" [ -z "$out" ]
check "nothing recorded" [ ! -e "$stack/deploys.log" ]

echo "2. :main moves to v2: deployed"
release v2
deploy
check "exit 0" [ "$rc" = 0 ]
check ".env names v2" image_in_env v2
check "api runs v2" runs v2
check "serving" serving
check "recorded as deployed" logged "deployed sha-2222222 (was sha-1111111)"
check "notified" said "notify: jobtrack deployed sha-2222222"

echo "3. :main moves to a build whose migrations fail: rolled back"
release bad-migrate
deploy
check "exit 1" [ "$rc" = 1 ]
check ".env back on v2" image_in_env v2
check "api runs v2" runs v2
check "serving" serving
check "recorded as failed" logged "FAILED sha-badbad1, rolled back to sha-2222222"
check "notified" said "notify: jobtrack deploy failed"
check "failed digest remembered" [ "$(cat "$stack/.deploy-failed")" = "$(digest_of bad-migrate)" ]

echo "4. the same failed build again: skipped"
deploy
check "exit 0" [ "$rc" = 0 ]
check "silent" [ -z "$out" ]
check "api still runs v2" runs v2

echo "5. a build that's healthy inside but unreachable on the port: rolled back"
release bad-port
deploy
check "exit 1" [ "$rc" = 1 ]
check "api runs v2" runs v2
check "serving" serving
check "recorded as failed" logged "FAILED sha-badbad2, rolled back to sha-2222222"

echo "6. --force retries a failed build"
deploy --force
check "exit 1 (it still fails)" [ "$rc" = 1 ]
check "it was tried" said "deploying"
check "api runs v2" runs v2

echo "7. a deploy already running: skipped"
flock "$stack/.deploy.lock" sleep 5 &
sleep 1
deploy
wait
check "exit 0" [ "$rc" = 0 ]
check "says why" said "another deploy is still running"

echo "8. :main moves back to v1 (a revert): deployed, failed marker cleared"
release v1
deploy
check "exit 0" [ "$rc" = 0 ]
check "api runs v1" runs v1
check "serving" serving
check "failed marker cleared" [ ! -e "$stack/.deploy-failed" ]
check ".env still private" [ "$(stat -c %a "$stack/.env")" = 600 ]

echo "9. bad arguments"
deploy --sideways
check "exit 64" [ "$rc" = 64 ]

echo "10. heartbeat.sh reports to Kuma"
# A stand-in for Kuma's push endpoint: answers 200 and writes down each request's path.
kuma_log=$work/kuma.log
python3 - "$kuma_port" "$kuma_log" <<'PY' &
import http.server, sys
port, log = int(sys.argv[1]), sys.argv[2]
class Push(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        with open(log, "a") as f:
            f.write(self.path + "\n")
        self.send_response(200)
        self.end_headers()
    def log_message(self, *args):
        pass
http.server.HTTPServer(("127.0.0.1", port), Push).serve_forever()
PY
kuma_pid=$!
for _ in $(seq 30); do
	curl -fsS -o /dev/null "http://127.0.0.1:$kuma_port/ping" 2>/dev/null && break
	sleep 0.2
done
: >"$kuma_log"
heartbeat() {
	set +e
	out=$("$stack/heartbeat.sh" 2>&1)
	rc=$?
	set -e
}
pushed() { tail -n 1 "$kuma_log" | grep -q -- "$1"; }
heartbeat
check "no push URL: exit 0" [ "$rc" = 0 ]
check "no push URL: nothing sent" [ ! -s "$kuma_log" ]
# As Kuma shows it, with its example query; heartbeat.sh drops that and adds its own.
echo "JOBTRACK_KUMA_PUSH_URL=http://127.0.0.1:$kuma_port/api/push/t0ken?status=up&msg=OK&ping=" >>"$stack/.env"
heartbeat
check "up: exit 0" [ "$rc" = 0 ]
check "up: pushed status=up" pushed '^/api/push/t0ken?status=up&msg=ready&ping=[0-9]*$'
(cd "$stack" && docker compose stop api >/dev/null 2>&1)
heartbeat
check "down: exit 0" [ "$rc" = 0 ]
check "down: pushed status=down" pushed "^/api/push/t0ken?status=down&msg=no+answer+on+127.0.0.1%3a$app_port&ping=[0-9]*\$"
kill "$kuma_pid"
wait "$kuma_pid" 2>/dev/null || true
kuma_pid=
heartbeat
check "Kuma unreachable: exit non-zero" [ "$rc" != 0 ]

if [ "$failures" -gt 0 ]; then
	echo "FAIL: $failures check(s) failed; last deploy output:"
	echo "$out"
	exit 1
fi
echo "PASS: all deploy scenarios"
