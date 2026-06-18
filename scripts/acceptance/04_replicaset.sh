#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/04_replicaset.sh
  bash scripts/acceptance/04_replicaset.sh cleanup
  bash scripts/acceptance/04_replicaset.sh --help

Run on node-a after 01_node_multinode.sh has started bridge, all sailers,
mooring CNI, and kube-proxy. This script uses fixed manifests under
manifests/replicaset/ in the deployed tree or manifest/replicaset/ in the
source tree.

Sections:
  04.1 ReplicaSet create/delete, info, Pods, and multi-node scheduling
  04.2 ReplicaSet Service binding, NodePort access, and random balancing
  04.3 ReplicaSet recovery after Pod deletion

Preparatory checks and cleanup are quiet unless they fail. Evidence commands
print [RUN], [EXIT], [OUTPUT], and a conclusion for TA-readable logs.
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
SECTION_ACCEPT_COUNT=0
SECTION_LIMITED_COUNT=0
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

replicaset_yaml() {
  "$KUBECTL_BIN" get rs "$RS_NAME" -o yaml
}

service_yaml() {
  "$KUBECTL_BIN" get svc "$SERVICE_NAME" -o yaml
}

rs_manifest_summary() {
  sed -n '
    s/^kind:/kind:/p
    s/^apiVersion:/apiVersion:/p
    s/^[[:space:]]*name:[[:space:]]*/name: /p
    s/^[[:space:]]*replicas:[[:space:]]*/replicas: /p
    s/^[[:space:]]*app:[[:space:]]*/selector.app: /p
    s/^[[:space:]]*case:[[:space:]]*/selector.case: /p
    s/^[[:space:]]*image:[[:space:]]*/image: /p
    s/^[[:space:]]*imageTag:[[:space:]]*/imageTag: /p
    s/^[[:space:]]*containerPort:[[:space:]]*/containerPort: /p
    s/^[[:space:]]*cpu:[[:space:]]*/cpu: /p
    s/^[[:space:]]*memory:[[:space:]]*/memory: /p
    s/^[[:space:]]*restartPolicy:[[:space:]]*/restartPolicy: /p
  ' "$1"
}

service_manifest_summary() {
  sed -n '
    s/^kind:/kind:/p
    s/^apiVersion:/apiVersion:/p
    s/^[[:space:]]*name:[[:space:]]*/name: /p
    s/^[[:space:]]*type:[[:space:]]*/type: /p
    s/^[[:space:]]*app:[[:space:]]*/selector.app: /p
    s/^[[:space:]]*case:[[:space:]]*/selector.case: /p
    s/^[[:space:]]*port:[[:space:]]*/port: /p
    s/^[[:space:]]*targetPort:[[:space:]]*/targetPort: /p
    s/^[[:space:]]*nodePort:[[:space:]]*/nodePort: /p
  ' "$1"
}

rs_summary() {
  local yaml desired current ready
  yaml="$(replicaset_yaml)"
  desired="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*replicas:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  current="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*currentReplicas:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  ready="$(running_rs_pod_count)"
  printf 'replicaset=%s desired=%s current=%s runningPods=%s selector=app=%s,case=%s\n' \
    "$RS_NAME" "$desired" "${current:-<unknown>}" "$ready" "$APP_LABEL" "$CASE_LABEL"
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
    "$SERVICE_NAME" "$type" "$APP_LABEL" "$CASE_LABEL" "$cluster_ip" "$port" "$target_port" "${node_port:-<none>}" "${endpoints:-<none>}" "$(endpoint_count)"
}

rs_pods_table() {
  "$KUBECTL_BIN" get pods
}

rs_pod_names() {
  rs_pods_table | awk -v case_label="case=$CASE_LABEL" '
    NR > 1 && index($0, case_label) {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^rs-04-web/) {
          print $i
          break
        }
      }
    }
  '
}

running_rs_pod_names() {
  rs_pods_table | awk -v case_label="case=$CASE_LABEL" '
    NR > 1 && index($0, case_label) && index($0, "Running") {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^rs-04-web/) {
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

rs_pod_summary() {
  local pod_name node ip
  for pod_name in $(running_rs_pod_names | sort); do
    node="$(pod_node "$pod_name")"
    ip="$(pod_ip "$pod_name")"
    printf 'pod=%s node=%s podIP=%s labels=app=%s,case=%s\n' "$pod_name" "$node" "$ip" "$APP_LABEL" "$CASE_LABEL"
  done
}

rs_node_summary() {
  local nodes count
  nodes="$(rs_pod_summary | sed -n 's/.* node=\([^ ]*\) .*/\1/p' | sort -u | paste -sd ',' -)"
  count="$(printf '%s\n' "$nodes" | tr ',' '\n' | sed '/^$/d' | wc -l | tr -d ' ')"
  printf 'nodes=%s nodeCount=%s\n' "${nodes:-<none>}" "$count"
  test "$count" -ge 2
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

wait_for_rs_status() {
  local expected="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if "$KUBECTL_BIN" describe rs "$RS_NAME" 2>/dev/null | grep -Eq "^Desired:[[:space:]]*$expected$" &&
      "$KUBECTL_BIN" describe rs "$RS_NAME" 2>/dev/null | grep -Eq "^Current:[[:space:]]*$expected$"; then
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

wait_for_http() {
  local label="$1"
  local url="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >/tmp/minik8s-acceptance-04-http.out 2>/dev/null; then
      evidence_run "$label" curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url"
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$label"
  return 1
}

try_wait_for_http() {
  local url="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >/tmp/minik8s-acceptance-04-http.out 2>/dev/null; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  return 1
}

nodeport_backend_sample() {
  local url="$1"
  local attempts="${2:-20}"
  local tmp="/tmp/minik8s-acceptance-04-backends.out"
  : >"$tmp"
  local i
  for i in $(seq 1 "$attempts"); do
    curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" |
      awk -F= '
        /^ip=/ && $2 != "" {
          print "ip=" $2
          found = 1
          exit
        }
        /^pod=/ && $2 != "" && pod == "" {
          pod = "pod=" $2
        }
        END {
          if (!found && pod != "") {
            print pod
          }
        }
      ' >>"$tmp"
  done
  sort "$tmp" | uniq -c
  local distinct
  distinct="$(sort -u "$tmp" | sed '/^$/d' | wc -l | tr -d ' ')"
  printf 'attempts=%s distinctBackends=%s backendIdentity=ip-or-pod\n' "$attempts" "$distinct"
  test "$distinct" -ge 2
}

kubeproxy_nodeport_rules_summary() {
  local rules tmp
  tmp="/tmp/minik8s-acceptance-04-iptables.out"
  iptables-save -t nat >"$tmp"
  rules="$(grep -- "--dport $NODEPORT" "$tmp" | grep -E "MK8S-SVC|to-destination|mode random" || true)"
  printf '%s\n' "$rules"
  printf 'dnatRuleCount=%s randomRuleCount=%s\n' \
    "$(printf '%s\n' "$rules" | grep -c -- "--to-destination" || true)" \
    "$(printf '%s\n' "$rules" | grep -c -- "--mode random" || true)"
  test "$(printf '%s\n' "$rules" | grep -c -- "--to-destination" || true)" -ge 3
  printf '%s\n' "$rules" | grep -q -- "--mode random"
}

delete_service_if_exists() {
  if [ ! -x "$KUBECTL_BIN" ]; then
    return 0
  fi
  if "$KUBECTL_BIN" get svc "$SERVICE_NAME" >/dev/null 2>&1; then
    quiet_run "delete stale service $SERVICE_NAME" "$KUBECTL_BIN" delete svc "$SERVICE_NAME" || true
  fi
}

delete_rs_if_exists() {
  if [ ! -x "$KUBECTL_BIN" ]; then
    return 0
  fi
  if "$KUBECTL_BIN" get rs "$RS_NAME" >/dev/null 2>&1; then
    quiet_run "delete stale replicaset $RS_NAME" "$KUBECTL_BIN" delete rs "$RS_NAME" || true
  fi
}

delete_orphan_rs_pods() {
  if [ ! -x "$KUBECTL_BIN" ]; then
    return 0
  fi
  local pod_name
  for pod_name in $(rs_pod_names); do
    quiet_run "delete stale ReplicaSet pod $pod_name" "$KUBECTL_BIN" delete pod "$pod_name" || true
  done
}

cleanup_runtime() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if bash -lc 'test -z "$(docker ps -aq --filter label=minik8s.kind=pod-container --filter label=case=minik8s-acceptance-04)"' >/dev/null 2>&1; then
      break
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  quiet_run "remove temporary 04 HTTP outputs" rm -f /tmp/minik8s-acceptance-04-http.out /tmp/minik8s-acceptance-04-backends.out /tmp/minik8s-acceptance-04-iptables.out || true
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
    return 0
  fi
  quiet_check "kubectl binary exists" test -x "$KUBECTL_BIN" || return 1
  quiet_check "Harbor API is reachable" curl -fsS -o /dev/null "$MINIK8S_HARBOR/api/v1" || return 1
  quiet_check "$MINIK8S_NODE_A_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_A_NAME" || return 1
  quiet_check "$MINIK8S_NODE_B_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_B_NAME" || return 1
  quiet_check "CNI is enabled for ReplicaSet acceptance" bash -c 'test "${MINIK8S_CNI_DISABLED:-0}" != "1"' || return 1
  quiet_check "local mooring CNI config exists" test -f "$MINIK8S_CNI_CONF_DIR/10-mooring.conf" || return 1
  quiet_check "local mooring CNI plugin exists" test -x "$MINIK8S_CNI_BIN_DIR/mooring" || return 1
  quiet_check "bridge netfilter sends Pod traffic through iptables" bash -c 'test "$(sysctl -n net.bridge.bridge-nf-call-iptables 2>/dev/null)" = "1"' || return 1
  quiet_check "sailer service proxy is enabled" bash -c '! systemctl cat minik8s-sailer.service 2>/dev/null | grep -q -- "--proxy-disabled"' || return 1
  quiet_check "04 ReplicaSet manifests exist" test -f "$RS_MANIFEST" -a -f "$SERVICE_MANIFEST" || return 1
  PREFLIGHT_DONE=1
}

apply_rs() {
  evidence_run "$RS_NAME manifest applied" "$KUBECTL_BIN" apply -f "$RS_MANIFEST" || return 1
  wait_for_rs_running 3 || return 1
  wait_for_rs_status 3 || return 1
}

ensure_rs_workload() {
  if [ "$(running_rs_pod_count 2>/dev/null || printf '0')" -eq 3 ] &&
    "$KUBECTL_BIN" describe rs "$RS_NAME" 2>/dev/null | grep -Eq '^Desired:[[:space:]]*3$' &&
    "$KUBECTL_BIN" describe rs "$RS_NAME" 2>/dev/null | grep -Eq '^Current:[[:space:]]*3$'; then
    return 0
  fi
  delete_service_if_exists
  delete_rs_if_exists
  delete_orphan_rs_pods
  apply_rs
}

apply_service() {
  delete_service_if_exists
  evidence_run "$SERVICE_NAME Service manifest applied" "$KUBECTL_BIN" apply -f "$SERVICE_MANIFEST" || return 1
  wait_for_service_endpoints 3 || return 1
}

section_create_delete() {
  section_begin "04.1 replicaset create delete info multinode acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR manifests=$MANIFEST_DIR"
  if preflight; then
    cleanup_all
    evidence_run "ReplicaSet manifest shows name, pod template, selector, and replicas" rs_manifest_summary "$RS_MANIFEST" &&
      apply_rs &&
      evidence_run "kubectl describe shows ReplicaSet desired/current status and selector" "$KUBECTL_BIN" describe rs "$RS_NAME" &&
      evidence_run "$RS_NAME summary shows desired/current/running replicas" rs_summary &&
      evidence_run "$RS_NAME Pods show multi-node placement and Pod IPs" rs_pod_summary &&
      evidence_run "$RS_NAME Pods are scheduled on at least two nodes" rs_node_summary &&
      evidence_run "$RS_NAME deleted through kubectl" "$KUBECTL_BIN" delete rs "$RS_NAME" &&
      wait_for_rs_running 0 &&
      evidence_run "$RS_NAME owned Pods are absent after delete" bash -c '
        pods="$("$1" get pods | awk -v case_label="case=$2" "NR > 1 && index(\$0, case_label) {print}")"
        if [ -n "$pods" ]; then
          printf "%s\n" "$pods"
          exit 1
        fi
        printf "replicaset pods absent\n"
      ' _ "$KUBECTL_BIN" "$CASE_LABEL" &&
      evidence_run "$RS_NAME is absent after delete" bash -c 'if "$1" get rs "$2" >/dev/null 2>&1; then "$1" get rs "$2"; exit 1; fi; printf "%s absent\n" "$2"' _ "$KUBECTL_BIN" "$RS_NAME" || true
  fi
  cleanup_all
  section_end "04.1 cleaned ReplicaSet create/delete resources"
}

section_service_binding() {
  section_begin "04.2 replicaset service binding nodeport balancing acceptance"
  if preflight; then
    cleanup_all
    evidence_run "ReplicaSet Service manifest shows selector, NodePort, port, and targetPort" service_manifest_summary "$SERVICE_MANIFEST" &&
      apply_rs &&
      evidence_run "$RS_NAME Pods selected by Service" rs_pod_summary &&
      apply_service &&
      evidence_run "kubectl describe shows Service selector, NodePort, and endpoints" "$KUBECTL_BIN" describe svc "$SERVICE_NAME" &&
      evidence_run "$SERVICE_NAME summary shows three ReplicaSet endpoints" service_summary || true
  fi
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of)"
    if quiet_check "$SERVICE_NAME has NodePort $NODEPORT" test "$node_port" = "$NODEPORT" &&
      wait_for_http "node-a reaches local ReplicaSet NodePort $LOCAL_NODE_IP:$node_port" "http://$LOCAL_NODE_IP:$node_port/"; then
      evidence_run "NodePort traffic reaches at least two ReplicaSet backends through random DNAT" nodeport_backend_sample "http://$LOCAL_NODE_IP:$node_port/" 20 || true
      evidence_run "kube-proxy programs random DNAT rules for three ReplicaSet backends" kubeproxy_nodeport_rules_summary || true
    fi
    if [ "$SECTION_STATUS" = "PASS" ]; then
      if try_wait_for_http "http://$REMOTE_NODE_IP:$node_port/"; then
        evidence_run "node-a host reaches node-b ReplicaSet NodePort $REMOTE_NODE_IP:$node_port" curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "http://$REMOTE_NODE_IP:$node_port/" || true
      else
        section_limited "node-b NodePort access from node-a failed; local NodePort and backend balancing worked"
      fi
    fi
  fi
  delete_service_if_exists
  if [ "$SECTION_STATUS" != "PASS" ] && [ "$SECTION_STATUS" != "LIMITED" ]; then
    delete_rs_if_exists
    delete_orphan_rs_pods
    cleanup_runtime
    section_end "04.2 cleaned ReplicaSet Service resources after failure"
    return 0
  fi
  section_end "04.2 cleaned only Service and kept ReplicaSet for 04.3"
}

section_recovery() {
  section_begin "04.3 replicaset recovery acceptance"
  if preflight &&
    ensure_rs_workload; then
    local before deleted after
    before="$(running_rs_pod_names | sort | tr '\n' ' ')"
    deleted="$(running_rs_pod_names | sort | head -n 1)"
    evidence_run "$RS_NAME starts with three Running Pods" bash -c 'printf "before_pods=%s\nrunningCount=%s\n" "$1" "$2"; test "$2" = "3"' _ "$before" "$(running_rs_pod_count)" &&
      quiet_check "selected Pod for deletion exists" test -n "$deleted" &&
      evidence_run "delete one ReplicaSet-owned Pod $deleted" "$KUBECTL_BIN" delete pod "$deleted" &&
      wait_for_rs_running 3 &&
      wait_for_rs_status 3 &&
      evidence_run "$RS_NAME recovered to desired/current/running replicas" rs_summary || true
    if [ "$SECTION_STATUS" = "PASS" ]; then
      after="$(running_rs_pod_names | sort | tr '\n' ' ')"
      evidence_run "$RS_NAME created a replacement Pod after deletion" bash -c '
        before="$1"
        after="$2"
        deleted="$3"
        printf "deleted=%s\nbefore=%s\nafter=%s\n" "$deleted" "$before" "$after"
        ! printf "%s\n" "$after" | grep -Fq "$deleted"
      ' _ "$before" "$after" "$deleted" || true
    fi
  fi
  cleanup_all
  section_end "04.3 cleaned all ReplicaSet acceptance resources"
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
  if [ "$SECTION_LIMITED_COUNT" -gt 0 ]; then
    printf '[END] status=%s/%sPASS+%sLIMITED\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL" "$SECTION_LIMITED_COUNT"
  else
    printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  fi
  if [ "$SECTION_ACCEPT_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
