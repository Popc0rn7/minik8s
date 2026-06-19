#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

"$CLI" apply -f "$DEMO_ROOT/workflow.yaml"
"$CLI" apply -f "$DEMO_ROOT/eventtrigger.yaml"
