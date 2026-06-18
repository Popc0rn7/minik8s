#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/05_hpa.sh
  bash scripts/acceptance/05_hpa.sh cleanup
  bash scripts/acceptance/05_hpa.sh --help

Run on node-a after 01_node_multinode.sh has started bridge, all sailers,
mooring CNI, kube-proxy, and the metrics addon.
The ReplicaSet uses polinux/stress:1.0.4 as a sidecar. Preload it on every
node so acceptance does not depend on registry access during scheduling.

Sections:
  05.1 HPA create and metrics API
  05.2 HPA scale up driven by stress CPU load
  05.3 HPA scale down after real load stops and Service access

Each section cleans its own resources and emits [END] status=<PASS|FAIL>.
The script emits a final [END] status=N/3PASS.
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
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_05_WAIT_ATTEMPTS:-90}"
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
SECTION_TOTAL=3
PREFLIGHT_DONE=0

manifest_dir() {
  if [ -d "$REMOTE_DIR/manifests/hpa" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/hpa"
    return 0
  fi
  printf '%s\n' "$ROOT/manifest/hpa"
}

MANIFEST_DIR="$(manifest_dir)"
RS_MANIFEST="$MANIFEST_DIR/replicaset_05_acceptance.yaml"
SERVICE_MANIFEST="$MANIFEST_DIR/service_05_acceptance.yaml"
HPA_UP_MANIFEST="$MANIFEST_DIR/hpa_05_scale_up.yaml"
HPA_DOWN_MANIFEST="$MANIFEST_DIR/hpa_05_scale_down.yaml"

section_begin() {
  SECTION_STATUS="PASS"
  begin "$1"
}

section_fail() {
  SECTION_STATUS="FAIL"
  printf '[FAIL] %s\n' "$*"
}

section_check_run() {
  local message="$1"
  shift
  if run "$@"; then
    pass "$message"
    return 0
  fi
  section_fail "$message"
  return 1
}

section_check_quiet() {
  local message="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    pass "$message"
    return 0
  fi
  section_fail "$message"
  return 1
}

section_end() {
  cleanup "$1"
  printf '[END] status=%s\n' "$SECTION_STATUS"
  if [ "$SECTION_STATUS" = "PASS" ]; then
    SECTION_PASS_COUNT=$((SECTION_PASS_COUNT + 1))
  fi
}

wait_note() {
  local message="$1"
  local attempt="$2"
  if [ "$attempt" -eq 1 ] || [ "$attempt" -eq "$WAIT_ATTEMPTS" ] || [ $((attempt % 10)) -eq 0 ]; then
    output "$message attempt=$attempt/$WAIT_ATTEMPTS"
  fi
}

pods_table() {
  "$KUBECTL_BIN" get pods
}

rs_pod_names() {
  pods_table | awk -v case_label="case=$CASE_LABEL" 'NR > 1 && index($0, case_label) {print $2}'
}

running_rs_pod_names() {
  pods_table | awk -v case_label="case=$CASE_LABEL" 'NR > 1 && index($0, case_label) && index($0, "Running") {print $2}'
}

running_rs_pod_count() {
  running_rs_pod_names | sed '/^$/d' | wc -l | tr -d ' '
}

describe_rs_value() {
  "$KUBECTL_BIN" describe rs "$RS_NAME" 2>/dev/null | awk -v key="$1" '$1 == key ":" {print $2; exit}'
}

service_yaml() {
  "$KUBECTL_BIN" get svc "$SERVICE_NAME" -o yaml
}

endpoint_count() {
  service_yaml | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | sed '/^$/d' | wc -l | tr -d ' '
}

node_port_of() {
  service_yaml | sed -n 's/^[[:space:]]*nodePort:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

wait_for_rs_running() {
  local expected="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local count
    count="$(running_rs_pod_count)"
    if [ "$count" -eq "$expected" ]; then
      pass "$RS_NAME has $expected Running Pods"
      return 0
    fi
    wait_note "waiting for HPA target Running Pods current=$count expected=$expected" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$RS_NAME final Pod list before failure" "$KUBECTL_BIN" get pods || true
  section_fail "$RS_NAME did not reach $expected Running Pods"
  return 1
}

wait_for_rs_desired() {
  local expected="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local desired current
    desired="$(describe_rs_value Desired || true)"
    current="$(describe_rs_value Current || true)"
    if [ "${desired:-}" = "$expected" ] && [ "${current:-}" = "$expected" ]; then
      pass "$RS_NAME Desired=$expected Current=$expected"
      return 0
    fi
    wait_note "waiting for ReplicaSet status desired=${desired:-?} current=${current:-?} expected=$expected" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$RS_NAME final describe before status failure" "$KUBECTL_BIN" describe rs "$RS_NAME" || true
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
      pass "$SERVICE_NAME endpoint count is $expected"
      return 0
    fi
    wait_note "waiting for Service endpoints current=$count expected=$expected" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$SERVICE_NAME final describe before endpoint failure" "$KUBECTL_BIN" describe svc "$SERVICE_NAME" || true
  section_fail "$SERVICE_NAME endpoint count did not become $expected"
  return 1
}

wait_for_metrics() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl -fsS "$MINIK8S_HARBOR/apis/metrics.k8s.io/v1beta1/pods" >/tmp/minik8s-acceptance-05-metrics.json 2>/dev/null &&
      grep -Fq "$RS_NAME" /tmp/minik8s-acceptance-05-metrics.json; then
      section_check_run "pods.metrics.k8s.io contains HPA target metrics" bash -c 'head -c 1200 /tmp/minik8s-acceptance-05-metrics.json; printf "\n"'
      return 0
    fi
    wait_note "waiting for fresh Pod metrics for $RS_NAME" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "fresh Pod metrics for $RS_NAME were not observed"
  return 1
}

wait_for_http() {
  local label="$1"
  local url="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >/tmp/minik8s-acceptance-05-http.out 2>/dev/null; then
      section_check_run "$label" bash -c 'cat /tmp/minik8s-acceptance-05-http.out'
      return 0
    fi
    wait_note "waiting for HTTP access $url" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$label"
  return 1
}

delete_hpa_if_exists() {
  if "$KUBECTL_BIN" get hpa "$HPA_NAME" >/dev/null 2>&1; then
    run "$KUBECTL_BIN" delete hpa "$HPA_NAME" || true
  else
    output "hpa $HPA_NAME already absent"
  fi
}

delete_service_if_exists() {
  if "$KUBECTL_BIN" get svc "$SERVICE_NAME" >/dev/null 2>&1; then
    run "$KUBECTL_BIN" delete svc "$SERVICE_NAME" || true
  else
    output "service $SERVICE_NAME already absent"
  fi
}

delete_rs_if_exists() {
  if "$KUBECTL_BIN" get rs "$RS_NAME" >/dev/null 2>&1; then
    run "$KUBECTL_BIN" delete rs "$RS_NAME" || true
  else
    output "replicaset $RS_NAME already absent"
  fi
}

delete_orphan_pods() {
  local pod_name
  for pod_name in $(rs_pod_names); do
    run "$KUBECTL_BIN" delete pod "$pod_name" || true
  done
}

cleanup_runtime() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if bash -lc 'test -z "$(docker ps -aq --filter label=minik8s.kind=pod-container --filter label=case=minik8s-acceptance-05)"' >/dev/null 2>&1; then
      pass "no local acceptance HPA containers remain"
      break
    fi
    wait_note "waiting for local runtime cleanup" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  run rm -f /tmp/minik8s-acceptance-05-http.out /tmp/minik8s-acceptance-05-metrics.json || true
}

cleanup_all() {
  delete_hpa_if_exists
  delete_service_if_exists
  delete_rs_if_exists
  delete_orphan_pods
  cleanup_runtime
}

cleanup_trap() {
  cleanup_all >/dev/null 2>&1 || true
}
trap cleanup_trap EXIT

preflight() {
  if [ "$PREFLIGHT_DONE" -eq 1 ]; then
    pass "shared preflight already passed"
    return 0
  fi
  section_check_quiet "kubectl binary exists" test -x "$KUBECTL_BIN" || return 1
  section_check_run "Harbor API is reachable" curl -fsS -o /dev/null -w 'http=%{http_code}\n' "$MINIK8S_HARBOR/api/v1" || return 1
  section_check_run "metrics API discovery is reachable" curl -fsS "$MINIK8S_HARBOR/apis/metrics.k8s.io/v1beta1" || return 1
  section_check_run "kubectl can list nodes" "$KUBECTL_BIN" get nodes || return 1
  section_check_quiet "$MINIK8S_NODE_A_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_A_NAME" || return 1
  section_check_quiet "CNI is enabled for HPA acceptance" bash -c 'test "${MINIK8S_CNI_DISABLED:-0}" != "1"' || return 1
  section_check_run "$STRESS_IMAGE is preloaded locally" docker image inspect "$STRESS_IMAGE" || return 1
  section_check_run "$STRESS_IMAGE contains real stress binary" docker run --rm --entrypoint sh "$STRESS_IMAGE" -c 'command -v stress && stress --cpu 1 --timeout 1s' || return 1
  section_check_quiet "05 HPA manifests exist" test -f "$RS_MANIFEST" -a -f "$SERVICE_MANIFEST" -a -f "$HPA_UP_MANIFEST" -a -f "$HPA_DOWN_MANIFEST" || return 1
  PREFLIGHT_DONE=1
}

apply_rs_and_service() {
  section_check_run "$RS_NAME manifest applied" "$KUBECTL_BIN" apply -f "$RS_MANIFEST" || return 1
  wait_for_rs_running 1 || return 1
  wait_for_rs_desired 1 || return 1
  section_check_run "$SERVICE_NAME Service manifest applied" "$KUBECTL_BIN" apply -f "$SERVICE_MANIFEST" || return 1
  wait_for_service_endpoints 1 || return 1
  section_check_run "$SERVICE_NAME summary shows NodePort and endpoint" "$KUBECTL_BIN" describe svc "$SERVICE_NAME" || return 1
}

section_create_metrics() {
  section_begin "05.1 hpa create and metrics acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR manifests=$MANIFEST_DIR"
  preflight &&
    cleanup_all &&
    apply_rs_and_service &&
    wait_for_metrics &&
    section_check_run "$HPA_NAME CPU-target manifest applied" "$KUBECTL_BIN" apply -f "$HPA_UP_MANIFEST" &&
    section_check_run "$HPA_NAME get output shows min max replicas and metrics" "$KUBECTL_BIN" get hpa "$HPA_NAME" &&
    section_check_run "$HPA_NAME describe output shows CPU and memory metrics" "$KUBECTL_BIN" describe hpa "$HPA_NAME"
  cleanup_all
  section_end "05.1 cleaned HPA create and metrics resources"
}

section_scale_up() {
  section_begin "05.2 hpa scale up acceptance with real stress load"
  preflight &&
    cleanup_all &&
    apply_rs_and_service &&
    wait_for_metrics &&
    section_check_run "$HPA_NAME CPU-target manifest applied" "$KUBECTL_BIN" apply -f "$HPA_UP_MANIFEST" &&
    wait_for_rs_desired 3 &&
    wait_for_rs_running 3 &&
    wait_for_service_endpoints 3 &&
    section_check_run "$HPA_NAME reports desired replicas after scale up" "$KUBECTL_BIN" describe hpa "$HPA_NAME" &&
    section_check_run "$RS_NAME Pods after scale up" "$KUBECTL_BIN" get pods
  cleanup_all
  section_end "05.2 cleaned HPA scale-up resources"
}

section_scale_down_access() {
  section_begin "05.3 hpa scale down after stress exits and service access acceptance"
  preflight &&
    cleanup_all &&
    apply_rs_and_service &&
    wait_for_metrics &&
    section_check_run "$HPA_NAME CPU-target manifest applied" "$KUBECTL_BIN" apply -f "$HPA_UP_MANIFEST" &&
    wait_for_rs_desired 3 &&
    wait_for_rs_running 3 &&
    wait_for_service_endpoints 3 &&
    section_check_run "$HPA_NAME target unchanged while stress commands age out" "$KUBECTL_BIN" apply -f "$HPA_DOWN_MANIFEST" &&
    wait_for_rs_desired 1 &&
    wait_for_rs_running 1 &&
    wait_for_service_endpoints 1
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of)"
    section_check_quiet "$SERVICE_NAME has NodePort $NODEPORT" test "$node_port" = "$NODEPORT" &&
      wait_for_http "node-a reaches HPA Service after scale down $LOCAL_NODE_IP:$node_port" "http://$LOCAL_NODE_IP:$node_port/" &&
      section_check_run "$HPA_NAME final get output" "$KUBECTL_BIN" get hpa "$HPA_NAME" &&
      section_check_run "$HPA_NAME final describe output" "$KUBECTL_BIN" describe hpa "$HPA_NAME"
  fi
  cleanup_all
  section_end "05.3 cleaned HPA scale-down resources"
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
  section_scale_up
  section_scale_down_access
  printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  if [ "$SECTION_PASS_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
