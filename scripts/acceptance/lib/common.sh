#!/usr/bin/env bash

set -u

STATUS="${STATUS:-PASS}"

begin() {
  printf '[BEGIN] %s\n' "$*"
}

step() {
  printf '[STEP] %s\n' "$*"
}

output() {
  printf '[OUTPUT]\n%s\n' "$*"
}

pass() {
  printf '[PASS] %s\n' "$*"
}

run() {
  printf '[RUN] %s\n' "$*"
  local out
  set +e
  out="$("$@" 2>&1)"
  local code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  return "$code"
}

check_run() {
  local message="$1"
  shift
  if run "$@"; then
    pass "$message"
  else
    fail "$message"
  fi
}

mark_partial() {
  if [ "$STATUS" = "PASS" ]; then
    STATUS="PARTIAL"
  fi
  printf '[PARTIAL] %s\n' "$*"
}

mark_limited() {
  if [ "$STATUS" = "PASS" ] || [ "$STATUS" = "PARTIAL" ]; then
    STATUS="LIMITED"
  fi
  printf '[LIMITED] %s\n' "$*"
}

mark_skip() {
  if [ "$STATUS" != "FAIL" ]; then
    STATUS="SKIP"
  fi
  printf '[SKIP] %s\n' "$*"
}

fail() {
  STATUS="FAIL"
  printf '[FAIL] %s\n' "$*"
  if [ -n "${ACCEPTANCE_CLEANUP_ON_FAIL:-}" ]; then
    cleanup "$ACCEPTANCE_CLEANUP_ON_FAIL"
  fi
  end
  exit 1
}

cleanup() {
  printf '[CLEANUP] %s\n' "$*"
}

end() {
  printf '[END] status=%s\n' "$STATUS"
}
