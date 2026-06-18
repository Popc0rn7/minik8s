#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/04_replicaset.sh
  bash scripts/acceptance/04_replicaset.sh cleanup
  bash scripts/acceptance/04_replicaset.sh --help

Run on node-a after 01_node_multinode.sh has started bridge, all sailers,
mooring CNI, and kube-proxy.

Sections:
  04.1 ReplicaSet create/delete and multinode scheduling
  04.2 ReplicaSet Service binding and NodePort access
  04.3 ReplicaSet recovery after Pod deletion

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
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_WAIT_ATTEMPTS:-36}"
WAIT_SLEEP_SECONDS="${MINIK8S_ACCEPTANCE_WAIT_SLEEP_SECONDS:-2}"
HTTP_TIMEOUT_SECONDS="${MINIK8S_ACCEPTANCE_HTTP_TIMEOUT_SECONDS:-3}"
LOCAL_NODE_IP="${MINIK8S_NODE_A_IP:-192.168.1.4}"
REMOTE_NODE_IP="${MINIK8S_NODE_B_IP:-192.168.1.10}"
NODEPORT="${MINIK8S_ACCEPTANCE_04_NODEPORT:-30081}"

RS_NAME="rs-04-web"
SERVICE_NAME="rs-04-web"
CASE_LABEL="minik8s-acceptance-04"
APP_LABEL="rs-04-web"

SECTION_STATUS="PASS"
SECTION_PASS_COUNT=0
SECTION_TOTAL=3
PREFLIGHT_DONE=0

manifest_dir() {
  if [ -d "$REMOTE_DIR/manifests/replicaset" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/replicaset"
    return 0
  fi
  printf '%s\n' "$ROOT/manifest/replicaset"
}

MANIFEST_DIR="$(manifest_dir)"
RS_MANIFEST="$MANIFEST_DIR/replicaset_04_acceptance.yaml"
SERVICE_MANIFEST="$MANIFEST_DIR/service_04_acceptance.yaml"

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
  if [ "$attempt" -eq 1 ] || [ "$attempt" -eq "$WAIT_ATTEMPTS" ] || [ $((attempt % 5)) -eq 0 ]; then
    output "$message attempt=$attempt/$WAIT_ATTEMPTS"
  fi
}

rs_pods_table() {
  "$KUBECTL_BIN" get pods
}

rs_pod_names() {
  rs_pods_table | awk -v case_label="case=$CASE_LABEL" 'NR > 1 && index($0, case_label) {print $2}'
}

running_rs_pod_names() {
  rs_pods_table | awk -v case_label="case=$CASE_LABEL" 'NR > 1 && index($0, case_label) && index($0, "Running") {print $2}'
}

running_rs_pod_count() {
  running_rs_pod_names | sed '/^$/d' | wc -l | tr -d ' '
}

running_rs_nodes() {
  local pod_name
  for pod_name in $(running_rs_pod_names); do
    "$KUBECTL_BIN" get pod "$pod_name" -o yaml | sed -n 's/^[[:space:]]*nodeName:[[:space:]]*//p' | head -n 1
  done | sed '/^$/d' | sort -u
}

running_rs_node_count() {
  running_rs_nodes | wc -l | tr -d ' '
}

endpoint_count() {
  "$KUBECTL_BIN" get svc "$SERVICE_NAME" -o yaml | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | sed '/^$/d' | wc -l | tr -d ' '
}

node_port_of() {
  "$KUBECTL_BIN" get svc "$SERVICE_NAME" -o yaml | sed -n 's/^[[:space:]]*nodePort:[[:space:]]*//p' | head -n 1 | tr -d '"'
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

wait_for_rs_status() {
  local expected="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if "$KUBECTL_BIN" describe rs "$RS_NAME" 2>/dev/null | grep -Eq "^Desired:[[:space:]]*$expected$" &&
      "$KUBECTL_BIN" describe rs "$RS_NAME" 2>/dev/null | grep -Eq "^Current:[[:space:]]*$expected$"; then
      pass "$RS_NAME status Desired=$expected Current=$expected"
      return 0
    fi
    wait_note "waiting for ReplicaSet status Desired=$expected Current=$expected" "$attempt"
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

wait_for_http() {
  local label="$1"
  local url="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >/tmp/minik8s-acceptance-04-http.out 2>/dev/null; then
      section_check_run "$label" bash -c 'cat /tmp/minik8s-acceptance-04-http.out'
      return 0
    fi
    wait_note "waiting for HTTP access $url" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$label"
  return 1
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

delete_orphan_rs_pods() {
  local pod_name
  for pod_name in $(rs_pod_names); do
    run "$KUBECTL_BIN" delete pod "$pod_name" || true
  done
}

cleanup_runtime() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if bash -lc 'test -z "$(docker ps -aq --filter label=minik8s.kind=pod-container --filter label=case=minik8s-acceptance-04)"' >/dev/null 2>&1; then
      pass "no local acceptance ReplicaSet containers remain"
      break
    fi
    wait_note "waiting for local runtime cleanup" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  run rm -f /tmp/minik8s-acceptance-04-http.out /tmp/minik8s-acceptance-04-backends.out || true
}

cleanup_all() {
  delete_service_if_exists
  delete_rs_if_exists
  delete_orphan_rs_pods
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
  section_check_quiet "$MINIK8S_NODE_A_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_A_NAME" || return 1
  section_check_quiet "$MINIK8S_NODE_B_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_B_NAME" || return 1
  section_check_quiet "$MINIK8S_NODE_C_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_C_NAME" || return 1
  section_check_quiet "CNI is enabled for ReplicaSet acceptance" bash -c 'test "${MINIK8S_CNI_DISABLED:-0}" != "1"' || return 1
  section_check_quiet "bridge netfilter sends Pod traffic through iptables" bash -c 'test "$(sysctl -n net.bridge.bridge-nf-call-iptables 2>/dev/null)" = "1"' || return 1
  section_check_quiet "04 ReplicaSet manifests exist" test -f "$RS_MANIFEST" -a -f "$SERVICE_MANIFEST" || return 1
  PREFLIGHT_DONE=1
}

apply_rs() {
  section_check_run "$RS_NAME manifest applied" "$KUBECTL_BIN" apply -f "$RS_MANIFEST" || return 1
  wait_for_rs_running 3 || return 1
  wait_for_rs_status 3 || return 1
  section_check_run "$RS_NAME summary shows desired/current replicas" "$KUBECTL_BIN" describe rs "$RS_NAME" || return 1
}

apply_service() {
  delete_service_if_exists
  section_check_run "$SERVICE_NAME Service manifest applied" "$KUBECTL_BIN" apply -f "$SERVICE_MANIFEST" || return 1
  wait_for_service_endpoints 3 || return 1
  section_check_run "$SERVICE_NAME Service summary shows selector, NodePort, and endpoints" "$KUBECTL_BIN" describe svc "$SERVICE_NAME" || return 1
}

section_create_delete() {
  section_begin "04.1 replicaset create delete multinode acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR manifests=$MANIFEST_DIR"
  preflight &&
    cleanup_all &&
    apply_rs &&
    section_check_run "$RS_NAME Pods are visible" "$KUBECTL_BIN" get pods &&
    section_check_run "$RS_NAME Pods are scheduled on at least two nodes" bash -c '
      pods="$("$1" get pods | awk -v case_label="case=$2" "NR > 1 && index(\$0, case_label) && index(\$0, \"Running\") {print \$2}")"
      nodes=""
      for pod_name in $pods; do
        node="$("$1" get pod "$pod_name" -o yaml | sed -n "s/^[[:space:]]*nodeName:[[:space:]]*//p" | head -n 1)"
        nodes="${nodes}${node}
"
      done
      nodes="$(printf "%s\n" "$nodes" | sed "/^$/d" | sort -u)"
      printf "%s\n" "$nodes"
      test "$(printf "%s\n" "$nodes" | sed "/^$/d" | wc -l | tr -d " ")" -ge 2
    ' _ "$KUBECTL_BIN" "$CASE_LABEL" &&
    section_check_run "$RS_NAME deleted" "$KUBECTL_BIN" delete rs "$RS_NAME" &&
    wait_for_rs_running 0 &&
    section_check_quiet "$RS_NAME is absent after delete" bash -c '! "$1" get rs "$2"' _ "$KUBECTL_BIN" "$RS_NAME"
  cleanup_all
  section_end "04.1 cleaned ReplicaSet create/delete resources"
}

section_service_binding() {
  section_begin "04.2 replicaset service binding acceptance"
  preflight &&
    cleanup_all &&
    apply_rs &&
    apply_service
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of)"
    section_check_quiet "$SERVICE_NAME has NodePort $NODEPORT" test "$node_port" = "$NODEPORT" &&
      wait_for_http "node-a reaches ReplicaSet Service NodePort $LOCAL_NODE_IP:$node_port" "http://$LOCAL_NODE_IP:$node_port/" &&
      wait_for_http "node-a reaches node-b ReplicaSet Service NodePort $REMOTE_NODE_IP:$node_port" "http://$REMOTE_NODE_IP:$node_port/" &&
      section_check_run "kube-proxy programs random DNAT rules for three ReplicaSet backends" bash -c '
        rules="$(iptables-save | grep -- "--dport $1" | grep "MK8S-SVC\\|to-destination" || true)"
        printf "%s\n" "$rules"
        test "$(printf "%s\n" "$rules" | grep -c -- "--to-destination")" -ge 3
        printf "%s\n" "$rules" | grep -q -- "--mode random"
      ' _ "$NODEPORT" &&
      section_check_run "$SERVICE_NAME endpoints map to three ReplicaSet Pods" "$KUBECTL_BIN" get svc "$SERVICE_NAME" -o yaml
  fi
  cleanup_all
  section_end "04.2 cleaned ReplicaSet Service resources"
}

section_recovery() {
  section_begin "04.3 replicaset recovery acceptance"
  preflight &&
    cleanup_all &&
    apply_rs
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local before deleted after
    before="$(running_rs_pod_names | sort | tr '\n' ' ')"
    deleted="$(running_rs_pod_names | sort | head -n 1)"
    output "before_pods=$before"
    section_check_quiet "selected Pod for deletion exists" test -n "$deleted" &&
      section_check_run "delete one ReplicaSet-owned Pod $deleted" "$KUBECTL_BIN" delete pod "$deleted" &&
      wait_for_rs_running 3 &&
      wait_for_rs_status 3
    if [ "$SECTION_STATUS" = "PASS" ]; then
      after="$(running_rs_pod_names | sort | tr '\n' ' ')"
      output "after_pods=$after"
      section_check_run "$RS_NAME created a replacement Pod" bash -c '
        before="$1"
        after="$2"
        deleted="$3"
        printf "deleted=%s\nbefore=%s\nafter=%s\n" "$deleted" "$before" "$after"
        ! printf "%s\n" "$after" | grep -Fq "$deleted"
      ' _ "$before" "$after" "$deleted"
    fi
  fi
  cleanup_all
  section_end "04.3 cleaned ReplicaSet recovery resources"
}

main() {
  if [ "${1:-}" = "cleanup" ]; then
    begin "04 replicaset acceptance cleanup"
    cleanup_all
    cleanup "04 cleanup completed"
    end
    return 0
  fi

  section_create_delete
  section_service_binding
  section_recovery
  printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  if [ "$SECTION_PASS_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
