#!/usr/bin/env bash
# heartbeat.sh: report jobtrack's health to an Uptime Kuma "Push" monitor.
#
# Installed beside deploy.sh in ~/stacks/jobtrack and run from cron every minute:
#
#   * * * * * $HOME/stacks/jobtrack/heartbeat.sh 2>&1 | logger -t jobtrack-heartbeat
#
# Why push instead of letting Kuma poll: Kuma runs in a container that can't reach
# jobtrack. The API listens on the host's loopback only, and tailscale serve accepts
# connections from the tailnet interface only. Putting jobtrack on Kuma's Docker network
# would let anything on that network call the API with a forged Tailscale-User-Login, so
# the host checks /readyz itself and pushes the result. If the pushes stop (Pi or cron
# down), Kuma alerts on the missing heartbeat.
#
# JOBTRACK_KUMA_PUSH_URL in .env is the monitor's push URL (http://127.0.0.1:3001/api/push/
# <token>; anything after '?' is dropped). With none set, this does nothing.
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin

cd "${JOBTRACK_STACK_DIR:-$(cd "$(dirname "$0")" && pwd)}"
env_get() { sed -n "s/^$1=//p" .env | tail -n 1; }

push=$(env_get JOBTRACK_KUMA_PUSH_URL)
[ -n "$push" ] || exit 0
push=${push%%\?*}
port=$(env_get JOBTRACK_PORT)
port=${port:-8090}

# curl prints "000" as the code when nothing answers.
read -r code secs < <(curl -s -o /dev/null -w '%{http_code} %{time_total}\n' --max-time 5 \
	"http://127.0.0.1:$port/readyz" || true)
if [ "$code" = 200 ]; then
	status=up msg="ready"
elif [ "$code" = 000 ]; then
	status=down msg="no answer on 127.0.0.1:$port"
else
	status=down msg="/readyz answered $code"
fi
ms=$(awk -v s="$secs" 'BEGIN { printf "%d", s * 1000 }')

curl -fsS --max-time 10 -o /dev/null -G "$push" \
	--data-urlencode "status=$status" --data-urlencode "msg=$msg" --data-urlencode "ping=$ms"
