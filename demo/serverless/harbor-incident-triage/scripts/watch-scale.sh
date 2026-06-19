#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

PATTERN="${1:-tiny-log-classifier}"
INTERVAL="${INTERVAL:-2}"
VERBOSE="${VERBOSE:-0}"

manifest_file() {
  local candidate
  for candidate in \
    "$REPO_ROOT/manifests/serverless/harbor-incident-triage/functions/$PATTERN.yaml" \
    "$REPO_ROOT/manifest/serverless/harbor-incident-triage/functions/$PATTERN.yaml"; do
    if [[ -f "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

yaml_value() {
  local file="$1"
  local key="$2"
  awk -F: -v key="$key" '
    $1 ~ "^[[:space:]]*" key "$" {
      value = $2
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      print value
      exit
    }
  ' "$file"
}

matching_lines() {
  local text="$1"
  local header="$2"
  local pattern="$3"
  {
    printf '%s\n' "$text" | head -n 1
    printf '%s\n' "$text" | grep -E "$pattern" || true
  } | awk 'NF > 0'
}

first_replica_pair() {
  local text="$1"
  local pattern="$2"
  printf '%s\n' "$text" | awk -v pattern="$pattern" '
    $0 ~ pattern {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^[0-9]+\/[0-9]+$/) {
          print $i
          exit
        }
      }
    }
  ' | head -n 1
}

replicaset_pair() {
  local text="$1"
  local pattern="$2"
  printf '%s\n' "$text" | awk -v pattern="$pattern" '
    $0 ~ pattern {
      for (i = 1; i <= NF; i++) {
        if ($i ~ pattern && (i + 2) <= NF) {
          print $(i + 1) "/" $(i + 2)
          exit
        }
      }
    }
  ' | head -n 1
}

pod_names() {
  local text="$1"
  local pattern="$2"
  printf '%s\n' "$text" | grep -E "$pattern" | grep -oE "$pattern[^[:space:]]*" | sort -u || true
}

describe_pod_brief() {
  local pod_name="$1"
  local status="-"
  local ip="-"
  local node="-"
  local line

  while IFS= read -r line; do
    case "$line" in
      Status:*) status="${line#Status: }" ;;
      IP:*) ip="${line#IP: }" ;;
      Node:*) node="${line#Node: }" ;;
    esac
  done < <("$CLI" describe pod "$pod_name" 2>/dev/null || true)

  printf '%-42s %-14s %-15s %s\n' "$pod_name" "$status" "$ip" "$node"
}

change_note() {
  local label="$1"
  local previous="$2"
  local current="$3"
  if [[ -z "$previous" || "$previous" == "$current" ]]; then
    printf '%-18s %s\n' "$label" "$current"
    return
  fi
  printf '%-18s %s  (%s -> %s)\n' "$label" "$current" "$previous" "$current"
}

divider() {
  printf '\n==== %s ====\n' "$1"
}

MANIFEST="$(manifest_file || true)"
MIN_REPLICAS="-"
MAX_REPLICAS="-"
TARGET_CONCURRENCY="-"
IDLE_TIMEOUT="-"
RUNTIME="-"
if [[ -n "$MANIFEST" ]]; then
  RUNTIME="$(yaml_value "$MANIFEST" runtime || true)"
  MIN_REPLICAS="$(yaml_value "$MANIFEST" minReplicas || true)"
  MAX_REPLICAS="$(yaml_value "$MANIFEST" maxReplicas || true)"
  TARGET_CONCURRENCY="$(yaml_value "$MANIFEST" targetConcurrency || true)"
  IDLE_TIMEOUT="$(yaml_value "$MANIFEST" idleTimeoutSeconds || true)"
fi

PREV_FUNCTION_REPLICAS=""
PREV_RS_REPLICAS=""
PREV_POD_COUNT=""

while true; do
  functions="$("$CLI" get functions 2>&1 || true)"
  replicasets="$("$CLI" get replicasets 2>&1 || true)"
  pods="$("$CLI" get pods 2>&1 || true)"

  function_replicas="$(first_replica_pair "$functions" "$PATTERN")"
  function_replicas="${function_replicas:-0/0}"
  rs_replicas="$(replicaset_pair "$replicasets" "fn-$PATTERN")"
  rs_replicas="${rs_replicas:-0/0}"
  pod_count="$(printf '%s\n' "$pods" | awk -v pattern="fn-$PATTERN" '$0 ~ pattern { c++ } END { print c + 0 }')"
  running_count="$(printf '%s\n' "$pods" | awk -v pattern="fn-$PATTERN" '$0 ~ pattern && $0 ~ /Running/ { c++ } END { print c + 0 }')"

  printf '\033[H\033[2J'
  # printf 'harbor-incident-triage serverless dashboard\n'
  # printf 'time=%s target=%s interval=%ss\n' "$(date '+%H:%M:%S')" "$PATTERN" "$INTERVAL"
  # printf 'harbor=%s nats=%s\n' "${MINIK8S_HARBOR:-http://127.0.0.1:18080}" "${MINIK8S_NATS_URL:--}"
  # printf 'manifest=%s\n\n' "${MANIFEST:-not found}"

  # printf 'CONFIG\n'
  # printf 'runtime=%s min=%s max=%s targetConcurrency=%s idleTimeout=%ss\n\n' \
  #   "${RUNTIME:--}" "${MIN_REPLICAS:--}" "${MAX_REPLICAS:--}" "${TARGET_CONCURRENCY:--}" "${IDLE_TIMEOUT:--}"

  divider "SUMMARY"
  change_note 'function replicas' "$PREV_FUNCTION_REPLICAS" "$function_replicas"
  change_note 'rs desired/current' "$PREV_RS_REPLICAS" "$rs_replicas"
  change_note 'pods total' "$PREV_POD_COUNT" "$pod_count"
  printf '%-18s %s/%s\n' 'pods running' "$running_count" "$pod_count"

  divider "FUNCTION"
  matching_lines "$functions" FUNCTION "$PATTERN"

  divider "REPLICASET"
  matching_lines "$replicasets" REPLICASET "fn-$PATTERN"

  divider "PODS"
  matching_lines "$pods" POD "fn-$PATTERN"

  divider "POD PLACEMENT"
  printf '%-42s %-14s %-15s %s\n' POD STATUS IP NODE
  while IFS= read -r pod_name; do
    [[ -n "$pod_name" ]] || continue
    describe_pod_brief "$pod_name"
  done < <(pod_names "$pods" "fn-$PATTERN")

  if [[ "$VERBOSE" == "1" ]]; then
    divider "RAW FUNCTION DESCRIBE"
    "$CLI" describe function "$PATTERN" 2>/dev/null || true
  fi

  PREV_FUNCTION_REPLICAS="$function_replicas"
  PREV_RS_REPLICAS="$rs_replicas"
  PREV_POD_COUNT="$pod_count"

  sleep "$INTERVAL"
done
