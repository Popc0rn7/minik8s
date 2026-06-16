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
  exit 1
}

end() {
  printf '[END] status=%s\n' "$STATUS"
}
