#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

"$MINIK8S" invoke workflow harbor-incident-triage --data "$(cat "$DEMO_ROOT/inputs/runtime-incident.json")"
