#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/07_fault_tolerance.sh
  bash scripts/acceptance/07_fault_tolerance.sh cleanup
  bash scripts/acceptance/07_fault_tolerance.sh --help

Run on node-a. The script verifies bridge restart, then stops the local
minik8s-sailer.service to simulate a node failure, and always tries to restart
both services during cleanup.

Sections:
  07.1 Bridge restart preserves control-plane state and Service access
  07.2 ReplicaSet recovers after local sailer failure
  07.3 Bare Pod is marked NodeLost and Service endpoint is removed
  07.4 Node recovery after sailer restart

Each section cleans its own resources and emits [END] status=<PASS|FAIL|LIMITED>.
The script emits a final [END] status=N/4PASS.
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
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_07_WAIT_ATTEMPTS:-90}"
WAIT_SLEEP_SECONDS="${MINIK8S_ACCEPTANCE_WAIT_SLEEP_SECONDS:-2}"
HTTP_TIMEOUT_SECONDS="${MINIK8S_ACCEPTANCE_HTTP_TIMEOUT_SECONDS:-3}"
LOCAL_NODE_NAME="${MINIK8S_ACCEPTANCE_LOCAL_NODE:-${MINIK8S_NODE_A_NAME:-node-a}}"
LOCAL_NODE_IP="${MINIK8S_NODE_A_IP:-192.168.1.4}"
NODEPORT="${MINIK8S_ACCEPTANCE_07_NODEPORT:-30085}"

RS_NAME="rs-07-web"
RS_SERVICE_NAME="rs-07-web"
BARE_POD_NAME="pod-07-bare"
BARE_SERVICE_NAME="pod-07-bare"
CASE_LABEL="minik8s-acceptance-07"

SECTION_STATUS="PASS"
SECTION_PASS_COUNT=0
SECTION_TOTAL=4
PREFLIGHT_DONE=0

manifest_dir() {
  if [ -d "$REMOTE_DIR/manifests/fault" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/fault"
    return 0
  fi
  printf '%s\n' "$ROOT/manifest/fault"
}

MANIFEST_DIR="$(manifest_dir)"
RS_MANIFEST="$MANIFEST_DIR/replicaset_07_acceptance.yaml"
RS_SERVICE_MANIFEST="$MANIFEST_DIR/service_07_acceptance.yaml"
BARE_POD_MANIFEST="$MANIFEST_DIR/pod_07_bare.yaml"
BARE_SERVICE_MANIFEST="$MANIFEST_DIR/service_07_bare.yaml"

section_begin() {
  SECTION_STATUS="PASS"
  begin "$1"
}

section_fail() {
  SECTION_STATUS="FAIL"
  printf '[FAIL] %s\n' "$*"
}

section_limited() {
  if [ "$SECTION_STATUS" = "PASS" ]; then
    SECTION_STATUS="LIMITED"
  fi
  printf '[LIMITED] %s\n' "$*"
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
  pods_table | awk -v app="app=$RS_NAME" -v case_label="case=$CASE_LABEL" 'NR > 1 && index($0, app) && index($0, case_label) {print $2}'
}

running_rs_pod_names() {
  pods_table | awk -v app="app=$RS_NAME" -v case_label="case=$CASE_LABEL" 'NR > 1 && index($0, app) && index($0, case_label) && index($0, "Running") {print $2}'
}

running_rs_pod_count() {
  running_rs_pod_names | sed '/^$/d' | wc -l | tr -d ' '
}

pod_node() {
  "$KUBECTL_BIN" get pod "$1" -o yaml | sed -n 's/^[[:space:]]*nodeName:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

rs_nodes() {
  local pod_name
  for pod_name in $(running_rs_pod_names); do
    pod_node "$pod_name"
  done | sed '/^$/d' | sort -u
}

rs_has_local_node() {
  rs_nodes | grep -Fxq "$LOCAL_NODE_NAME"
}

service_yaml() {
  "$KUBECTL_BIN" get svc "$1" -o yaml
}

endpoint_count() {
  service_yaml "$1" | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | sed '/^$/d' | wc -l | tr -d ' '
}

endpoint_pod_names() {
  service_yaml "$1" | sed -n 's/^[[:space:]]*podName:[[:space:]]*//p'
}

node_port_of() {
  service_yaml "$RS_SERVICE_NAME" | sed -n 's/^[[:space:]]*nodePort:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

wait_for_node_phase() {
  local node_name="$1"
  local expected="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if "$KUBECTL_BIN" get node "$node_name" -o yaml 2>/dev/null | grep -Eq "phase:[[:space:]]*$expected"; then
      pass "$node_name is $expected"
      return 0
    fi
    wait_note "waiting for node $node_name phase $expected" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$node_name final describe before phase failure" "$KUBECTL_BIN" describe node "$node_name" || true
  section_fail "$node_name did not become $expected"
  return 1
}

wait_for_harbor() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl -fsS -o /dev/null "$MINIK8S_HARBOR/api/v1" 2>/dev/null; then
      pass "Harbor API is reachable"
      return 0
    fi
    wait_note "waiting for Harbor API after bridge start" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "Harbor API did not become reachable"
  return 1
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
    wait_note "waiting for ReplicaSet Running Pods current=$count expected=$expected" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$RS_NAME final Pod list before failure" "$KUBECTL_BIN" get pods || true
  section_fail "$RS_NAME did not reach $expected Running Pods"
  return 1
}

wait_for_service_endpoints() {
  local service_name="$1"
  local expected="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local count
    count="$(endpoint_count "$service_name" 2>/dev/null || printf '0')"
    if [ "$count" -eq "$expected" ]; then
      pass "$service_name endpoint count is $expected"
      return 0
    fi
    wait_note "waiting for Service $service_name endpoints current=$count expected=$expected" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$service_name final describe before endpoint failure" "$KUBECTL_BIN" describe svc "$service_name" || true
  section_fail "$service_name endpoint count did not become $expected"
  return 1
}

wait_for_http() {
  local label="$1"
  local url="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >/tmp/minik8s-acceptance-07-http.out 2>/dev/null; then
      section_check_run "$label" bash -c 'cat /tmp/minik8s-acceptance-07-http.out'
      return 0
    fi
    wait_note "waiting for HTTP access $url" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$label"
  return 1
}

start_sailer() {
  run systemctl start minik8s-sailer.service || true
}

start_bridge() {
  run systemctl start minik8s-bridge.service || true
  wait_for_harbor || true
}

restart_bridge() {
  section_check_run "minik8s-bridge.service restarted" systemctl restart minik8s-bridge.service &&
    wait_for_harbor
}

stop_sailer() {
  section_check_run "local minik8s-sailer.service stopped" systemctl stop minik8s-sailer.service
}

delete_if_exists() {
  local resource="$1"
  local name="$2"
  if "$KUBECTL_BIN" get "$resource" "$name" >/dev/null 2>&1; then
    run "$KUBECTL_BIN" delete "$resource" "$name" || true
  else
    output "$resource $name already absent"
  fi
}

delete_rs_pods() {
  local pod_name
  for pod_name in $(rs_pod_names); do
    run "$KUBECTL_BIN" delete pod "$pod_name" || true
  done
}

cleanup_runtime() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if bash -lc 'test -z "$(docker ps -aq --filter label=minik8s.kind=pod-container --filter label=case=minik8s-acceptance-07)"' >/dev/null 2>&1; then
      pass "no local acceptance fault-tolerance containers remain"
      break
    fi
    wait_note "waiting for local runtime cleanup" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  run rm -f /tmp/minik8s-acceptance-07-http.out || true
}

cleanup_all() {
  start_bridge
  start_sailer
  wait_for_node_phase "$LOCAL_NODE_NAME" Ready || true
  delete_if_exists svc "$BARE_SERVICE_NAME"
  delete_if_exists pod "$BARE_POD_NAME"
  delete_if_exists svc "$RS_SERVICE_NAME"
  delete_if_exists rs "$RS_NAME"
  delete_rs_pods
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
  section_check_run "kubectl can list nodes" "$KUBECTL_BIN" get nodes || return 1
  section_check_quiet "$LOCAL_NODE_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$LOCAL_NODE_NAME" || return 1
  section_check_quiet "minik8s-bridge.service is active before fault injection" systemctl is-active --quiet minik8s-bridge.service || return 1
  section_check_quiet "minik8s-sailer.service is active before fault injection" systemctl is-active --quiet minik8s-sailer.service || return 1
  section_check_quiet "07 fault tolerance manifests exist" test -f "$RS_MANIFEST" -a -f "$RS_SERVICE_MANIFEST" -a -f "$BARE_POD_MANIFEST" -a -f "$BARE_SERVICE_MANIFEST" || return 1
  PREFLIGHT_DONE=1
}

apply_rs_with_local_pod() {
  local attempt=1
  while [ "$attempt" -le 3 ]; do
    delete_if_exists svc "$RS_SERVICE_NAME"
    delete_if_exists rs "$RS_NAME"
    delete_rs_pods
    section_check_run "$RS_NAME manifest applied attempt=$attempt" "$KUBECTL_BIN" apply -f "$RS_MANIFEST" || return 1
    wait_for_rs_running 3 || return 1
    section_check_run "$RS_NAME scheduling distribution attempt=$attempt" bash -c '
      KUBECTL_BIN="$1"
      shift
      for pod_name in "$@"; do
        node="$("$KUBECTL_BIN" get pod "$pod_name" -o yaml | sed -n "s/^[[:space:]]*nodeName:[[:space:]]*//p" | head -n 1)"
        printf "%s node=%s\n" "$pod_name" "$node"
      done
    ' _ "$KUBECTL_BIN" $(running_rs_pod_names) || true
    if rs_has_local_node; then
      pass "$RS_NAME has at least one Pod on $LOCAL_NODE_NAME"
      return 0
    fi
    output "$RS_NAME did not schedule a Pod on $LOCAL_NODE_NAME on attempt=$attempt"
    attempt=$((attempt + 1))
  done
  section_limited "$RS_NAME did not place a Pod on $LOCAL_NODE_NAME after 3 attempts; cannot prove local node failure recovery"
  return 1
}

section_bridge_restart() {
  section_begin "07.1 bridge restart preserves control-plane state and service access"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR manifests=$MANIFEST_DIR local_node=$LOCAL_NODE_NAME"
  preflight &&
    cleanup_all &&
    section_check_run "$RS_NAME manifest applied before bridge restart" "$KUBECTL_BIN" apply -f "$RS_MANIFEST" &&
    wait_for_rs_running 3 &&
    section_check_run "$RS_SERVICE_NAME Service manifest applied before bridge restart" "$KUBECTL_BIN" apply -f "$RS_SERVICE_MANIFEST" &&
    wait_for_service_endpoints "$RS_SERVICE_NAME" 3
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of)"
    section_check_quiet "$RS_SERVICE_NAME keeps NodePort $NODEPORT before bridge restart" test "$node_port" = "$NODEPORT" &&
      wait_for_http "node-a reaches Service before bridge restart $LOCAL_NODE_IP:$node_port" "http://$LOCAL_NODE_IP:$node_port/"
  fi
  if [ "$SECTION_STATUS" = "PASS" ]; then
    restart_bridge &&
      section_check_run "nodes are listable after bridge restart" "$KUBECTL_BIN" get nodes &&
      section_check_run "$RS_NAME Pods are listable after bridge restart" "$KUBECTL_BIN" get pods &&
      wait_for_rs_running 3 &&
      wait_for_service_endpoints "$RS_SERVICE_NAME" 3 &&
      section_check_run "$RS_SERVICE_NAME endpoints after bridge restart" "$KUBECTL_BIN" describe svc "$RS_SERVICE_NAME"
  fi
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of)"
    wait_for_http "node-a reaches Service after bridge restart $LOCAL_NODE_IP:$node_port" "http://$LOCAL_NODE_IP:$node_port/"
  fi
  cleanup_all
  section_end "07.1 cleaned bridge restart resources"
}

section_replicaset_recovery() {
  section_begin "07.2 replicaset recovers after local sailer failure"
  preflight &&
    cleanup_all &&
    apply_rs_with_local_pod &&
    section_check_run "$RS_SERVICE_NAME Service manifest applied" "$KUBECTL_BIN" apply -f "$RS_SERVICE_MANIFEST" &&
    wait_for_service_endpoints "$RS_SERVICE_NAME" 3 &&
    stop_sailer &&
    wait_for_node_phase "$LOCAL_NODE_NAME" Unknown &&
    wait_for_rs_running 3 &&
    section_check_run "$RS_NAME Pods after local node loss" "$KUBECTL_BIN" get pods &&
    section_check_run "$RS_NAME no Running Pods remain scheduled on $LOCAL_NODE_NAME" bash -c '
      KUBECTL_BIN="$1"
      local_node="$2"
      shift 2
      for pod_name in "$@"; do
        node="$("$KUBECTL_BIN" get pod "$pod_name" -o yaml | sed -n "s/^[[:space:]]*nodeName:[[:space:]]*//p" | head -n 1)"
        printf "%s node=%s\n" "$pod_name" "$node"
        test "$node" != "$local_node"
      done
    ' _ "$KUBECTL_BIN" "$LOCAL_NODE_NAME" $(running_rs_pod_names) &&
    wait_for_service_endpoints "$RS_SERVICE_NAME" 3 &&
    section_check_run "$RS_SERVICE_NAME endpoints after local node loss" "$KUBECTL_BIN" describe svc "$RS_SERVICE_NAME"
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of)"
    section_check_quiet "$RS_SERVICE_NAME keeps NodePort $NODEPORT" test "$node_port" = "$NODEPORT"
  fi
  cleanup_all
  section_end "07.2 cleaned ReplicaSet fault resources and restarted sailer"
}

section_bare_pod_nodelost() {
  section_begin "07.3 bare pod is marked NodeLost and service endpoint is removed"
  preflight &&
    cleanup_all &&
    section_check_run "$BARE_POD_NAME manifest applied" "$KUBECTL_BIN" apply -f "$BARE_POD_MANIFEST" &&
    section_check_quiet "$BARE_POD_NAME is scheduled on $LOCAL_NODE_NAME" bash -c '"$1" get pod "$2" -o yaml | grep -Eq "nodeName:[[:space:]]*$3"' _ "$KUBECTL_BIN" "$BARE_POD_NAME" "$LOCAL_NODE_NAME" &&
    section_check_run "$BARE_SERVICE_NAME Service manifest applied" "$KUBECTL_BIN" apply -f "$BARE_SERVICE_MANIFEST" &&
    wait_for_service_endpoints "$BARE_SERVICE_NAME" 1 &&
    stop_sailer &&
    wait_for_node_phase "$LOCAL_NODE_NAME" Unknown &&
    section_check_run "$BARE_POD_NAME is marked Unknown/NodeLost" bash -c '
      "$1" describe pod "$2"
      "$1" describe pod "$2" | grep -Eq "^Status:[[:space:]]*Unknown"
      "$1" describe pod "$2" | grep -Eq "^Reason:[[:space:]]*NodeLost"
    ' _ "$KUBECTL_BIN" "$BARE_POD_NAME" &&
    wait_for_service_endpoints "$BARE_SERVICE_NAME" 0 &&
    section_check_run "$BARE_SERVICE_NAME endpoints no longer contain $BARE_POD_NAME" bash -c '
      "$1" get svc "$2" -o yaml
      ! "$1" get svc "$2" -o yaml | sed -n "s/^[[:space:]]*podName:[[:space:]]*//p" | grep -Fx "$3"
    ' _ "$KUBECTL_BIN" "$BARE_SERVICE_NAME" "$BARE_POD_NAME"
  cleanup_all
  section_end "07.3 cleaned bare Pod fault resources and restarted sailer"
}

section_node_recovery() {
  section_begin "07.4 node recovery after sailer restart"
  preflight &&
    cleanup_all &&
    stop_sailer &&
    wait_for_node_phase "$LOCAL_NODE_NAME" Unknown &&
    section_check_run "local minik8s-sailer.service restarted" systemctl start minik8s-sailer.service &&
    wait_for_node_phase "$LOCAL_NODE_NAME" Ready &&
    section_check_run "node list after local sailer recovery" "$KUBECTL_BIN" get nodes
  cleanup_all
  section_end "07.4 cleaned node recovery resources and kept sailer running"
}

main() {
  if [ "${1:-}" = "cleanup" ]; then
    begin "07 fault tolerance acceptance cleanup"
    cleanup_all
    cleanup "07 cleanup completed"
    end
    return 0
  fi

  section_bridge_restart
  section_replicaset_recovery
  section_bare_pod_nodelost
  section_node_recovery
  printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  if [ "$SECTION_PASS_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
