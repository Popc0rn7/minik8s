#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/03_service.sh
  bash scripts/acceptance/03_service.sh cleanup
  bash scripts/acceptance/03_service.sh --help

Run on node-a after 01_node_multinode.sh has started bridge, all sailers,
mooring CNI, and kube-proxy. This script uses fixed manifests under
manifests/service/ in the deployed tree or manifest/service/ in the source tree.

Sections:
  03.1&2 Service create/delete, info, selector, and endpoints
  03.3 ClusterIP host and Pod access
  03.4 NodePort external access
  03.5 Dynamic endpoints

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
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_WAIT_ATTEMPTS:-30}"
WAIT_SLEEP_SECONDS="${MINIK8S_ACCEPTANCE_WAIT_SLEEP_SECONDS:-2}"
HTTP_TIMEOUT_SECONDS="${MINIK8S_ACCEPTANCE_HTTP_TIMEOUT_SECONDS:-3}"
LOCAL_NODE_NAME="${MINIK8S_ACCEPTANCE_LOCAL_NODE:-${MINIK8S_NODE_A_NAME:-node-a}}"
LOCAL_NODE_IP="${MINIK8S_NODE_A_IP:-192.168.1.4}"
REMOTE_NODE_IP="${MINIK8S_NODE_B_IP:-192.168.1.10}"
NODEPORT="${MINIK8S_ACCEPTANCE_03_NODEPORT:-30080}"

POD_NGINX_A="svc-03-nginx-a"
POD_NGINX_B="svc-03-nginx-b"
POD_CLIENT="svc-03-client"
SVC_CLUSTERIP="svc-03-clusterip"
SVC_NODEPORT="svc-03-nodeport"
ALL_PODS=("$POD_CLIENT" "$POD_NGINX_A" "$POD_NGINX_B")
ALL_SERVICES=("$SVC_NODEPORT" "$SVC_CLUSTERIP")

SECTION_STATUS="PASS"
SECTION_PASS_COUNT=0
SECTION_ACCEPT_COUNT=0
SECTION_LIMITED_COUNT=0
SECTION_TOTAL=4
PREFLIGHT_DONE=0

manifest_dir() {
  if [ -d "$REMOTE_DIR/manifests/service" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/service"
    return 0
  fi
  printf '%s\n' "$ROOT/manifest/service"
}

MANIFEST_DIR="$(manifest_dir)"
POD_A_MANIFEST="$MANIFEST_DIR/pod_03_nginx_node_a.yaml"
POD_B_MANIFEST="$MANIFEST_DIR/pod_03_nginx_node_b.yaml"
CLIENT_MANIFEST="$MANIFEST_DIR/pod_03_client.yaml"
CLUSTERIP_MANIFEST="$MANIFEST_DIR/service_03_clusterip.yaml"
NODEPORT_MANIFEST="$MANIFEST_DIR/service_03_nodeport.yaml"

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

container_id_cmd() {
  local pod_name="$1"
  local container_name="$2"
  printf "docker ps -aq --filter label=minik8s.kind=pod-container --filter label=minik8s.pod.name=%q --filter label=minik8s.container.name=%q | head -n 1" "$pod_name" "$container_name"
}

pod_yaml() {
  "$KUBECTL_BIN" get pod "$1" -o yaml
}

service_yaml() {
  "$KUBECTL_BIN" get svc "$1" -o yaml
}

pod_ip() {
  pod_yaml "$1" | sed -n 's/^[[:space:]]*podIP:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

pod_node() {
  pod_yaml "$1" | sed -n 's/^[[:space:]]*nodeName:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

cluster_ip_of() {
  service_yaml "$1" | sed -n 's/^[[:space:]]*clusterIP:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

node_port_of() {
  service_yaml "$1" | sed -n 's/^[[:space:]]*nodePort:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

endpoint_ips() {
  service_yaml "$1" | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | tr -d '"'
}

endpoint_count() {
  endpoint_ips "$1" | sed '/^$/d' | wc -l | tr -d ' '
}

service_manifest_summary() {
  sed -n '
    s/^kind:/kind:/p
    s/^apiVersion:/apiVersion:/p
    s/^[[:space:]]*name:[[:space:]]*/name: /p
    s/^[[:space:]]*type:[[:space:]]*/type: /p
    s/^[[:space:]]*app:[[:space:]]*/selector.app: /p
    s/^[[:space:]]*port:[[:space:]]*/port: /p
    s/^[[:space:]]*targetPort:[[:space:]]*/targetPort: /p
    s/^[[:space:]]*nodePort:[[:space:]]*/nodePort: /p
  ' "$1"
}

service_summary() {
  local svc_name="$1"
  local yaml type cluster_ip node_port port target_port endpoints
  yaml="$(service_yaml "$svc_name")"
  type="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*type:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  cluster_ip="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*clusterIP:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  node_port="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*nodePort:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  port="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*port:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  target_port="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*targetPort:[[:space:]]*//p' | head -n 1 | tr -d '"')"
  endpoints="$(printf '%s\n' "$yaml" | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | tr -d '"' | paste -sd ',' -)"
  printf 'service=%s type=%s selector=app=svc-03-nginx clusterIP=%s port=%s targetPort=%s nodePort=%s endpoints=%s endpointCount=%s\n' \
    "$svc_name" "$type" "$cluster_ip" "$port" "$target_port" "${node_port:-<none>}" "${endpoints:-<none>}" "$(endpoint_count "$svc_name")"
}

service_absence_summary() {
  local svc_name="$1"
  if "$KUBECTL_BIN" get svc "$svc_name" >/dev/null 2>&1; then
    "$KUBECTL_BIN" get svc "$svc_name"
    return 1
  fi
  printf 'service=%s absent\n' "$svc_name"
}

pod_endpoint_summary() {
  local pod_name ip node
  for pod_name in "$POD_NGINX_A" "$POD_NGINX_B"; do
    ip="$(pod_ip "$pod_name")"
    node="$(pod_node "$pod_name")"
    printf 'pod=%s node=%s podIP=%s labels=app=svc-03-nginx,case=minik8s-acceptance-03\n' "$pod_name" "$node" "$ip"
  done
}

wait_for_pod_running() {
  local pod_name="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if "$KUBECTL_BIN" describe pod "$pod_name" 2>/dev/null | grep -Eq '^Status:[[:space:]]*Running'; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$pod_name final describe before failure" "$KUBECTL_BIN" describe pod "$pod_name" || true
  section_fail "$pod_name did not become Running"
  return 1
}

wait_for_service_endpoints() {
  local svc_name="$1"
  local expected="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local count
    count="$(endpoint_count "$svc_name" 2>/dev/null || printf '0')"
    if [ "$count" -eq "$expected" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$svc_name final describe before endpoint failure" "$KUBECTL_BIN" describe svc "$svc_name" || true
  section_fail "$svc_name endpoint count did not become $expected"
  return 1
}

wait_for_http() {
  local label="$1"
  local url="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >/tmp/minik8s-acceptance-03-http.out 2>/dev/null; then
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
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >/tmp/minik8s-acceptance-03-http.out 2>/dev/null; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  return 1
}

wait_for_client_http() {
  local label="$1"
  local url="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if bash -lc "cid=\$($(container_id_cmd "$POD_CLIENT" client)); test -n \"\$cid\"; docker exec \"\$cid\" wget -T \"$HTTP_TIMEOUT_SECONDS\" -qO- \"$url\" >/tmp/minik8s-acceptance-03-client.out" >/dev/null 2>&1; then
      evidence_run "$label" bash -lc "cid=\$($(container_id_cmd "$POD_CLIENT" client)); test -n \"\$cid\"; docker exec \"\$cid\" wget -T \"$HTTP_TIMEOUT_SECONDS\" -qO- \"$url\""
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$label"
  return 1
}

delete_service_if_exists() {
  local svc_name="$1"
  if "$KUBECTL_BIN" get svc "$svc_name" >/dev/null 2>&1; then
    quiet_run "delete stale service $svc_name" "$KUBECTL_BIN" delete svc "$svc_name" || true
  fi
}

delete_pod_if_exists() {
  local pod_name="$1"
  if "$KUBECTL_BIN" get pod "$pod_name" >/dev/null 2>&1; then
    quiet_run "delete stale pod $pod_name" "$KUBECTL_BIN" delete pod "$pod_name" || true
  fi
}

cleanup_services() {
  local svc_name
  for svc_name in "$@"; do
    delete_service_if_exists "$svc_name"
  done
}

cleanup_pods() {
  local pod_name
  for pod_name in "$@"; do
    delete_pod_if_exists "$pod_name"
  done
}

cleanup_runtime() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if bash -lc 'test -z "$(docker ps -aq --filter label=minik8s.kind=pod-container --filter label=case=minik8s-acceptance-03)"' >/dev/null 2>&1; then
      break
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  quiet_run "remove temporary 03 HTTP outputs" rm -f /tmp/minik8s-acceptance-03-http.out /tmp/minik8s-acceptance-03-client.out || true
}

cleanup_all() {
  cleanup_services "${ALL_SERVICES[@]}"
  cleanup_pods "${ALL_PODS[@]}"
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
  quiet_check "CNI is enabled for Service acceptance" bash -c 'test "${MINIK8S_CNI_DISABLED:-0}" != "1"' || return 1
  quiet_check "local mooring CNI config exists" test -f "$MINIK8S_CNI_CONF_DIR/10-mooring.conf" || return 1
  quiet_check "local mooring CNI plugin exists" test -x "$MINIK8S_CNI_BIN_DIR/mooring" || return 1
  quiet_check "bridge netfilter sends Pod traffic through iptables" bash -c 'test "$(sysctl -n net.bridge.bridge-nf-call-iptables 2>/dev/null)" = "1"' || return 1
  quiet_check "sailer service proxy is enabled" bash -c '! systemctl cat minik8s-sailer.service 2>/dev/null | grep -q -- "--proxy-disabled"' || return 1
  quiet_check "03 Service manifests exist" test -f "$POD_A_MANIFEST" -a -f "$POD_B_MANIFEST" -a -f "$CLIENT_MANIFEST" -a -f "$CLUSTERIP_MANIFEST" -a -f "$NODEPORT_MANIFEST" || return 1
  PREFLIGHT_DONE=1
}

ensure_nginx_workload() {
  if "$KUBECTL_BIN" describe pod "$POD_NGINX_A" 2>/dev/null | grep -Eq '^Status:[[:space:]]*Running' &&
    "$KUBECTL_BIN" describe pod "$POD_NGINX_B" 2>/dev/null | grep -Eq '^Status:[[:space:]]*Running'; then
    return 0
  fi
  cleanup_pods "$POD_NGINX_A" "$POD_NGINX_B"
  evidence_run "$POD_NGINX_A manifest applied" "$KUBECTL_BIN" apply -f "$POD_A_MANIFEST" || return 1
  evidence_run "$POD_NGINX_B manifest applied" "$KUBECTL_BIN" apply -f "$POD_B_MANIFEST" || return 1
  wait_for_pod_running "$POD_NGINX_A" || return 1
  wait_for_pod_running "$POD_NGINX_B" || return 1
}

ensure_client() {
  if "$KUBECTL_BIN" describe pod "$POD_CLIENT" 2>/dev/null | grep -Eq '^Status:[[:space:]]*Running'; then
    return 0
  fi
  cleanup_pods "$POD_CLIENT"
  evidence_run "$POD_CLIENT manifest applied" "$KUBECTL_BIN" apply -f "$CLIENT_MANIFEST" || return 1
  wait_for_pod_running "$POD_CLIENT" || return 1
}

apply_clusterip_service() {
  cleanup_services "$SVC_CLUSTERIP"
  evidence_run "$SVC_CLUSTERIP manifest applied" "$KUBECTL_BIN" apply -f "$CLUSTERIP_MANIFEST" || return 1
  wait_for_service_endpoints "$SVC_CLUSTERIP" 2 || return 1
}

ensure_clusterip_service() {
  local count
  count="$(endpoint_count "$SVC_CLUSTERIP" 2>/dev/null || printf '0')"
  if [ "$count" -eq 2 ]; then
    return 0
  fi
  apply_clusterip_service
}

apply_nodeport_service() {
  cleanup_services "$SVC_NODEPORT"
  evidence_run "$SVC_NODEPORT manifest applied" "$KUBECTL_BIN" apply -f "$NODEPORT_MANIFEST" || return 1
  wait_for_service_endpoints "$SVC_NODEPORT" 2 || return 1
}

section_service_create_info_selector() {
  section_begin "03.1&2 service create delete info selector endpoint acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR manifests=$MANIFEST_DIR"
  if preflight &&
    ensure_nginx_workload; then
    cleanup_services "$SVC_CLUSTERIP"
    evidence_run "ClusterIP Service manifest shows kind, name, selector, port, and targetPort" service_manifest_summary "$CLUSTERIP_MANIFEST" &&
      evidence_run "$SVC_CLUSTERIP is absent before create" service_absence_summary "$SVC_CLUSTERIP" &&
      evidence_run "$SVC_CLUSTERIP manifest applied" "$KUBECTL_BIN" apply -f "$CLUSTERIP_MANIFEST" &&
      wait_for_service_endpoints "$SVC_CLUSTERIP" 2 &&
      evidence_run "kubectl describe shows Service selector, virtual IP, ports, and endpoints" "$KUBECTL_BIN" describe svc "$SVC_CLUSTERIP" &&
      evidence_run "$SVC_CLUSTERIP summary shows two selected nginx endpoints" service_summary "$SVC_CLUSTERIP" &&
      evidence_run "selected backend Pods expose PodCIDR IPs on node-a and node-b" pod_endpoint_summary &&
      evidence_run "$SVC_CLUSTERIP deleted through kubectl" "$KUBECTL_BIN" delete svc "$SVC_CLUSTERIP" &&
      evidence_run "$SVC_CLUSTERIP is absent after delete" bash -c 'if "$1" get svc "$2" >/dev/null 2>&1; then "$1" get svc "$2"; exit 1; fi; printf "%s absent\n" "$2"' _ "$KUBECTL_BIN" "$SVC_CLUSTERIP" &&
      evidence_run "$SVC_CLUSTERIP manifest re-applied for following sections" "$KUBECTL_BIN" apply -f "$CLUSTERIP_MANIFEST" &&
      wait_for_service_endpoints "$SVC_CLUSTERIP" 2 &&
      evidence_run "$SVC_CLUSTERIP endpoints restored after re-create" service_summary "$SVC_CLUSTERIP" || true
  fi
  if [ "$SECTION_STATUS" != "PASS" ]; then
    cleanup_services "$SVC_CLUSTERIP"
    cleanup_pods "$POD_NGINX_A" "$POD_NGINX_B"
    cleanup_runtime
    section_end "03.1&2 cleaned Service selector resources after failure"
    return 0
  fi
  section_end "03.1&2 kept nginx Pods and ClusterIP Service for 03.3 and 03.5"
}

section_clusterip_access() {
  section_begin "03.3 clusterip host and pod access acceptance"
  if preflight &&
    ensure_nginx_workload &&
    ensure_clusterip_service &&
    ensure_client; then
    local cluster_ip
    cluster_ip="$(cluster_ip_of "$SVC_CLUSTERIP")"
    quiet_check "$SVC_CLUSTERIP has ClusterIP $cluster_ip" test -n "$cluster_ip" &&
      wait_for_http "node-a host reaches ClusterIP $cluster_ip" "http://$cluster_ip:80/" &&
      wait_for_client_http "client Pod reaches ClusterIP $cluster_ip" "http://$cluster_ip:80/" &&
      evidence_run "$SVC_CLUSTERIP summary after ClusterIP access" service_summary "$SVC_CLUSTERIP" || true
  fi
  cleanup_pods "$POD_CLIENT"
  section_end "03.3 cleaned only client Pod"
}

section_nodeport_access() {
  section_begin "03.4 nodeport external access acceptance"
  if preflight &&
    ensure_nginx_workload &&
    apply_nodeport_service; then
    local node_port
    node_port="$(node_port_of "$SVC_NODEPORT")"
    quiet_check "$SVC_NODEPORT has NodePort $NODEPORT" test "$node_port" = "$NODEPORT" &&
      evidence_run "$SVC_NODEPORT summary shows fixed NodePort" service_summary "$SVC_NODEPORT" &&
      wait_for_http "node-a host reaches local NodePort $LOCAL_NODE_IP:$node_port" "http://$LOCAL_NODE_IP:$node_port/" || true
    if [ "$SECTION_STATUS" = "PASS" ]; then
      if try_wait_for_http "http://$REMOTE_NODE_IP:$node_port/"; then
        evidence_run "node-a host reaches node-b NodePort $REMOTE_NODE_IP:$node_port" curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "http://$REMOTE_NODE_IP:$node_port/" || true
      else
        section_limited "node-b NodePort access from node-a failed; local NodePort access worked"
      fi
    fi
  fi
  cleanup_services "$SVC_NODEPORT"
  section_end "03.4 cleaned only NodePort Service"
}

section_dynamic_endpoints() {
  section_begin "03.5 dynamic endpoints acceptance"
  if preflight &&
    ensure_nginx_workload &&
    ensure_clusterip_service; then
    local pod_a_ip
    pod_a_ip="$(pod_ip "$POD_NGINX_A")"
    evidence_run "$SVC_CLUSTERIP starts with two endpoints" service_summary "$SVC_CLUSTERIP" &&
      evidence_run "$POD_NGINX_B deleted to trigger endpoint update" "$KUBECTL_BIN" delete pod "$POD_NGINX_B" &&
      wait_for_service_endpoints "$SVC_CLUSTERIP" 1 &&
      evidence_run "$SVC_CLUSTERIP endpoint set contains only $POD_NGINX_A" bash -c '
        "$1" get svc "$2" -o yaml | grep -F "ip: $3"
      ' _ "$KUBECTL_BIN" "$SVC_CLUSTERIP" "$pod_a_ip" &&
      evidence_run "$POD_NGINX_B manifest re-applied" "$KUBECTL_BIN" apply -f "$POD_B_MANIFEST" &&
      wait_for_pod_running "$POD_NGINX_B" &&
      wait_for_service_endpoints "$SVC_CLUSTERIP" 2 &&
      evidence_run "$SVC_CLUSTERIP endpoints restored to two backends" service_summary "$SVC_CLUSTERIP" || true
  fi
  cleanup_services "$SVC_CLUSTERIP"
  cleanup_pods "$POD_CLIENT" "$POD_NGINX_A" "$POD_NGINX_B"
  cleanup_runtime
  section_end "03.5 cleaned all Service acceptance resources"
}

main() {
  if [ "${1:-}" = "cleanup" ]; then
    begin "03 service acceptance cleanup"
    cleanup_all
    cleanup "03 cleanup completed"
    end
    return 0
  fi

  section_service_create_info_selector
  section_clusterip_access
  section_nodeport_access
  section_dynamic_endpoints
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
