#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/03_service.sh
  bash scripts/acceptance/03_service.sh cleanup
  bash scripts/acceptance/03_service.sh --help

Run on node-a after 01_node_multinode.sh has started bridge, all sailers,
and mooring CNI. This script uses fixed manifests under manifests/service/
in the deployed tree or manifest/service/ in the source tree.

Sections:
  03.1 Service create/delete and selector/endpoints
  03.2 ClusterIP host and Pod access
  03.3 NodePort external access
  03.4 Dynamic endpoints

Each section cleans its own resources and emits [END] status=<PASS|FAIL>.
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

section_output_run() {
  local message="$1"
  shift
  printf '[OUTPUT]\n'
  "$@" 2>&1 || {
    section_fail "$message"
    return 1
  }
  pass "$message"
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

container_id_cmd() {
  local pod_name="$1"
  local container_name="$2"
  printf "docker ps -aq --filter label=minik8s.kind=pod-container --filter label=minik8s.pod.name=%q --filter label=minik8s.container.name=%q | head -n 1" "$pod_name" "$container_name"
}

pod_ip() {
  "$KUBECTL_BIN" get pod "$1" -o yaml | sed -n 's/^[[:space:]]*podIP:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

service_yaml() {
  "$KUBECTL_BIN" get svc "$1" -o yaml
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

service_summary() {
  local svc_name="$1"
  "$KUBECTL_BIN" get svc "$svc_name"
  "$KUBECTL_BIN" describe svc "$svc_name"
}

wait_for_pod_running() {
  local pod_name="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if "$KUBECTL_BIN" describe pod "$pod_name" 2>/dev/null | grep -Eq '^Status:[[:space:]]*Running'; then
      pass "$pod_name is Running"
      return 0
    fi
    wait_note "waiting for pod $pod_name Running" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$pod_name final describe before failure" "$KUBECTL_BIN" describe pod "$pod_name" || true
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
      pass "$svc_name endpoint count is $expected"
      return 0
    fi
    wait_note "waiting for service $svc_name endpoints current=$count expected=$expected" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$svc_name final describe before endpoint failure" "$KUBECTL_BIN" describe svc "$svc_name" || true
  section_fail "$svc_name endpoint count did not become $expected"
  return 1
}

wait_for_http() {
  local label="$1"
  local url="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS "$url" >/tmp/minik8s-acceptance-03-http.out 2>/dev/null; then
      section_check_run "$label" bash -c 'head -n 3 /tmp/minik8s-acceptance-03-http.out'
      return 0
    fi
    wait_note "waiting for HTTP access $url" "$attempt"
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
    wait_note "waiting for optional HTTP access $url" "$attempt"
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
      section_check_run "$label" bash -c 'head -n 3 /tmp/minik8s-acceptance-03-client.out'
      return 0
    fi
    wait_note "waiting for client Pod HTTP access $url" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$label"
  return 1
}

delete_service_if_exists() {
  local svc_name="$1"
  if "$KUBECTL_BIN" get svc "$svc_name" >/dev/null 2>&1; then
    run "$KUBECTL_BIN" delete svc "$svc_name" || true
  else
    output "service $svc_name already absent"
  fi
}

delete_pod_if_exists() {
  local pod_name="$1"
  if "$KUBECTL_BIN" get pod "$pod_name" >/dev/null 2>&1; then
    run "$KUBECTL_BIN" delete pod "$pod_name" || true
  else
    output "pod $pod_name already absent"
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
      pass "no local acceptance Service containers remain"
      break
    fi
    wait_note "waiting for local runtime cleanup" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  run rm -f /tmp/minik8s-acceptance-03-http.out /tmp/minik8s-acceptance-03-client.out || true
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
    pass "shared preflight already passed"
    return 0
  fi
  section_check_quiet "kubectl binary exists" test -x "$KUBECTL_BIN" || return 1
  section_check_run "Harbor API is reachable" curl -fsS -o /dev/null -w 'http=%{http_code}\n' "$MINIK8S_HARBOR/api/v1" || return 1
  section_check_run "kubectl can list nodes" "$KUBECTL_BIN" get nodes || return 1
  section_check_quiet "$MINIK8S_NODE_A_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_A_NAME" || return 1
  section_check_quiet "$MINIK8S_NODE_B_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_B_NAME" || return 1
  section_check_quiet "$MINIK8S_NODE_C_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$MINIK8S_NODE_C_NAME" || return 1
  section_check_quiet "CNI is enabled for Service acceptance" bash -c 'test "${MINIK8S_CNI_DISABLED:-0}" != "1"' || return 1
  section_check_quiet "local mooring CNI config exists" test -f "$MINIK8S_CNI_CONF_DIR/10-mooring.conf" || return 1
  section_check_quiet "local mooring CNI plugin exists" test -x "$MINIK8S_CNI_BIN_DIR/mooring" || return 1
  section_check_quiet "bridge netfilter sends Pod traffic through iptables" bash -c 'test "$(sysctl -n net.bridge.bridge-nf-call-iptables 2>/dev/null)" = "1"' || return 1
  section_check_quiet "mooring ConfigMap endpoint exists" bash -c 'test "$(curl -sS -o /dev/null -w "%{http_code}" "$1")" = "200"' _ "$MINIK8S_HARBOR/api/v1/namespaces/kube-mooring/configmaps/mooring-cni-cfg" || return 1
  section_check_quiet "mooring DaemonSet endpoint exists" bash -c 'test "$(curl -sS -o /dev/null -w "%{http_code}" "$1")" = "200"' _ "$MINIK8S_HARBOR/apis/apps/v1/namespaces/kube-mooring/daemonsets/mooring-cni-ds" || return 1
  section_check_quiet "sailer service proxy is enabled" bash -c '! systemctl cat minik8s-sailer.service 2>/dev/null | grep -q -- "--proxy-disabled"' || return 1
  section_check_quiet "03 Service manifests exist" test -f "$POD_A_MANIFEST" -a -f "$POD_B_MANIFEST" -a -f "$CLIENT_MANIFEST" -a -f "$CLUSTERIP_MANIFEST" -a -f "$NODEPORT_MANIFEST" || return 1
  PREFLIGHT_DONE=1
}

apply_nginx_workload() {
  cleanup_pods "$POD_CLIENT" "$POD_NGINX_A" "$POD_NGINX_B"
  section_check_run "$POD_NGINX_A manifest applied" "$KUBECTL_BIN" apply -f "$POD_A_MANIFEST" || return 1
  section_check_run "$POD_NGINX_B manifest applied" "$KUBECTL_BIN" apply -f "$POD_B_MANIFEST" || return 1
  wait_for_pod_running "$POD_NGINX_A" || return 1
  wait_for_pod_running "$POD_NGINX_B" || return 1
  section_check_run "nginx Pods expose PodCIDR IPs" bash -c '
    KUBECTL_BIN="$1"
    shift
    for pod_name in "$@"; do
      ip="$("$KUBECTL_BIN" get pod "$pod_name" -o yaml | sed -n "s/^[[:space:]]*podIP:[[:space:]]*//p" | head -n 1 | tr -d "\"")"
      printf "%s podIP=%s\n" "$pod_name" "$ip"
      test -n "$ip"
    done
  ' _ "$KUBECTL_BIN" "$POD_NGINX_A" "$POD_NGINX_B" || return 1
}

apply_client() {
  section_check_run "$POD_CLIENT manifest applied" "$KUBECTL_BIN" apply -f "$CLIENT_MANIFEST" || return 1
  wait_for_pod_running "$POD_CLIENT" || return 1
}

apply_clusterip_service() {
  cleanup_services "$SVC_CLUSTERIP"
  section_check_run "$SVC_CLUSTERIP manifest applied" "$KUBECTL_BIN" apply -f "$CLUSTERIP_MANIFEST" || return 1
  wait_for_service_endpoints "$SVC_CLUSTERIP" 2 || return 1
  section_check_run "$SVC_CLUSTERIP summary shows selector, ClusterIP, ports, and endpoints" service_summary "$SVC_CLUSTERIP" || return 1
}

apply_nodeport_service() {
  cleanup_services "$SVC_NODEPORT"
  section_check_run "$SVC_NODEPORT manifest applied" "$KUBECTL_BIN" apply -f "$NODEPORT_MANIFEST" || return 1
  wait_for_service_endpoints "$SVC_NODEPORT" 2 || return 1
  section_check_run "$SVC_NODEPORT summary shows selector, ClusterIP, NodePort, and endpoints" service_summary "$SVC_NODEPORT" || return 1
}

section_create_delete_selector() {
  section_begin "03.1 service create delete selector acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR manifests=$MANIFEST_DIR"
  preflight &&
    cleanup_all &&
    apply_nginx_workload &&
    apply_clusterip_service &&
    section_check_run "selector chooses exactly two nginx endpoints" bash -c '
      count="$("$1" get svc "$2" -o yaml | sed -n "s/^[[:space:]]*ip:[[:space:]]*//p" | sed "/^$/d" | wc -l | tr -d " ")"
      test "$count" -eq 2
    ' _ "$KUBECTL_BIN" "$SVC_CLUSTERIP" &&
    section_check_run "$SVC_CLUSTERIP deleted" "$KUBECTL_BIN" delete svc "$SVC_CLUSTERIP" &&
    section_check_quiet "$SVC_CLUSTERIP is absent after delete" bash -c '! "$1" get svc "$2"' _ "$KUBECTL_BIN" "$SVC_CLUSTERIP"
  cleanup_services "$SVC_CLUSTERIP"
  cleanup_pods "$POD_CLIENT" "$POD_NGINX_A" "$POD_NGINX_B"
  cleanup_runtime
  section_end "03.1 cleaned Service selector resources"
}

section_clusterip_access() {
  section_begin "03.2 clusterip host and pod access acceptance"
  preflight &&
    cleanup_all &&
    apply_nginx_workload &&
    apply_client &&
    apply_clusterip_service
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local cluster_ip
    cluster_ip="$(cluster_ip_of "$SVC_CLUSTERIP")"
    section_check_quiet "$SVC_CLUSTERIP has ClusterIP $cluster_ip" test -n "$cluster_ip" &&
      wait_for_http "node-a host reaches ClusterIP $cluster_ip" "http://$cluster_ip:80/" &&
      wait_for_client_http "client Pod reaches ClusterIP $cluster_ip" "http://$cluster_ip:80/" &&
      section_check_run "$SVC_CLUSTERIP still has two endpoints" service_summary "$SVC_CLUSTERIP"
  fi
  cleanup_services "$SVC_CLUSTERIP"
  cleanup_pods "$POD_CLIENT" "$POD_NGINX_A" "$POD_NGINX_B"
  cleanup_runtime
  section_end "03.2 cleaned ClusterIP access resources"
}

section_nodeport_access() {
  section_begin "03.3 nodeport external access acceptance"
  preflight &&
    cleanup_all &&
    apply_nginx_workload &&
    apply_nodeport_service
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local node_port
    node_port="$(node_port_of "$SVC_NODEPORT")"
    section_check_quiet "$SVC_NODEPORT has NodePort $NODEPORT" test "$node_port" = "$NODEPORT" &&
      wait_for_http "node-a host reaches local NodePort $LOCAL_NODE_IP:$node_port" "http://$LOCAL_NODE_IP:$node_port/"
    if try_wait_for_http "http://$REMOTE_NODE_IP:$node_port/"; then
      section_check_run "node-a host reaches node-b NodePort $REMOTE_NODE_IP:$node_port" bash -c 'head -n 3 /tmp/minik8s-acceptance-03-http.out'
    else
      section_limited "node-b NodePort access from node-a failed; local NodePort access worked"
    fi
  fi
  cleanup_services "$SVC_NODEPORT"
  cleanup_pods "$POD_CLIENT" "$POD_NGINX_A" "$POD_NGINX_B"
  cleanup_runtime
  section_end "03.3 cleaned NodePort access resources"
}

section_dynamic_endpoints() {
  section_begin "03.4 dynamic endpoints acceptance"
  preflight &&
    cleanup_all &&
    apply_nginx_workload &&
    apply_clusterip_service
  if [ "$SECTION_STATUS" = "PASS" ]; then
    local cluster_ip pod_a_ip
    cluster_ip="$(cluster_ip_of "$SVC_CLUSTERIP")"
    pod_a_ip="$(pod_ip "$POD_NGINX_A")"
    cleanup_pods "$POD_NGINX_B"
    wait_for_service_endpoints "$SVC_CLUSTERIP" 1 &&
      section_check_run "$SVC_CLUSTERIP endpoint set now contains only $POD_NGINX_A" bash -c '
        "$1" get svc "$2" -o yaml | grep -F "ip: $3"
      ' _ "$KUBECTL_BIN" "$SVC_CLUSTERIP" "$pod_a_ip" &&
      section_check_run "$POD_NGINX_B manifest re-applied" "$KUBECTL_BIN" apply -f "$POD_B_MANIFEST" &&
      wait_for_pod_running "$POD_NGINX_B" &&
      wait_for_service_endpoints "$SVC_CLUSTERIP" 2 &&
      wait_for_http "ClusterIP $cluster_ip works after endpoint restore" "http://$cluster_ip:80/"
  fi
  cleanup_services "$SVC_CLUSTERIP"
  cleanup_pods "$POD_CLIENT" "$POD_NGINX_A" "$POD_NGINX_B"
  cleanup_runtime
  section_end "03.4 cleaned dynamic endpoint resources"
}

main() {
  if [ "${1:-}" = "cleanup" ]; then
    begin "03 service acceptance cleanup"
    cleanup_all
    cleanup "03 cleanup completed"
    end
    return 0
  fi

  section_create_delete_selector
  section_clusterip_access
  section_nodeport_access
  section_dynamic_endpoints
  printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  if [ "$SECTION_PASS_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
