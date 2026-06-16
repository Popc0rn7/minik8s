#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/acceptance/lib/common.sh
source "$ROOT/scripts/acceptance/lib/common.sh"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

{
  begin "unit acceptance"
  step "run succeeds"
  run bash -c 'printf ok'
  mark_limited "network tools unavailable"
  end
} >"$tmp"

grep -F "[BEGIN] unit acceptance" "$tmp" >/dev/null
grep -F "[STEP] run succeeds" "$tmp" >/dev/null
grep -F "[RUN] bash -c printf ok" "$tmp" >/dev/null
grep -F "[EXIT] 0" "$tmp" >/dev/null
grep -F "[OUTPUT]" "$tmp" >/dev/null
grep -F "ok" "$tmp" >/dev/null
grep -F "[LIMITED] network tools unavailable" "$tmp" >/dev/null
grep -F "[END] status=LIMITED" "$tmp" >/dev/null

printf 'common_test: PASS\n'
