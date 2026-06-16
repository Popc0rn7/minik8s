#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

"$CLI" apply -f "$DEMO_ROOT/functions/normalize_input/function.yaml"
"$CLI" apply -f "$DEMO_ROOT/functions/tiny_log_classifier/function.yaml"
"$CLI" apply -f "$DEMO_ROOT/functions/network_diagnose/function.yaml"
"$CLI" apply -f "$DEMO_ROOT/functions/runtime_diagnose/function.yaml"
"$CLI" apply -f "$DEMO_ROOT/functions/build_diagnose/function.yaml"
"$CLI" apply -f "$DEMO_ROOT/functions/app_diagnose/function.yaml"
"$CLI" apply -f "$DEMO_ROOT/functions/quick_reply/function.yaml"
"$CLI" apply -f "$DEMO_ROOT/functions/notify_captain/function.yaml"
"$CLI" apply -f "$DEMO_ROOT/functions/compose_report/function.yaml"
