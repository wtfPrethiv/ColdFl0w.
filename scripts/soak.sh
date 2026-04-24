#!/bin/sh
# Use an isolated DEMO installation; this does not exercise live providers.
set -eu
binary=${1:?Usage: soak.sh BINARY EMPTY_DATA_DIRECTORY [SECONDS]}
data=${2:?Specify an empty demo data directory}
duration=${3:-86400}
if [ -e "$data/config.json" ]; then echo 'Use a new empty data directory' >&2; exit 1; fi
"$binary" --data "$data" init --demo --non-interactive
"$binary" --data "$data" serve > "$data/soak.log" 2>&1 &
service_pid=$!
trap 'kill "$service_pid" 2>/dev/null || true' EXIT INT TERM
echo 'utc,rss_kib,cpu_percent' > "$data/metrics.csv"
elapsed=0
while [ "$elapsed" -lt "$duration" ]; do
    kill -0 "$service_pid"
    metrics=$(ps -p "$service_pid" -o rss= -o %cpu= | awk '{print $1","$2}')
    echo "$(date -u +%FT%TZ),$metrics" >> "$data/metrics.csv"
    "$binary" --data "$data" status
    sleep 60
    elapsed=$((elapsed + 60))
done
echo "Completed $duration seconds. Metrics: $data/metrics.csv"
