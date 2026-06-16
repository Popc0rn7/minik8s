#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"
COUNT="${COUNT:-24}"

rm -f /tmp/harbor-incident-triage-*.out
for i in $(seq 1 "$COUNT"); do
  "$MINIK8S" invoke workflow harbor-incident-triage --data "$(cat "$DEMO_ROOT/inputs/network-incident.json")" \
    > "/tmp/harbor-incident-triage-$i.out" 2>&1 &
done

for _ in $(seq 1 10); do
  "$CLI" get replicasets | grep -E 'fn-(normalize-input|tiny-log-classifier|notify-captain|network-diagnose)' || true
  sleep 2
done

wait
grep -L 'workflow/harbor-incident-triage invoked' /tmp/harbor-incident-triage-*.out || true
