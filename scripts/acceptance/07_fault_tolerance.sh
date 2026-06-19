#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/07_fault_tolerance.sh
  bash scripts/acceptance/07_fault_tolerance.sh cleanup
  bash scripts/acceptance/07_fault_tolerance.sh --help

Run on node-a after 01_node_multinode.sh has started bridge, all sailers,
mooring CNI, and kube-proxy. This script uses fixed manifests under
manifests/fault/ in the deployed tree or manifests/fault/ in the source tree.
It restarts bridge and stops the local minik8s-sailer.service for fault
injection, then tries to restart both services during cleanup.

Sections:
  07.1 Bridge restart, state recovery, and Service access
  07.2 ReplicaSet recovery after local sailer failure
  07.3 Bare Pod NodeLost and endpoint removal
  07.4 Local sailer restart and node Ready recovery

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
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_07_WAIT_ATTEMPTS:-${MINIK8S_ACCEPTANCE_WAIT_ATTEMPTS:-90}}"
WAIT_SLEEP_SECONDS="${MINIK8S_ACCEPTANCE_WAIT_SLEEP_SECONDS:-2}"
HTTP_TIMEOUT_SECONDS="${MINIK8S_ACCEPTANCE_HTTP_TIMEOUT_SECONDS:-3}"
LOCAL_NODE_NAME="${MINIK8S_ACCEPTANCE_LOCAL_NODE:-${MINIK8S_NODE_A_NAME:-node-a}}"
LOCAL_NODE_IP="${MINIK8S_NODE_A_IP:-192.168.1.4}"
SURVIVOR_NODE_NAME="${MINIK8S_ACCEPTANCE_07_SURVIVOR_NODE:-${MINIK8S_NODE_B_NAME:-node-b}}"
SURVIVOR_NODE_IP="${MINIK8S_ACCEPTANCE_07_SURVIVOR_IP:-${MINIK8S_NODE_B_IP:-192.168.1.10}}"
NODEPORT="${MINIK8S_ACCEPTANCE_07_NODEPORT:-30085}"

RS_NAME="rs-07-web"
RS_SERVICE_NAME="rs-07-web"
BARE_POD_NAME="pod-07-bare"
BARE_SERVICE_NAME="pod-07-bare"
LOCAL_PENDING_POD_NAME="pod-07-local-pending"
CASE_LABEL="minik8s-acceptance-07"

SECTION_STATUS="PASS"
SECTION_PASS_COUNT=0
SECTION_ACCEPT_COUNT=0
SECTION_LIMITED_COUNT=0
SECTION_TOTAL=4
PREFLIGHT_DONE=0

manifest_dir() {
  if [ -d "$REMOTE_DIR/manifests/fault" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/fault"
    return 0
  fi
  printf '%s\n' "$ROOT/manifests/fault"
}

MANIFEST_DIR="$(manifest_dir)"
RS_MANIFEST="$MANIFEST_DIR/replicaset_07_acceptance.yaml"
RS_SERVICE_MANIFEST="$MANIFEST_DIR/service_07_acceptance.yaml"
BARE_POD_MANIFEST="$MANIFEST_DIR/pod_07_bare.yaml"
BARE_SERVICE_MANIFEST="$MANIFEST_DIR/service_07_bare.yaml"
LOCAL_PENDING_POD_MANIFEST="$MANIFEST_DIR/pod_07_local_pending.yaml"

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

section_end() {
  cleanup "$1"
  printf '[END] status=%s\n' "$SECTION_STATUS"
  if [ "$SECTION_STATUS" = "PASS" ]; then
    SECTION_PASS_COUNT=$((SECTION_PASS_COUNT + 1))
  fi
  if [ "$SECTION_STATUS" = "PASS" ] || [ "$SECTION_STATUS" = "LIMITED" ]; then
    SECTION_ACCEPT_COUNT=$((SECTION_ACCEPT_COUNT + 1))
  fi
  if [ "$SECTION_STATUS" = "LIMITED" ]; then
    SECTION_LIMITED_COUNT=$((SECTION_LIMITED_COUNT + 1))
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

service_yaml() {
  "$KUBECTL_BIN" get svc "$1" -o yaml
}

node_yaml() {
  "$KUBECTL_BIN" get node "$1" -o yaml
}

rs_pod_names() {
  pods_table | awk -v app="app=$RS_NAME" -v case_label="case=$CASE_LABEL" '
    NR > 1 && index($0, app) && index($0, case_label) {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^rs-07-web/) {
          print $i
          break
        }
      }
    }
  '
}

running_rs_pod_names() {
  pods_table | awk -v app="app=$RS_NAME" -v case_label="case=$CASE_LABEL" '
    NR > 1 && index($0, app) && index($0, case_label) && index($0, "Running") {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^rs-07-web/) {
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

pod_phase() {
  "$KUBECTL_BIN" get pod "$1" -o yaml | sed -n 's/^[[:space:]]*phase:[[:space:]]*//p' | head -n 1 | tr -d '"'
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

endpoint_count() {
  service_yaml "$1" | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | sed '/^$/d' | wc -l | tr -d ' '
}

endpoint_pod_names() {
  service_yaml "$1" | sed -n 's/^[[:space:]]*podName:[[:space:]]*//p' | tr -d '"'
}

node_port_of() {
  service_yaml "$RS_SERVICE_NAME" | sed -n 's/^[[:space:]]*nodePort:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

manifest_summary() {
  sed -n '
    s/^kind:/kind:/p
    s/^apiVersion:/apiVersion:/p
    s/^[[:space:]]*name:[[:space:]]*/name: /p
    s/^[[:space:]]*replicas:[[:space:]]*/replicas: /p
    s/^[[:space:]]*type:[[:space:]]*/type: /p
    s/^[[:space:]]*app:[[:space:]]*/selector.app: /p
    s/^[[:space:]]*case:[[:space:]]*/selector.case: /p
    s/^[[:space:]]*node:[[:space:]]*/nodeSelector.node: /p
    s/^[[:space:]]*image:[[:space:]]*/image: /p
    s/^[[:space:]]*imageTag:[[:space:]]*/imageTag: /p
    s/^[[:space:]]*port:[[:space:]]*/port: /p
    s/^[[:space:]]*targetPort:[[:space:]]*/targetPort: /p
    s/^[[:space:]]*nodePort:[[:space:]]*/nodePort: /p
  ' "$1"
}

rs_pod_summary() {
  local pod_name node ip
  for pod_name in $(running_rs_pod_names | sort); do
    node="$(pod_node "$pod_name")"
    ip="$(pod_ip "$pod_name")"
    printf 'pod=%s node=%s podIP=%s labels=app=%s,case=%s\n' "$pod_name" "$node" "$ip" "$RS_NAME" "$CASE_LABEL"
  done
}

service_summary() {
  local svc_name="$1"
  local yaml type cluster_ip node_port port target_port endpoints pod_names
  yaml="$(service_yaml "$svc_name")"
  type="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*type:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  cluster_ip="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*clusterIP:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  node_port="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*nodePort:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  port="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*port:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  target_port="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*targetPort:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  endpoints="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | tr -d '"' | paste -sd ',' -)"
  pod_names="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*podName:[[:space:]]*//p' | tr -d '"' | paste -sd ',' -)"
  printf 'service=%s type=%s clusterIP=%s port=%s targetPort=%s nodePort=%s endpoints=%s endpointPods=%s endpointCount=%s\n' \
    "$svc_name" "$type" "$cluster_ip" "$port" "$target_port" "${node_port:-<none>}" "${endpoints:-<none>}" "${pod_names:-<none>}" "$(endpoint_count "$svc_name")"
}

node_summary() {
  local node_name="$1"
  local yaml phase ip pod_cidr
  yaml="$(node_yaml "$node_name")"
  phase="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*phase:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  ip="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*address:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  pod_cidr="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*podCIDR:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  printf 'node=%s phase=%s address=%s podCIDR=%s\n' "$node_name" "$phase" "${ip:-<unknown>}" "${pod_cidr:-<unknown>}"
}

wait_for_node_phase() {
  local node_name="$1"
  local expected="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if node_yaml "$node_name" 2>/dev/null | grep -Eq "phase:[[:space:]]*$expected"; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$node_name final describe before phase failure" "$KUBECTL_BIN" describe node "$node_name" || true
  section_fail "$node_name did not become $expected"
  return 1
}

wait_for_harbor() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl -fsS -o /dev/null "$MINIK8S_HARBOR/api/v1" 2>/dev/null; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "Harbor API did not become reachable"
  return 1
}

wait_for_harbor_quiet() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl -fsS -o /dev/null "$MINIK8S_HARBOR/api/v1" 2>/dev/null; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  return 1
}

wait_for_node_phase_quiet() {
  local node_name="$1"
  local expected="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if node_yaml "$node_name" 2>/dev/null | grep -Eq "phase:[[:space:]]*$expected"; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  return 1
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
  evidence_run "$RS_NAME final Pod list before failure" "$KUBECTL_BIN" get pods || true
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
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$service_name final describe before endpoint failure" "$KUBECTL_BIN" describe svc "$service_name" || true
  section_fail "$service_name endpoint count did not become $expected"
  return 1
}

wait_for_http() {
  local label="$1"
  local url="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >/tmp/minik8s-acceptance-07-http.out 2>/dev/null; then
      evidence_run "$label" bash -c 'cat /tmp/minik8s-acceptance-07-http.out'
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$label"
  return 1
}

start_sailer_quiet() {
  systemctl start minik8s-sailer.service >/dev/null 2>&1 || true
}

start_bridge_quiet() {
  systemctl start minik8s-bridge.service >/dev/null 2>&1 || true
  wait_for_harbor_quiet >/dev/null 2>&1 || true
}

restart_bridge() {
  evidence_run "minik8s-bridge.service restarted" systemctl restart minik8s-bridge.service &&
    wait_for_harbor
}

stop_sailer() {
  evidence_run "local minik8s-sailer.service stopped" systemctl stop minik8s-sailer.service
}

delete_if_exists() {
  local resource="$1"
  local name="$2"
  if "$KUBECTL_BIN" get "$resource" "$name" >/dev/null 2>&1; then
    quiet_run "delete $resource $name" "$KUBECTL_BIN" delete "$resource" "$name" || true
  fi
}

delete_rs_pods() {
  local pod_name
  for pod_name in $(rs_pod_names 2>/dev/null || true); do
    quiet_run "delete orphan pod $pod_name" "$KUBECTL_BIN" delete pod "$pod_name" || true
  done
}

cleanup_runtime() {
  rm -f /tmp/minik8s-acceptance-07-http.out 2>/dev/null || true
}

cleanup_all() {
  start_bridge_quiet
  start_sailer_quiet
  wait_for_node_phase_quiet "$LOCAL_NODE_NAME" Ready >/dev/null 2>&1 || true
  delete_if_exists pod "$LOCAL_PENDING_POD_NAME"
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
    return 0
  fi
  quiet_check "kubectl binary exists" test -x "$KUBECTL_BIN" || return 1
  evidence_run "Harbor API is reachable" curl -fsS -o /dev/null -w 'http=%{http_code}\n' "$MINIK8S_HARBOR/api/v1" || return 1
  evidence_run "kubectl can list nodes" "$KUBECTL_BIN" get nodes || return 1
  quiet_check "$LOCAL_NODE_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$LOCAL_NODE_NAME" || return 1
  quiet_check "minik8s-bridge.service is active before fault injection" systemctl is-active --quiet minik8s-bridge.service || return 1
  quiet_check "minik8s-sailer.service is active before fault injection" systemctl is-active --quiet minik8s-sailer.service || return 1
  quiet_check "07 fault tolerance manifests exist" test -f "$RS_MANIFEST" -a -f "$RS_SERVICE_MANIFEST" -a -f "$BARE_POD_MANIFEST" -a -f "$BARE_SERVICE_MANIFEST" -a -f "$LOCAL_PENDING_POD_MANIFEST" || return 1
  PREFLIGHT_DONE=1
}

show_fault_manifests() {
  evidence_run "ReplicaSet fault manifest summary" manifest_summary "$RS_MANIFEST" &&
    evidence_run "ReplicaSet Service fault manifest summary" manifest_summary "$RS_SERVICE_MANIFEST" &&
    evidence_run "bare Pod fault manifest summary" manifest_summary "$BARE_POD_MANIFEST" &&
    evidence_run "bare Pod Service fault manifest summary" manifest_summary "$BARE_SERVICE_MANIFEST"
}

apply_rs() {
  quiet_run "$RS_NAME manifest applied" "$KUBECTL_BIN" apply -f "$RS_MANIFEST" || return 1
  wait_for_rs_running 3 || return 1
}

apply_rs_service() {
  quiet_run "$RS_SERVICE_NAME Service manifest applied" "$KUBECTL_BIN" apply -f "$RS_SERVICE_MANIFEST" || return 1
  wait_for_service_endpoints "$RS_SERVICE_NAME" 3 || return 1
}

show_rs_evidence() {
  evidence_run "$RS_NAME running Pod summary" rs_pod_summary &&
    evidence_run "$RS_SERVICE_NAME Service summary" service_summary "$RS_SERVICE_NAME"
}

apply_rs_with_local_pod() {
  local attempt=1
  while [ "$attempt" -le 3 ]; do
    delete_if_exists svc "$RS_SERVICE_NAME"
    delete_if_exists rs "$RS_NAME"
    delete_rs_pods
    quiet_run "$RS_NAME manifest applied attempt=$attempt" "$KUBECTL_BIN" apply -f "$RS_MANIFEST" || return 1
    wait_for_rs_running 3 || return 1
    evidence_run "$RS_NAME scheduling distribution attempt=$attempt" rs_pod_summary || true
    if rs_has_local_node; then
      pass "$RS_NAME has at least one Pod on $LOCAL_NODE_NAME"
      return 0
    fi
    attempt=$((attempt + 1))
  done
  section_limited "$RS_NAME did not place a Pod on $LOCAL_NODE_NAME after 3 attempts; cannot prove local node failure recovery"
  return 1
}

verify_no_running_rs_pods_on_local_node() {
  local pod_name node failed=0
  for pod_name in $(running_rs_pod_names); do
    node="$(pod_node "$pod_name")"
    printf '%s node=%s\n' "$pod_name" "$node"
    if [ "$node" = "$LOCAL_NODE_NAME" ]; then
      failed=1
    fi
  done
  test "$failed" -eq 0
}

verify_endpoint_pods_do_not_include() {
  local old_pods="$1"
  local endpoint_pod
  endpoint_pod_names "$RS_SERVICE_NAME"
  for endpoint_pod in $(endpoint_pod_names "$RS_SERVICE_NAME"); do
    if printf '%s\n' "$old_pods" | grep -Fxq "$endpoint_pod"; then
      return 1
    fi
  done
}

apply_local_pending_probe_pod() {
  "$KUBECTL_BIN" apply -f "$LOCAL_PENDING_POD_MANIFEST"
}

verify_local_pending_probe_unscheduled() {
  "$KUBECTL_BIN" describe pod "$LOCAL_PENDING_POD_NAME"
  local yaml phase node_name
  yaml="$("$KUBECTL_BIN" get pod "$LOCAL_PENDING_POD_NAME" -o yaml)"
  phase="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*phase:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  node_name="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*nodeName:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  printf 'probePod=%s phase=%s nodeName=%s expectedNodeSelector=%s\n' "$LOCAL_PENDING_POD_NAME" "${phase:-<none>}" "${node_name:-<none>}" "$LOCAL_NODE_NAME"
  test "$phase" = "Pending"
  test -z "$node_name"
}

wait_for_local_pending_probe_running_on_local_node() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local phase node_name
    phase="$(pod_phase "$LOCAL_PENDING_POD_NAME" 2>/dev/null || true)"
    node_name="$(pod_node "$LOCAL_PENDING_POD_NAME" 2>/dev/null || true)"
    if [ "$phase" = "Running" ] && [ "$node_name" = "$LOCAL_NODE_NAME" ]; then
      "$KUBECTL_BIN" describe pod "$LOCAL_PENDING_POD_NAME"
      printf 'probePod=%s phase=%s nodeName=%s\n' "$LOCAL_PENDING_POD_NAME" "$phase" "$node_name"
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  "$KUBECTL_BIN" describe pod "$LOCAL_PENDING_POD_NAME"
  return 1
}

delete_one_nonlocal_rs_pod() {
  local pod_name node
  for pod_name in $(running_rs_pod_names | sort); do
    node="$(pod_node "$pod_name")"
    if [ "$node" != "$LOCAL_NODE_NAME" ]; then
      "$KUBECTL_BIN" delete pod "$pod_name"
      printf 'deletedPod=%s deletedPodNode=%s\n' "$pod_name" "$node"
      return 0
    fi
  done
  printf 'no non-%s ReplicaSet Pod found to delete\n' "$LOCAL_NODE_NAME"
  return 1
}

wait_for_rs_back_on_local_node() {
  local attempt=1
  while [ "$attempt" -le 6 ]; do
    if rs_has_local_node; then
      evidence_run "$RS_NAME has a Running Pod on recovered $LOCAL_NODE_NAME" rs_pod_summary
      return 0
    fi
    evidence_run "$RS_NAME replacement trigger attempt=$attempt after $LOCAL_NODE_NAME recovery" delete_one_nonlocal_rs_pod || true
    wait_for_rs_running 3 || return 1
    evidence_run "$RS_NAME scheduling distribution after replacement attempt=$attempt" rs_pod_summary || true
    attempt=$((attempt + 1))
  done
  section_fail "$RS_NAME did not schedule a replacement Pod back to recovered $LOCAL_NODE_NAME"
  return 1
}

section_bridge_restart() {
  section_begin "07.1 bridge restart state recovery service access acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR manifests=$MANIFEST_DIR local_node=$LOCAL_NODE_NAME"
  preflight &&
    cleanup_all &&
    show_fault_manifests &&
    apply_rs &&
    apply_rs_service &&
    show_rs_evidence
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of)"
    quiet_check "$RS_SERVICE_NAME keeps NodePort $NODEPORT before bridge restart" test "$node_port" = "$NODEPORT" &&
      wait_for_http "node-a reaches Service before bridge restart $LOCAL_NODE_IP:$node_port" "http://$LOCAL_NODE_IP:$node_port/"
  fi
  if [ "$SECTION_STATUS" = "PASS" ]; then
    restart_bridge &&
      evidence_run "nodes are listable after bridge restart" "$KUBECTL_BIN" get nodes &&
      wait_for_rs_running 3 &&
      wait_for_service_endpoints "$RS_SERVICE_NAME" 3 &&
      evidence_run "$RS_NAME Pods after bridge restart" rs_pod_summary &&
      evidence_run "$RS_SERVICE_NAME Service after bridge restart" service_summary "$RS_SERVICE_NAME"
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
  section_begin "07.2 replicaset recovery after local sailer failure acceptance"
  local local_pods_before=""
  preflight &&
    cleanup_all &&
    apply_rs_with_local_pod &&
    apply_rs_service
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local_pods_before="$(rs_pod_summary | awk -v node="$LOCAL_NODE_NAME" '$0 ~ "node=" node { sub(/^pod=/, "", $1); print $1 }')"
    evidence_run "$RS_NAME local Pods before node loss" printf '%s\n' "$local_pods_before" &&
      stop_sailer &&
      wait_for_node_phase "$LOCAL_NODE_NAME" Unknown &&
      evidence_run "$LOCAL_NODE_NAME is Unknown after sailer stop" node_summary "$LOCAL_NODE_NAME" &&
      evidence_run "$LOCAL_NODE_NAME describe after sailer stop" "$KUBECTL_BIN" describe node "$LOCAL_NODE_NAME" &&
      wait_for_rs_running 3 &&
      evidence_run "$RS_NAME no Running Pods remain scheduled on $LOCAL_NODE_NAME" verify_no_running_rs_pods_on_local_node &&
      wait_for_service_endpoints "$RS_SERVICE_NAME" 3 &&
      evidence_run "$RS_SERVICE_NAME endpoints after local node loss" service_summary "$RS_SERVICE_NAME" &&
      evidence_run "$RS_SERVICE_NAME endpoint pods exclude lost node-a Pods" verify_endpoint_pods_do_not_include "$local_pods_before" &&
      evidence_run "$LOCAL_PENDING_POD_NAME Pod with nodeSelector=$LOCAL_NODE_NAME applied while node is Unknown" apply_local_pending_probe_pod &&
      evidence_run "$LOCAL_PENDING_POD_NAME remains Pending and unscheduled while $LOCAL_NODE_NAME is Unknown" verify_local_pending_probe_unscheduled
  fi
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of)"
    quiet_check "$RS_SERVICE_NAME keeps NodePort $NODEPORT" test "$node_port" = "$NODEPORT" &&
      quiet_check "$SURVIVOR_NODE_NAME is Ready after $LOCAL_NODE_NAME loss" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$SURVIVOR_NODE_NAME" &&
      wait_for_http "$SURVIVOR_NODE_NAME NodePort reaches Service after $LOCAL_NODE_NAME loss through remaining endpoints $SURVIVOR_NODE_IP:$node_port" "http://$SURVIVOR_NODE_IP:$node_port/"
  fi
  if [ "$SECTION_STATUS" = "PASS" ]; then
    evidence_run "local minik8s-sailer.service restarted for $LOCAL_PENDING_POD_NAME recovery" systemctl start minik8s-sailer.service &&
      wait_for_node_phase "$LOCAL_NODE_NAME" Ready &&
      evidence_run "$LOCAL_PENDING_POD_NAME schedules and runs on recovered $LOCAL_NODE_NAME" wait_for_local_pending_probe_running_on_local_node
  fi
  cleanup_all
  section_end "07.2 cleaned ReplicaSet fault resources and restarted sailer"
}

section_bare_pod_nodelost() {
  section_begin "07.3 bare pod nodelost endpoint removal acceptance"
  preflight &&
    cleanup_all &&
    quiet_run "$BARE_POD_NAME manifest applied" "$KUBECTL_BIN" apply -f "$BARE_POD_MANIFEST" &&
    quiet_check "$BARE_POD_NAME is scheduled on $LOCAL_NODE_NAME" bash -c '"$1" get pod "$2" -o yaml | grep -Eq "nodeName:[[:space:]]*$3"' _ "$KUBECTL_BIN" "$BARE_POD_NAME" "$LOCAL_NODE_NAME" &&
    quiet_run "$BARE_SERVICE_NAME Service manifest applied" "$KUBECTL_BIN" apply -f "$BARE_SERVICE_MANIFEST" &&
    wait_for_service_endpoints "$BARE_SERVICE_NAME" 1 &&
    evidence_run "$BARE_SERVICE_NAME endpoints before node loss" service_summary "$BARE_SERVICE_NAME" &&
    stop_sailer &&
    wait_for_node_phase "$LOCAL_NODE_NAME" Unknown &&
    evidence_run "$BARE_POD_NAME is marked Unknown/NodeLost" bash -c '
      "$1" describe pod "$2"
      "$1" describe pod "$2" | grep -Eq "^Status:[[:space:]]*Unknown"
      "$1" describe pod "$2" | grep -Eq "^Reason:[[:space:]]*NodeLost"
    ' _ "$KUBECTL_BIN" "$BARE_POD_NAME" &&
    wait_for_service_endpoints "$BARE_SERVICE_NAME" 0 &&
    evidence_run "$BARE_SERVICE_NAME endpoint removed and no replacement Pod exists" bash -c '
      "$1" get svc "$2" -o yaml
      ! "$1" get svc "$2" -o yaml | sed -n "s/^[[:space:]]*podName:[[:space:]]*//p" | grep -Fx "$3"
      count="$("$1" get pods | awk -v name="$3" "NR > 1 && \$2 == name { c++ } END { print c + 0 }")"
      printf "barePodCount=%s\n" "$count"
      test "$count" -eq 1
    ' _ "$KUBECTL_BIN" "$BARE_SERVICE_NAME" "$BARE_POD_NAME"
  cleanup_all
  section_end "07.3 cleaned bare Pod fault resources and restarted sailer"
}

section_node_recovery() {
  section_begin "07.4 local sailer restart node ready recovery acceptance"
  preflight &&
    cleanup_all &&
    apply_rs_with_local_pod &&
    apply_rs_service &&
    evidence_run "$RS_NAME Pods before local node recovery fault" rs_pod_summary &&
    stop_sailer &&
    wait_for_node_phase "$LOCAL_NODE_NAME" Unknown &&
    evidence_run "$LOCAL_NODE_NAME is Unknown before restart" node_summary "$LOCAL_NODE_NAME" &&
    wait_for_rs_running 3 &&
    evidence_run "$RS_NAME migrated away from $LOCAL_NODE_NAME before recovery" verify_no_running_rs_pods_on_local_node &&
    evidence_run "local minik8s-sailer.service started" systemctl start minik8s-sailer.service &&
    wait_for_node_phase "$LOCAL_NODE_NAME" Ready &&
    evidence_run "node list after local sailer recovery" "$KUBECTL_BIN" get nodes &&
    evidence_run "$LOCAL_NODE_NAME summary after local sailer recovery" node_summary "$LOCAL_NODE_NAME" &&
    wait_for_rs_back_on_local_node &&
    wait_for_service_endpoints "$RS_SERVICE_NAME" 3 &&
    evidence_run "$RS_SERVICE_NAME endpoints after $LOCAL_NODE_NAME rejoined scheduling pool" service_summary "$RS_SERVICE_NAME"
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
  if [ "$SECTION_LIMITED_COUNT" -gt 0 ]; then
    printf '[END] status=%s/%sACCEPTED limited=%s\n' "$SECTION_ACCEPT_COUNT" "$SECTION_TOTAL" "$SECTION_LIMITED_COUNT"
  else
    printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  fi
  if [ "$SECTION_ACCEPT_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
