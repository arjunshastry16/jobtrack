#!/usr/bin/env bash
# deploy.sh [--force]: pull-based deploy of jobtrack.
#
# Installed beside compose.yml and .env in ~/stacks/jobtrack and run from cron:
#
#   */2 * * * * $HOME/stacks/jobtrack/deploy.sh 2>&1 | logger -t jobtrack-deploy
#
# Each run asks the registry which image the channel tag (:main) points at. If it isn't
# what .env runs, it:
#   1. pulls the new image by digest and writes it into .env,
#   2. runs `docker compose up -d --wait`, so migrations run and the API must turn healthy,
#   3. gates on the host: the api container runs the new image and /readyz answers 200,
#   4. on failure, puts the previous image back, brings it up and checks it the same way.
# Every deploy and rollback goes to ntfy (JOBTRACK_NTFY_URL in .env) and deploys.log.
#
# A digest that failed is remembered (.deploy-failed) and not retried every 2 minutes;
# the next image on the channel is tried as normal, and --force retries the failed one.
# Rollback only swaps the image: migrations aren't reversed, so they must stay
# backwards-compatible (add columns, don't drop or rename them in the same release).
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin

stack_dir=${JOBTRACK_STACK_DIR:-$(cd "$(dirname "$0")" && pwd)}
channel=${JOBTRACK_CHANNEL:-main}
gate_seconds=${JOBTRACK_GATE_SECONDS:-60}
force=
case ${1:-} in
--force) force=1 ;;
"") ;;
*)
	echo "usage: deploy.sh [--force]" >&2
	exit 64
	;;
esac

cd "$stack_dir"
env_file=.env

log() { echo "$(date -Is) $*"; }

# One deploy at a time: a slow deploy must not overlap the next cron run.
exec 9>.deploy.lock
if ! flock -n 9; then
	log "another deploy is still running; skipping"
	exit 0
fi

env_get() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }

current=$(env_get JOBTRACK_IMAGE)
current_digest=${current##*@}
if [[ $current_digest != sha256:* ]]; then
	log "JOBTRACK_IMAGE in $stack_dir/.env must be pinned by digest (…@sha256:…), got: $current"
	exit 1
fi
# The repository: drop @digest, then a :tag (a ':' after the last '/'; a registry
# host:port comes before it).
repo=${current%@*}
[[ ${repo##*/} == *:* ]] && repo=${repo%:*}
port=$(env_get JOBTRACK_PORT)
port=${port:-8090}
ntfy=$(env_get JOBTRACK_NTFY_URL)

# remote_digest REPO TAG: the digest the tag points at, from the registry's API (a HEAD
# request; anonymous token if the registry asks for one). Local registries use http.
remote_digest() {
	local host=${1%%/*} path=${1#*/} scheme=https headers challenge realm service scope token
	case $host in localhost:* | 127.0.0.1:*) scheme=http ;; esac
	local url="$scheme://$host/v2/$path/manifests/$2"
	local accept='application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'
	headers=$(curl -sS -I --max-time 20 -H "Accept: $accept" "$url") || return 1
	if grep -qiE '^HTTP/[0-9.]+ 401' <<<"$headers"; then
		challenge=$(grep -i '^www-authenticate:' <<<"$headers" | tr -d '\r')
		realm=$(sed -n 's/.*realm="\([^"]*\)".*/\1/p' <<<"$challenge")
		service=$(sed -n 's/.*service="\([^"]*\)".*/\1/p' <<<"$challenge")
		scope=$(sed -n 's/.*scope="\([^"]*\)".*/\1/p' <<<"$challenge")
		token=$(curl -sS --max-time 20 -G "$realm" --data-urlencode "service=$service" --data-urlencode "scope=$scope" |
			sed -n 's/.*"token" *: *"\([^"]*\)".*/\1/p')
		headers=$(curl -sS -I --max-time 20 -H "Accept: $accept" -H "Authorization: Bearer $token" "$url") || return 1
	fi
	grep -qiE '^HTTP/[0-9.]+ 200' <<<"$headers" || return 1
	grep -i '^docker-content-digest:' <<<"$headers" | awk '{print $2}' | tr -d '\r'
}

# label IMAGE: a readable name for an image ref, e.g. sha-e53e4ee, else the short digest.
label() {
	local tag=${1%@*}
	if [[ ${tag##*/} == *:* ]]; then echo "${tag##*:}"; else echo "${1##*@sha256:}" | cut -c1-12; fi
}

notify() { # notify TITLE MESSAGE [PRIORITY]
	log "notify: $1: $2"
	[ -n "$ntfy" ] || return 0
	curl -fsS --max-time 10 -o /dev/null -H "Title: $1" -H "Priority: ${3:-default}" -H "Tags: rocket" \
		-d "$2" "$ntfy" || log "ntfy failed"
}

record() { echo "$(date -Is) $*" >>deploys.log; }

# set_image REF: rewrite JOBTRACK_IMAGE in .env (via a private temp file and a rename, so
# .env is never half-written and stays readable only by its owner).
set_image() {
	local tmp
	tmp=$(mktemp "$env_file.XXXXXX")
	awk -v img="$1" '
		/^JOBTRACK_IMAGE=/ { print "JOBTRACK_IMAGE=" img; done = 1; next }
		{ print }
		END { if (!done) print "JOBTRACK_IMAGE=" img }
	' "$env_file" >"$tmp"
	mv "$tmp" "$env_file"
}

# up REF: start the stack on REF and gate on it: compose waits for migrations and a
# healthy api, then the host checks the api runs REF and /readyz answers on the port.
up() {
	local want id deadline
	# pipefail: this fails if compose fails, though tail succeeds.
	docker compose up -d --wait --wait-timeout "$gate_seconds" 2>&1 | tail -n 5 || return 1
	want=$(docker image inspect --format '{{.Id}}' "$1") || return 1
	deadline=$((SECONDS + gate_seconds))
	while [ "$SECONDS" -lt "$deadline" ]; do
		id=$(docker compose ps -q api)
		if [ -n "$id" ] && [ "$(docker inspect --format '{{.Image}}' "$id")" = "$want" ] &&
			curl -fsS -o /dev/null --max-time 3 "http://127.0.0.1:$port/readyz"; then
			return 0
		fi
		sleep 2
	done
	return 1
}

# prune: remove this repository's images other than the running one and the previous one
# (the rollback target), so old releases don't pile up on the SD card.
prune() {
	local keep id
	keep=$(docker image inspect --format '{{.Id}}' "$1" "$2" 2>/dev/null || true)
	for id in $(docker images --no-trunc --quiet "$repo" | sort -u); do
		grep -qx "$id" <<<"$keep" || docker image rm "$id" >/dev/null 2>&1 || true
	done
}

target=$(remote_digest "$repo" "$channel") || {
	log "couldn't read $repo:$channel from the registry"
	exit 1
}
[ -n "$target" ] || {
	log "the registry returned no digest for $repo:$channel"
	exit 1
}
[ "$target" = "$current_digest" ] && exit 0
if [ -z "$force" ] && [ "$(cat .deploy-failed 2>/dev/null)" = "$target" ]; then
	exit 0 # failed before; logged then. --force retries it.
fi

log "deploying $repo:$channel = $target (running $(label "$current"))"
docker pull -q "$repo@$target" >/dev/null
version=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$repo@$target")
if [[ $version =~ ^[0-9a-f]{7,40}$ ]]; then
	new="$repo:sha-$version@$target"
else
	new="$repo@$target"
fi

set_image "$new"
if up "$new"; then
	rm -f .deploy-failed
	record "deployed $(label "$new") (was $(label "$current"))"
	notify "jobtrack deployed $(label "$new")" "Was $(label "$current"). Healthy and serving."
	prune "$new" "$current"
	exit 0
fi

log "$(label "$new") failed its health gate; recent logs:"
docker compose logs --no-color --tail 20 migrate api 2>&1 || true
echo "$target" >.deploy-failed
set_image "$current"
if up "$current"; then
	record "FAILED $(label "$new"), rolled back to $(label "$current")"
	notify "jobtrack deploy failed" "$(label "$new") didn't become healthy; rolled back to $(label "$current"), which is serving." high
	exit 1
fi
record "FAILED $(label "$new") and rollback to $(label "$current") FAILED"
notify "jobtrack is DOWN" "$(label "$new") failed and rolling back to $(label "$current") failed too. Check: docker compose -f $stack_dir/compose.yml ps" urgent
exit 2
