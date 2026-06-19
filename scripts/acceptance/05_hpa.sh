#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/05_hpa.sh
  bash scripts/acceptance/05_hpa.sh cleanup
  bash scripts/acceptance/05_hpa.sh --help

Run on node-a after 01_node_multinode.sh has started bridge, all sailers,
mooring CNI, kube-proxy, and the metrics addon. This script uses fixed
manifests under manifests/hpa/ in the deployed tree or manifests/hpa/ in the
source tree.

Sections:
  05.1 HPA config and creation
  05.2 HPA scale timing with real CPU load
  05.3 HPA configurable speed policy
  05.4 HPA post-scale Service access

Preparatory checks, cleanup, and polling are quiet unless they fail. Evidence
commands print [RUN], [EXIT], [OUTPUT], and a conclusion for TA-readable logs.
EOF
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/acceptance/env.sh
source "$ROOT/scripts/acceptance/env.sh"
# shellcheck source=scripts/acceptance/lib/common.sh
source "$ROOT/scripts/acceptance/lib/common.sh"

REMOTE_DIR="${MINIK8S_REMOTE_DIR:-/opt/minik8s}"
KUBECTL_BIN="${KUBECTL_BIN:-$REMOTE_DIR/bin/kubectl}"
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_05_WAIT_ATTEMPTS:-${MINIK8S_ACCEPTANCE_WAIT_ATTEMPTS:-90}}"
WAIT_SLEEP_SECONDS="${MINIK8S_ACCEPTANCE_WAIT_SLEEP_SECONDS:-2}"
HTTP_TIMEOUT_SECONDS="${MINIK8S_ACCEPTANCE_HTTP_TIMEOUT_SECONDS:-3}"
LOCAL_NODE_IP="${MINIK8S_NODE_A_IP:-192.168.1.4}"
NODEPORT="${MINIK8S_ACCEPTANCE_05_NODEPORT:-30082}"
STRESS_IMAGE="${MINIK8S_ACCEPTANCE_05_STRESS_IMAGE:-polinux/stress:1.0.4}"

RS_NAME="rs-05-hpa"
HPA_NAME="hpa-05-web"
SERVICE_NAME="rs-05-hpa"
CASE_LABEL="minik8s-acceptance-05"

SECTION_STATUS="PASS"
SECTION_PASS_COUNT=0
SECTION_TOTAL=4
PREFLIGHT_DONE=0
OBSERVED_MAX_REPLICAS=0
OBSERVED_MIN_REPLICAS=999

manifest_dir() {
  if [ -d "$REMOTE_DIR/manifests/hpa" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/hpa"
    return 0
  fi
  printf '%s\n' "$ROOT/manifests/hpa"
}

MANIFEST_DIR="$(manifest_dir)"
RS_MANIFEST="$MANIFEST_DIR/replicaset_05_acceptance.yaml"
SERVICE_MANIFEST="$MANIFEST_DIR/service_05_acceptance.yaml"
HPA_MANIFEST="$MANIFEST_DIR/hpa_05_acceptance.yaml"
HPA_FAST_MANIFEST="$MANIFEST_DIR/hpa_05_fast.yaml"

section_begin() {
  SECTION_STATUS="PASS"
  begin "$1"
}

section_fail() {
  SECTION_STATUS="FAIL"
  printf '[FAIL] %s\n' "$*"
}

section_end() {
  cleanup "$1"
  printf '[END] status=%s\n' "$SECTION_STATUS"
  if [ "$SECTION_STATUS" = "PASS" ]; then
    SECTION_PASS_COUNT=$((SECTION_PASS_COUNT + 1))
  fi
}

command_line() {
  local arg
  for arg in "$@"; do
    printf '%q ' "$arg"
  done | sed 's/[[:space:]]$//'
}

quiet_check() {
  local message="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    return 0
  fi
  section_fail "$message"
  return 1
}

quiet_run() {
  local message="$1"
  shift
  local out code
  set +e
  out="$("$@" 2>&1)"
  code=$?
  set -e
  if [ "$code" -eq 0 ]; then
    return 0
  fi
  printf '[RUN] %s\n' "$(command_line "$@")"
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  section_fail "$message"
  return 1
}

evidence_run() {
  local message="$1"
  shift
  local out code
  printf '[RUN] %s\n' "$(command_line "$@")"
  set +e
  out="$("$@" 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  if [ "$code" -eq 0 ]; then
    pass "$message"
    return 0
  fi
  section_fail "$message"
  return 1
}

pods_table() {
  "$KUBECTL_BIN" get pods
}

rs_yaml() {
  "$KUBECTL_BIN" get rs "$RS_NAME" -o yaml
}

service_yaml() {
  "$KUBECTL_BIN" get svc "$SERVICE_NAME" -o yaml
}

rs_pod_names() {
  pods_table | awk -v case_label="case=$CASE_LABEL" '
    NR > 1 && index($0, case_label) {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^rs-05-hpa/) {
          print $i
          break
        }
      }
    }
  '
}

running_rs_pod_names() {
  pods_table | awk -v case_label="case=$CASE_LABEL" '
    NR > 1 && index($0, case_label) && index($0, "Running") {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^rs-05-hpa/) {
          print $i
          break
        }
      }
    }
  '
}

running_rs_pod_count() {
  running_rs_pod_names | sed '/^$/d' | wc -l | tr -d ' '
}

pod_node() {
  "$KUBECTL_BIN" get pod "$1" -o yaml | sed -n 's/^[[:space:]]*nodeName:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

pod_ip() {
  "$KUBECTL_BIN" get pod "$1" -o yaml | sed -n 's/^[[:space:]]*podIP:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

rs_desired() {
  local value
  value="$("$KUBECTL_BIN" describe rs "$RS_NAME" 2>/dev/null | awk '$1 == "Desired:" { print $2; exit }')"
  if [ -n "$value" ]; then
    printf '%s\n' "$value"
    return 0
  fi
  rs_yaml | sed -n 's/^[[:space:]]*replicas:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

rs_current() {
  local value
  value="$("$KUBECTL_BIN" describe rs "$RS_NAME" 2>/dev/null | awk '$1 == "Current:" { print $2; exit }')"
  if [ -n "$value" ]; then
    printf '%s\n' "$value"
    return 0
  fi
  rs_yaml | sed -n 's/^[[:space:]]*currentReplicas:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

endpoint_count() {
  service_yaml | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | sed '/^$/d' | wc -l | tr -d ' '
}

node_port_of() {
  service_yaml | sed -n 's/^[[:space:]]*nodePort:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

rs_summary() {
  printf 'replicaset=%s desired=%s current=%s runningPods=%s selector=app=%s,case=%s\n' \
    "$RS_NAME" "$(rs_desired 2>/dev/null || printf '<unknown>')" "$(rs_current 2>/dev/null || printf '<unknown>')" \
    "$(running_rs_pod_count 2>/dev/null || printf '0')" "$RS_NAME" "$CASE_LABEL"
}

pod_summary() {
  local pod_name node ip
  for pod_name in $(running_rs_pod_names | sort); do
    node="$(pod_node "$pod_name")"
    ip="$(pod_ip "$pod_name")"
    printf 'pod=%s node=%s podIP=%s labels=app=%s,case=%s\n' "$pod_name" "$node" "$ip" "$RS_NAME" "$CASE_LABEL"
  done
}

service_summary() {
  local yaml type cluster_ip node_port port target_port endpoints
  yaml="$(service_yaml)"
  type="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*type:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  cluster_ip="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*clusterIP:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  node_port="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*nodePort:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  port="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*port:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  target_port="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*targetPort:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  endpoints="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | tr -d '"' | paste -sd ',' -)"
  printf 'service=%s type=%s selector=app=%s,case=%s clusterIP=%s port=%s targetPort=%s nodePort=%s endpoints=%s endpointCount=%s\n' \
    "$SERVICE_NAME" "$type" "$RS_NAME" "$CASE_LABEL" "$cluster_ip" "$port" "$target_port" "${node_port:-<none>}" "${endpoints:-<none>}" "$(endpoint_count)"
}

hpa_manifest_summary() {
  awk '
    /^[[:space:]]*name:/ && hpaName == "" { hpaName=$2 }
    /^[[:space:]]*scaleTargetRef:/ { inTarget=1; next }
    inTarget && /^[[:space:]]*kind:/ { targetKind=$2; next }
    inTarget && /^[[:space:]]*name:/ { targetName=$2; inTarget=0; next }
    /^[[:space:]]*minReplicas:/ { min=$2 }
    /^[[:space:]]*maxReplicas:/ { max=$2 }
    /^[[:space:]]*behavior:/ { inBehavior=1; inScaleUp=0; inScaleDown=0; next }
    inBehavior && /^[[:space:]]*syncIntervalSeconds:/ { syncInterval=$2; next }
    inBehavior && /^[[:space:]]*scaleUp:/ { inScaleUp=1; inScaleDown=0; next }
    inBehavior && /^[[:space:]]*scaleDown:/ { inScaleUp=0; inScaleDown=1; next }
    inBehavior && inScaleUp && /^[[:space:]]*maxReplicaDeltaPerSync:/ { scaleUpDelta=$2; next }
    inBehavior && inScaleDown && /^[[:space:]]*maxReplicaDeltaPerSync:/ { scaleDownDelta=$2; next }
    inBehavior && inScaleDown && /^[[:space:]]*cooldownSeconds:/ { scaleDownCooldown=$2; next }
    /^[[:space:]]*metrics:/ { inBehavior=0; inScaleUp=0; inScaleDown=0; next }
    /^[[:space:]]*name:/ && ($2 == "cpu" || $2 == "memory") { metric=$2; next }
    /^[[:space:]]*averageUtilization:/ && metric != "" { metrics=metrics metric "=" $2 " "; metric="" }
    END {
      printf "hpa=%s target=%s/%s minReplicas=%s maxReplicas=%s behavior=syncIntervalSeconds=%s scaleUp.maxReplicaDeltaPerSync=%s scaleDown.maxReplicaDeltaPerSync=%s scaleDown.cooldownSeconds=%s metrics=%s\n", hpaName, targetKind, targetName, min, max, syncInterval, scaleUpDelta, scaleDownDelta, scaleDownCooldown, metrics
    }
  ' "$1"
}

hpa_yaml() {
  "$KUBECTL_BIN" get hpa "$HPA_NAME" -o yaml
}

hpa_current_replicas() {
  hpa_yaml | sed -n 's/^[[:space:]]*currentReplicas:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

hpa_desired_replicas() {
  hpa_yaml | sed -n 's/^[[:space:]]*desiredReplicas:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

hpa_metrics_text() {
  local value
  value="$("$KUBECTL_BIN" describe hpa "$HPA_NAME" 2>/dev/null | sed -n 's/^Metrics:[[:space:]]*//p' | head -n 1)"
  printf '%s\n' "${value:-<none>}"
}

hpa_condition_text() {
  local value
  value="$("$KUBECTL_BIN" describe hpa "$HPA_NAME" 2>/dev/null | sed -n 's/^Conditions:[[:space:]]*//p' | head -n 1)"
  printf '%s\n' "${value:-<none>}"
}

hpa_cpu_utilization() {
  hpa_metrics_text | sed -n 's/.*cpu=\([0-9][0-9]*\)%.*/\1/p' | head -n 1
}

metrics_summary_file() {
  local file="$1"
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$file" "$RS_NAME" <<'PY'
import json
import sys

path, target = sys.argv[1], sys.argv[2]
with open(path, "r", encoding="utf-8") as f:
    data = json.load(f)

items = data.get("items", data if isinstance(data, list) else [])
for item in items:
    name = item.get("metadata", {}).get("name", item.get("name", ""))
    if target not in name:
        continue
    timestamp = item.get("timestamp", item.get("metadata", {}).get("timestamp", ""))
    containers = item.get("containers", [])
    cpu = ",".join(c.get("usage", {}).get("cpu", "?") for c in containers) or "?"
    memory = ",".join(c.get("usage", {}).get("memory", "?") for c in containers) or "?"
    print(f"pod={name} cpu={cpu} memory={memory} timestamp={timestamp}")
PY
    return 0
  fi
  tr -d '\n' <"$file" | sed "s/},{/},\n{/g" | grep -F "$RS_NAME" | head -n 5
}

metrics_compact_file() {
  local file="$1"
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$file" "$RS_NAME" <<'PY'
import json
import sys

path, target = sys.argv[1], sys.argv[2]
with open(path, "r", encoding="utf-8") as f:
    data = json.load(f)

items = data.get("items", data if isinstance(data, list) else [])
parts = []
for item in items:
    name = item.get("metadata", {}).get("name", item.get("name", ""))
    if target not in name:
        continue
    timestamp = item.get("timestamp", item.get("metadata", {}).get("timestamp", ""))
    containers = item.get("containers", [])
    cpu = "+".join(c.get("usage", {}).get("cpu", "?") for c in containers) or "?"
    memory = "+".join(c.get("usage", {}).get("memory", "?") for c in containers) or "?"
    parts.append(f"{name}:cpu={cpu},memory={memory},ts={timestamp}")
print(";".join(parts) if parts else "<none>")
PY
    return 0
  fi
  metrics_summary_file "$file" | paste -sd ';' -
}

refresh_metrics_file() {
  curl -fsS "$MINIK8S_HARBOR/apis/metrics.k8s.io/v1beta1/pods" >/tmp/minik8s-acceptance-05-metrics.json
}

hpa_observation_row() {
  local phase="$1"
  local decision="$2"
  local now metrics_samples
  now="$(date -Is)"
  if refresh_metrics_file 2>/dev/null; then
    metrics_samples="$(metrics_compact_file /tmp/minik8s-acceptance-05-metrics.json)"
  else
    metrics_samples="<metrics-api-unavailable>"
  fi
  printf 'phase=%s time=%s hpaMetrics=%s podMetrics=%s hpaCurrent=%s hpaDesired=%s rsDesired=%s rsCurrent=%s runningPods=%s endpoints=%s condition=%s decision=%s\n' \
    "$phase" "$now" "$(hpa_metrics_text)" "$metrics_samples" \
    "$(hpa_current_replicas 2>/dev/null || printf '?')" "$(hpa_desired_replicas 2>/dev/null || printf '?')" \
    "$(rs_desired 2>/dev/null || printf '?')" "$(rs_current 2>/dev/null || printf '?')" \
    "$(running_rs_pod_count 2>/dev/null || printf '0')" "$(endpoint_count 2>/dev/null || printf '0')" \
    "$(hpa_condition_text)" "$decision"
}

record_observed_replicas() {
  local count
  count="$(running_rs_pod_count 2>/dev/null || printf '0')"
  if [ "$count" -gt "$OBSERVED_MAX_REPLICAS" ]; then
    OBSERVED_MAX_REPLICAS="$count"
  fi
  if [ "$count" -lt "$OBSERVED_MIN_REPLICAS" ]; then
    OBSERVED_MIN_REPLICAS="$count"
  fi
}

wait_for_rs_running() {
  local expected="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local count
    count="$(running_rs_pod_count 2>/dev/null || printf '0')"
    if [ "$count" -eq "$expected" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$RS_NAME final Pod list before Running count failure" "$KUBECTL_BIN" get pods || true
  section_fail "$RS_NAME did not reach $expected Running Pods"
  return 1
}

wait_for_rs_desired() {
  local expected="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local desired current
    desired="$(rs_desired 2>/dev/null || true)"
    current="$(rs_current 2>/dev/null || true)"
    if [ "${desired:-}" = "$expected" ] && [ "${current:-}" = "$expected" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$RS_NAME final describe before status failure" "$KUBECTL_BIN" describe rs "$RS_NAME" || true
  section_fail "$RS_NAME status did not become Desired=$expected Current=$expected"
  return 1
}

wait_for_service_endpoints() {
  local expected="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local count
    count="$(endpoint_count 2>/dev/null || printf '0')"
    if [ "$count" -eq "$expected" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$SERVICE_NAME final describe before endpoint failure" "$KUBECTL_BIN" describe svc "$SERVICE_NAME" || true
  section_fail "$SERVICE_NAME endpoint count did not become $expected"
  return 1
}

wait_for_metrics() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl -fsS "$MINIK8S_HARBOR/apis/metrics.k8s.io/v1beta1/pods" >/tmp/minik8s-acceptance-05-metrics.json 2>/dev/null &&
      grep -Fq "$RS_NAME" /tmp/minik8s-acceptance-05-metrics.json; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "fresh Pod metrics for $RS_NAME were not observed"
  return 1
}

wait_for_rs_above() {
  local threshold="$1"
  local label="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local desired current running endpoints
    desired="$(rs_desired 2>/dev/null || printf '0')"
    current="$(rs_current 2>/dev/null || printf '0')"
    running="$(running_rs_pod_count 2>/dev/null || printf '0')"
    endpoints="$(endpoint_count 2>/dev/null || printf '0')"
    record_observed_replicas
    if [ "$desired" -gt "$threshold" ] || [ "$current" -gt "$threshold" ] || [ "$running" -gt "$threshold" ] || [ "$endpoints" -gt "$threshold" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$RS_NAME final describe before $label failure" "$KUBECTL_BIN" describe rs "$RS_NAME" || true
  evidence_run "$HPA_NAME final describe before $label failure" "$KUBECTL_BIN" describe hpa "$HPA_NAME" || true
  section_fail "$label did not observe replicas above $threshold"
  return 1
}

wait_for_rs_below() {
  local threshold="$1"
  local label="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local desired current running endpoints
    desired="$(rs_desired 2>/dev/null || printf "$threshold")"
    current="$(rs_current 2>/dev/null || printf "$threshold")"
    running="$(running_rs_pod_count 2>/dev/null || printf "$threshold")"
    endpoints="$(endpoint_count 2>/dev/null || printf "$threshold")"
    record_observed_replicas
    if [ "$desired" -lt "$threshold" ] || [ "$current" -lt "$threshold" ] || [ "$running" -lt "$threshold" ] || [ "$endpoints" -lt "$threshold" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$RS_NAME final describe before $label failure" "$KUBECTL_BIN" describe rs "$RS_NAME" || true
  evidence_run "$HPA_NAME final describe before $label failure" "$KUBECTL_BIN" describe hpa "$HPA_NAME" || true
  section_fail "$label did not observe replicas below $threshold"
  return 1
}

wait_for_cpu_below() {
  local threshold="$1"
  local label="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local cpu
    cpu="$(hpa_cpu_utilization 2>/dev/null || true)"
    record_observed_replicas
    if [ -n "${cpu:-}" ] && [ "$cpu" -lt "$threshold" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$HPA_NAME final describe before $label failure" "$KUBECTL_BIN" describe hpa "$HPA_NAME" || true
  section_fail "$label did not observe CPU utilization below $threshold%"
  return 1
}

record_replica_path_until() {
  local expected="$1"
  local label="$2"
  local attempt=1
  local last="" path="" max_seen=0 min_seen=999
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local desired current running endpoints elapsed line
    desired="$(rs_desired 2>/dev/null || printf '?')"
    current="$(rs_current 2>/dev/null || printf '?')"
    running="$(running_rs_pod_count 2>/dev/null || printf '0')"
    endpoints="$(endpoint_count 2>/dev/null || printf '0')"
    elapsed=$(((attempt - 1) * WAIT_SLEEP_SECONDS))
    line="t=${elapsed}s:desired=${desired},current=${current},running=${running},endpoints=${endpoints}"
    if [ "$line" != "$last" ]; then
      if [ -n "$path" ]; then
        path="$path -> $line"
      else
        path="$line"
      fi
      last="$line"
    fi
    if [ "$running" -gt "$max_seen" ]; then
      max_seen="$running"
    fi
    if [ "$running" -lt "$min_seen" ]; then
      min_seen="$running"
    fi
    record_observed_replicas
    if [ "$desired" = "$expected" ] && [ "$current" = "$expected" ] && [ "$running" -eq "$expected" ] && [ "$endpoints" -eq "$expected" ]; then
      printf 'label=%s path=%s maxRunning=%s minRunning=%s\n' "$label" "$path" "$max_seen" "$min_seen"
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  printf 'label=%s incompletePath=%s maxRunning=%s minRunning=%s\n' "$label" "${path:-<none>}" "$max_seen" "$min_seen"
  evidence_run "$RS_NAME final describe before $label failure" "$KUBECTL_BIN" describe rs "$RS_NAME" || true
  evidence_run "$SERVICE_NAME final describe before $label failure" "$KUBECTL_BIN" describe svc "$SERVICE_NAME" || true
  section_fail "$label did not reach desired/current/running/endpoints=$expected"
  return 1
}

distinct_running_nodes() {
  local pod_name
  for pod_name in $(running_rs_pod_names | sort); do
    pod_node "$pod_name"
  done | sed '/^$/d' | sort -u | wc -l | tr -d ' '
}

delete_one_running_rs_pod() {
  local pod_name
  pod_name="$(running_rs_pod_names | head -n 1)"
  if [ -z "$pod_name" ]; then
    section_fail "no running $RS_NAME Pod exists for load refresh"
    return 1
  fi
  evidence_run "delete one $RS_NAME Pod to refresh stress sidecar" "$KUBECTL_BIN" delete pod "$pod_name"
}

sample_service_backends() {
  local url="$1"
  local tmp="/tmp/minik8s-acceptance-05-backends.out"
  local attempt=1
  : >"$tmp"
  while [ "$attempt" -le 24 ]; do
    curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >>"$tmp" 2>/dev/null || true
    printf '\n---\n' >>"$tmp"
    sleep 1
    attempt=$((attempt + 1))
  done
  awk '
    function flush() {
      if (pod != "") {
        print "pod:" pod
      } else if (ip != "") {
        print "ip:" ip
      }
      pod = ""
      ip = ""
    }
    /^---$/ { flush(); next }
    /^pod=/ { pod = substr($0, 5); next }
    /^ip=/ { ip = substr($0, 4); next }
    END { flush() }
  ' "$tmp" | sed '/^pod:$/d;/^ip:$/d' | sort -u >/tmp/minik8s-acceptance-05-backends.uniq
  printf 'responses:\n'
  sed '/^$/d' "$tmp" | head -n 80
  printf 'distinctBackends:\n'
  cat /tmp/minik8s-acceptance-05-backends.uniq
  local count
  count="$(wc -l </tmp/minik8s-acceptance-05-backends.uniq | tr -d ' ')"
  printf 'distinctBackendCount=%s\n' "$count"
  test "$count" -ge 2
}

delete_if_exists() {
  local resource="$1"
  local name="$2"
  if "$KUBECTL_BIN" get "$resource" "$name" >/dev/null 2>&1; then
    quiet_run "delete $resource $name" "$KUBECTL_BIN" delete "$resource" "$name" || true
  fi
}

delete_orphan_pods() {
  local pod_name
  for pod_name in $(rs_pod_names 2>/dev/null || true); do
    quiet_run "delete orphan pod $pod_name" "$KUBECTL_BIN" delete pod "$pod_name" || true
  done
}

cleanup_runtime() {
  rm -f /tmp/minik8s-acceptance-05-http.out /tmp/minik8s-acceptance-05-metrics.json \
    /tmp/minik8s-acceptance-05-backends.out /tmp/minik8s-acceptance-05-backends.uniq 2>/dev/null || true
}

cleanup_all() {
  delete_if_exists hpa "$HPA_NAME"
  delete_if_exists svc "$SERVICE_NAME"
  delete_if_exists rs "$RS_NAME"
  delete_orphan_pods
  cleanup_runtime
}

cleanup_trap() {
  cleanup_all >/dev/null 2>&1 || true
}
trap cleanup_trap EXIT

preflight() {
  if [ "$PREFLIGHT_DONE" -eq 1 ]; then
    return 0
  fi
  quiet_check "kubectl binary exists" test -x "$KUBECTL_BIN" || return 1
  evidence_run "Harbor API is reachable" curl -fsS -o /dev/null -w 'http=%{http_code}\n' "$MINIK8S_HARBOR/api/v1" || return 1
  evidence_run "metrics API discovery is reachable" curl -fsS "$MINIK8S_HARBOR/apis/metrics.k8s.io/v1beta1" || return 1
  evidence_run "kubectl can list nodes" "$KUBECTL_BIN" get nodes || return 1
  quiet_check "$MINIK8S_NODE_A_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_A_NAME" || return 1
  quiet_check "CNI is enabled for HPA acceptance" bash -c 'test "${MINIK8S_CNI_DISABLED:-0}" != "1"' || return 1
  quiet_run "$STRESS_IMAGE is preloaded locally" docker image inspect "$STRESS_IMAGE" || return 1
  quiet_run "$STRESS_IMAGE contains real stress binary" docker run --rm --entrypoint sh "$STRESS_IMAGE" -c 'command -v stress && stress --cpu 1 --timeout 1s' || return 1
  quiet_check "05 HPA manifests exist" test -f "$RS_MANIFEST" -a -f "$SERVICE_MANIFEST" -a -f "$HPA_MANIFEST" -a -f "$HPA_FAST_MANIFEST" || return 1
  PREFLIGHT_DONE=1
}

show_hpa_yaml_summary() {
  evidence_run "HPA manifest summary contains target, min/max, behavior rate policy, CPU and Memory metrics" hpa_manifest_summary "$HPA_MANIFEST"
}

apply_rs_and_service() {
  quiet_run "$RS_NAME manifest applied" "$KUBECTL_BIN" apply -f "$RS_MANIFEST" || return 1
  wait_for_rs_running 1 || return 1
  wait_for_rs_desired 1 || return 1
  quiet_run "$SERVICE_NAME Service manifest applied" "$KUBECTL_BIN" apply -f "$SERVICE_MANIFEST" || return 1
  wait_for_service_endpoints 1 || return 1
}

show_target_evidence() {
  evidence_run "$RS_NAME summary" rs_summary &&
    evidence_run "$RS_NAME running Pod summary" pod_summary &&
    evidence_run "$SERVICE_NAME summary" service_summary
}

show_metrics_evidence() {
  wait_for_metrics &&
    evidence_run "pods.metrics.k8s.io contains $RS_NAME CPU/memory samples" metrics_summary_file /tmp/minik8s-acceptance-05-metrics.json
}

section_create_metrics() {
  section_begin "05.1 hpa config creation metrics api acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR manifests=$MANIFEST_DIR"
  preflight &&
    cleanup_all &&
    show_hpa_yaml_summary &&
    apply_rs_and_service &&
    show_target_evidence &&
    show_metrics_evidence &&
    quiet_run "$HPA_NAME HPA manifest applied" "$KUBECTL_BIN" apply -f "$HPA_MANIFEST" &&
    evidence_run "$HPA_NAME get output shows min/max replicas and metrics" "$KUBECTL_BIN" get hpa "$HPA_NAME" &&
    evidence_run "$HPA_NAME describe output shows target kind/name and CPU/Memory metrics" "$KUBECTL_BIN" describe hpa "$HPA_NAME"
  section_end "05.1 kept HPA resources for 05.2"
}

section_scale_timing() {
  section_begin "05.2 hpa scale timing with real cpu load acceptance"
  preflight &&
    show_metrics_evidence &&
    evidence_run "05.2 observation before-load" hpa_observation_row "before-load" "initial one replica with fresh metrics before HPA scale decision" &&
    wait_for_rs_above 1 "scale-up-trigger" &&
    evidence_run "05.2 observation scale-up-trigger" hpa_observation_row "scale-up-trigger" "CPU utilization above target; HPA starts increasing replicas" &&
    evidence_run "05.2 scale-up Pod count path" record_replica_path_until 3 "scale-up-to-max" &&
    evidence_run "05.2 observation after-scale-up" hpa_observation_row "after-scale-up" "RS and Service reached maxReplicas=3" &&
    wait_for_cpu_below 50 "after-load" &&
    evidence_run "05.2 observation after-load" hpa_observation_row "after-load" "stress sidecar finished; CPU below target; waiting for scale-down decision" &&
    wait_for_rs_below 3 "scale-down-trigger" &&
    evidence_run "05.2 observation scale-down-trigger" hpa_observation_row "scale-down-trigger" "low metrics triggered replica reduction" &&
    evidence_run "05.2 scale-down Pod count path" record_replica_path_until 1 "scale-down-to-min" &&
    evidence_run "05.2 observed replica extrema" printf 'observedMaxReplicas=%s expectedMaxReplicas=3 observedMinReplicas=%s expectedMinReplicas=1\n' "$OBSERVED_MAX_REPLICAS" "$OBSERVED_MIN_REPLICAS"
  section_end "05.2 kept scaled-down HPA resources for 05.3"
}

section_scale_speed_policy() {
  section_begin "05.3 hpa configurable speed policy acceptance"
  preflight &&
    wait_for_rs_desired 1 &&
    wait_for_rs_running 1 &&
    wait_for_service_endpoints 1 &&
    evidence_run "05.3 fast HPA manifest summary shows larger per-sync deltas and no scale-down cooldown" hpa_manifest_summary "$HPA_FAST_MANIFEST" &&
    quiet_run "$HPA_NAME fast HPA manifest applied" "$KUBECTL_BIN" apply -f "$HPA_FAST_MANIFEST" &&
    evidence_run "$HPA_NAME describe output shows fast behavior policy" "$KUBECTL_BIN" describe hpa "$HPA_NAME" &&
    delete_one_running_rs_pod &&
    wait_for_rs_running 1 &&
    wait_for_service_endpoints 1 &&
    show_metrics_evidence &&
    evidence_run "05.3 observation before fast scale-up" hpa_observation_row "before-fast-scale-up" "fast behavior is active; replacement Pod restarts stress load" &&
    evidence_run "05.3 fast scale-up Pod count path" record_replica_path_until 3 "fast-scale-up-delta-2" &&
    evidence_run "05.3 observation after fast scale-up" hpa_observation_row "after-fast-scale-up" "scaleUp.maxReplicaDeltaPerSync=2 allows 1 to 3 in one HPA decision" &&
    evidence_run "05.3 observation before fast scale-down" hpa_observation_row "before-fast-scale-down" "waiting for CPU to drop; scaleDown cooldown is 0s and delta is 2" &&
    evidence_run "05.3 fast scale-down Pod count path" record_replica_path_until 1 "fast-scale-down-delta-2-cooldown-0" &&
    evidence_run "05.3 observation after fast scale-down" hpa_observation_row "after-fast-scale-down" "fast behavior reached minReplicas in one HPA decision after load stopped" &&
    quiet_run "$HPA_NAME normal HPA manifest restored for 05.4" "$KUBECTL_BIN" apply -f "$HPA_MANIFEST" &&
    evidence_run "$HPA_NAME restored describe output shows normal behavior policy" "$KUBECTL_BIN" describe hpa "$HPA_NAME"
  section_end "05.3 restored normal HPA resources for 05.4"
}

section_post_scale_access() {
  section_begin "05.4 hpa post-scale service access acceptance"
  preflight &&
    wait_for_rs_desired 1 &&
    wait_for_rs_running 1 &&
    wait_for_service_endpoints 1 &&
    delete_one_running_rs_pod &&
    wait_for_rs_running 1 &&
    wait_for_service_endpoints 1 &&
    show_metrics_evidence &&
    evidence_run "05.4 observation before access scale-up" hpa_observation_row "before-access-scale-up" "fresh replacement Pod restarts stress load" &&
    evidence_run "05.4 access scale-up Pod count path" record_replica_path_until 3 "access-scale-up-to-max" &&
    evidence_run "$RS_NAME Pods after access scale-up" pod_summary &&
    evidence_run "$SERVICE_NAME endpoints after access scale-up" service_summary
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of)"
    quiet_check "$SERVICE_NAME has NodePort $NODEPORT" test "$node_port" = "$NODEPORT" &&
      quiet_check "$RS_NAME Pods are distributed across multiple nodes" test "$(distinct_running_nodes)" -ge 2 &&
      evidence_run "node-a reaches HPA Service and observes multiple backends $LOCAL_NODE_IP:$node_port" sample_service_backends "http://$LOCAL_NODE_IP:$node_port/" &&
      evidence_run "$HPA_NAME final get output" "$KUBECTL_BIN" get hpa "$HPA_NAME" &&
      evidence_run "$HPA_NAME final describe output" "$KUBECTL_BIN" describe hpa "$HPA_NAME"
  fi
  cleanup_all
  section_end "05.4 cleaned HPA resources"
}

main() {
  if [ "${1:-}" = "cleanup" ]; then
    begin "05 hpa acceptance cleanup"
    cleanup_all
    cleanup "05 cleanup completed"
    end
    return 0
  fi

  section_create_metrics
  section_scale_timing
  section_scale_speed_policy
  section_post_scale_access
  printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  if [ "$SECTION_PASS_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
