#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/06_dns_forwarding.sh
  bash scripts/acceptance/06_dns_forwarding.sh cleanup
  bash scripts/acceptance/06_dns_forwarding.sh --help

Run on node-a after 01_node_multinode.sh has started bridge, all sailers,
mooring CNI, kube-proxy, and the DNS addon.

Sections:
  06.1 DNS object and sync
  06.2 Host ingress path routing
  06.3 Pod DNS resolution and delete behavior

Each section cleans its own resources and emits [END] status=<PASS|FAIL|LIMITED>.
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
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_06_WAIT_ATTEMPTS:-60}"
WAIT_SLEEP_SECONDS="${MINIK8S_ACCEPTANCE_WAIT_SLEEP_SECONDS:-2}"
HTTP_TIMEOUT_SECONDS="${MINIK8S_ACCEPTANCE_HTTP_TIMEOUT_SECONDS:-3}"
DNS_DIR="${MINIK8S_DNS_DIR:-$REMOTE_DIR/dns}"
DNS_QUERY_PORT="${MINIK8S_ACCEPTANCE_06_DNS_QUERY_PORT:-}"
DNS_HOST="acceptance06.minik8s.local"
INGRESS_URL="${MINIK8S_ACCEPTANCE_06_INGRESS_URL:-http://127.0.0.1}"
DNS_SERVICE_NS="minik8s-system"
DNS_SERVICE_NAME="minik8s-dns"

RS_ALPHA="rs-06-alpha"
RS_BETA="rs-06-beta"
SVC_ALPHA="service-06-alpha"
SVC_BETA="service-06-beta"
DNS_NAME="dns-06-routes"
CLIENT_POD="pod-06-client"
CASE_LABEL="minik8s-acceptance-06"

SECTION_STATUS="PASS"
SECTION_PASS_COUNT=0
SECTION_TOTAL=3
PREFLIGHT_DONE=0

manifest_dir() {
  if [ -d "$REMOTE_DIR/manifests/dns" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/dns"
    return 0
  fi
  printf '%s\n' "$ROOT/manifest/dns"
}

MANIFEST_DIR="$(manifest_dir)"
RS_ALPHA_MANIFEST="$MANIFEST_DIR/replicaset_06_alpha.yaml"
RS_BETA_MANIFEST="$MANIFEST_DIR/replicaset_06_beta.yaml"
SVC_ALPHA_MANIFEST="$MANIFEST_DIR/service_06_alpha.yaml"
SVC_BETA_MANIFEST="$MANIFEST_DIR/service_06_beta.yaml"
DNS_MANIFEST="$MANIFEST_DIR/dns_06_routes.yaml"
CLIENT_MANIFEST="$MANIFEST_DIR/pod_06_client.yaml"
ROUTES_PATH="$DNS_DIR/routes.json"
HOSTS_PATH="$DNS_DIR/hosts"

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

detect_dns_query_port() {
  if [ -n "$DNS_QUERY_PORT" ]; then
    printf '%s\n' "$DNS_QUERY_PORT"
    return 0
  fi
  if ss -H -lun 2>/dev/null | awk '{print $4}' | grep -Eq "(^|[.:])${MINIK8S_DNS_PORT}$"; then
    printf '%s\n' "$MINIK8S_DNS_PORT"
    return 0
  fi
  if ss -H -lun 2>/dev/null | awk '{print $4}' | grep -Eq "(^|[.:])53$"; then
    printf '%s\n' "53"
    return 0
  fi
  printf '%s\n' "$MINIK8S_DNS_PORT"
}

container_id_cmd() {
  local pod_name="$1"
  local container_name="$2"
  printf "docker ps -aq --filter label=minik8s.kind=pod-container --filter label=minik8s.pod.name=%q --filter label=minik8s.container.name=%q | head -n 1" "$pod_name" "$container_name"
}

running_pod_count_for_app() {
  local app="$1"
  "$KUBECTL_BIN" get pods | awk -v app="app=$app" -v case_label="case=$CASE_LABEL" 'NR > 1 && index($0, app) && index($0, case_label) && index($0, "Running") {count++} END {print count + 0}'
}

service_yaml() {
  "$KUBECTL_BIN" get svc "$1" -o yaml
}

dns_service_yaml() {
  "$KUBECTL_BIN" -n "$DNS_SERVICE_NS" get svc "$DNS_SERVICE_NAME" -o yaml
}

dns_service_cluster_ip() {
  dns_service_yaml | sed -n 's/^[[:space:]]*clusterIP:[[:space:]]*//p' | head -n 1 | tr -d ' '
}

endpoint_count() {
  service_yaml "$1" | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | sed '/^$/d' | wc -l | tr -d ' '
}

dns_service_endpoint_count() {
  dns_service_yaml | sed -n 's/^[[:space:]]*ip:[[:space:]]*//p' | sed '/^$/d' | wc -l | tr -d ' '
}

wait_for_rs_running() {
  local app="$1"
  local expected="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local count
    count="$(running_pod_count_for_app "$app")"
    if [ "$count" -eq "$expected" ]; then
      pass "$app has $expected Running Pods"
      return 0
    fi
    wait_note "waiting for $app Running Pods current=$count expected=$expected" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$app final Pod list before failure" "$KUBECTL_BIN" get pods || true
  section_fail "$app did not reach $expected Running Pods"
  return 1
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

wait_for_cluster_dns_service() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local cluster_ip count
    cluster_ip="$(dns_service_cluster_ip 2>/dev/null || true)"
    count="$(dns_service_endpoint_count 2>/dev/null || printf '0')"
    if [ -n "$cluster_ip" ] && [ "$count" -ge 2 ]; then
      section_check_run "$DNS_SERVICE_NS/$DNS_SERVICE_NAME exposes ClusterIP DNS with endpoints" dns_service_yaml
      return 0
    fi
    wait_note "waiting for cluster DNS Service clusterIP=$cluster_ip endpoints=$count" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$DNS_SERVICE_NS/$DNS_SERVICE_NAME did not expose ClusterIP DNS endpoints"
  return 1
}

wait_for_dns_sync() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if [ -f "$ROUTES_PATH" ] && [ -f "$HOSTS_PATH" ] &&
      grep -Fq "$DNS_HOST" "$ROUTES_PATH" &&
      grep -Fq "$DNS_HOST" "$HOSTS_PATH" &&
      grep -Fq "$SVC_ALPHA" "$ROUTES_PATH" &&
      grep -Fq "$SVC_BETA" "$ROUTES_PATH"; then
      section_check_run "DNS sync files contain host and both route targets" bash -c 'printf "hosts:\n"; grep -F "$1" "$2"; printf "routes:\n"; grep -E "$1|$3|$4" "$5"' _ "$DNS_HOST" "$HOSTS_PATH" "$SVC_ALPHA" "$SVC_BETA" "$ROUTES_PATH"
      return 0
    fi
    wait_note "waiting for DNS sync files host=$DNS_HOST" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "DNS sync files did not include $DNS_HOST and route targets"
  return 1
}

wait_for_dns_delete_sync() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if { [ ! -f "$ROUTES_PATH" ] || ! grep -Fq "$DNS_HOST" "$ROUTES_PATH"; } &&
      { [ ! -f "$HOSTS_PATH" ] || ! grep -Fq "$DNS_HOST" "$HOSTS_PATH"; }; then
      pass "DNS sync files no longer contain $DNS_HOST"
      return 0
    fi
    wait_note "waiting for DNS sync files to remove host=$DNS_HOST" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "DNS sync files still contain $DNS_HOST after delete"
  return 1
}

wait_for_host_http() {
  local route="$1"
  local want="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS -H "Host: $DNS_HOST" "$INGRESS_URL$route" >/tmp/minik8s-acceptance-06-http.out 2>/dev/null &&
      grep -Fq "$want" /tmp/minik8s-acceptance-06-http.out; then
      section_check_run "host ingress $route returns $want" bash -c 'cat /tmp/minik8s-acceptance-06-http.out'
      return 0
    fi
    wait_note "waiting for host ingress route $route" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "host ingress $route did not return $want"
  return 1
}

try_pod_http() {
  local route="$1"
  local want="$2"
  bash -lc "cid=\$($(container_id_cmd "$CLIENT_POD" client)); test -n \"\$cid\"; docker exec \"\$cid\" wget -T \"$HTTP_TIMEOUT_SECONDS\" -qO- \"http://$DNS_HOST$route\" >/tmp/minik8s-acceptance-06-client.out" >/dev/null 2>&1 &&
    grep -Fq "$want" /tmp/minik8s-acceptance-06-client.out
}

check_client_resolver() {
  local cluster_ip
  cluster_ip="$(dns_service_cluster_ip 2>/dev/null || true)"
  if [ -z "$cluster_ip" ]; then
    section_fail "cluster DNS Service has no ClusterIP"
    return 1
  fi
  if bash -lc "cid=\$($(container_id_cmd "$CLIENT_POD" client)); test -n \"\$cid\"; docker exec \"\$cid\" cat /etc/resolv.conf" >/tmp/minik8s-acceptance-06-resolv.out 2>/dev/null &&
    grep -Fq "nameserver $cluster_ip" /tmp/minik8s-acceptance-06-resolv.out; then
    section_check_run "client Pod resolver uses cluster DNS Service $cluster_ip" bash -c 'cat /tmp/minik8s-acceptance-06-resolv.out'
    return 0
  fi
  section_check_run "client Pod resolver before failure" bash -c 'cat /tmp/minik8s-acceptance-06-resolv.out 2>/dev/null || true'
  section_fail "client Pod resolver does not use cluster DNS Service $cluster_ip"
  return 1
}

wait_for_pod_http() {
  local route="$1"
  local want="$2"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if try_pod_http "$route" "$want"; then
      section_check_run "client Pod reaches $DNS_HOST$route and receives $want" bash -c 'cat /tmp/minik8s-acceptance-06-client.out'
      return 0
    fi
    wait_note "waiting for client Pod DNS HTTP route $route" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "client Pod could not resolve and reach $DNS_HOST$route"
  return 1
}

check_host_dns_query() {
  local query_port candidate seen attempt
  if command -v dig >/dev/null 2>&1; then
    attempt=1
    while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
      seen=""
      for candidate in "${DNS_QUERY_PORT:-}" "$MINIK8S_DNS_PORT" 53; do
        [ -n "$candidate" ] || continue
        case " $seen " in *" $candidate "*) continue ;; esac
        seen="$seen $candidate"
        if dig "@$MINIK8S_NODE_A_IP" -p "$candidate" "$DNS_HOST" +short | tee /tmp/minik8s-acceptance-06-dns.out >/dev/null &&
          grep -Fx "$MINIK8S_NODE_A_IP" /tmp/minik8s-acceptance-06-dns.out >/dev/null; then
          query_port="$candidate"
          section_check_run "dig resolves $DNS_HOST to $MINIK8S_NODE_A_IP through minik8s DNS addon port $query_port" bash -c 'cat /tmp/minik8s-acceptance-06-dns.out'
          return $?
        fi
      done
      wait_note "waiting for dig to resolve $DNS_HOST to $MINIK8S_NODE_A_IP" "$attempt"
      sleep "$WAIT_SLEEP_SECONDS"
      attempt=$((attempt + 1))
    done
    section_fail "dig did not resolve $DNS_HOST to $MINIK8S_NODE_A_IP through tested DNS addon ports"
    return 1
  fi
  if command -v nslookup >/dev/null 2>&1; then
    attempt=1
    while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
      seen=""
      for candidate in "${DNS_QUERY_PORT:-}" "$MINIK8S_DNS_PORT" 53; do
        [ -n "$candidate" ] || continue
        case " $seen " in *" $candidate "*) continue ;; esac
        seen="$seen $candidate"
        if nslookup -port="$candidate" "$DNS_HOST" "$MINIK8S_NODE_A_IP" | tee /tmp/minik8s-acceptance-06-dns.out >/dev/null &&
          (grep -Fx "Address: $MINIK8S_NODE_A_IP" /tmp/minik8s-acceptance-06-dns.out >/dev/null || grep -Fx "Address 1: $MINIK8S_NODE_A_IP" /tmp/minik8s-acceptance-06-dns.out >/dev/null); then
          query_port="$candidate"
          section_check_run "nslookup resolves $DNS_HOST to $MINIK8S_NODE_A_IP through minik8s DNS addon port $query_port" bash -c 'cat /tmp/minik8s-acceptance-06-dns.out'
          return $?
        fi
      done
      wait_note "waiting for nslookup to resolve $DNS_HOST to $MINIK8S_NODE_A_IP" "$attempt"
      sleep "$WAIT_SLEEP_SECONDS"
      attempt=$((attempt + 1))
    done
    section_fail "nslookup did not resolve $DNS_HOST to $MINIK8S_NODE_A_IP through tested DNS addon ports"
    return 1
  fi
  pass "neither dig nor nslookup is available on host; DNS query skipped because Pod resolver is checked in 06.3"
  return 0
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

cleanup_runtime() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if bash -lc 'test -z "$(docker ps -aq --filter label=minik8s.kind=pod-container --filter label=case=minik8s-acceptance-06)"' >/dev/null 2>&1; then
      pass "no local acceptance DNS containers remain"
      break
    fi
    wait_note "waiting for local runtime cleanup" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  run rm -f /tmp/minik8s-acceptance-06-http.out /tmp/minik8s-acceptance-06-client.out /tmp/minik8s-acceptance-06-dns.out /tmp/minik8s-acceptance-06-resolv.out || true
}

cleanup_all() {
  delete_if_exists dns "$DNS_NAME"
  delete_if_exists pod "$CLIENT_POD"
  delete_if_exists svc "$SVC_ALPHA"
  delete_if_exists svc "$SVC_BETA"
  delete_if_exists rs "$RS_ALPHA"
  delete_if_exists rs "$RS_BETA"
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
  section_check_run "kubectl can list DNS objects" "$KUBECTL_BIN" get dns || return 1
  wait_for_cluster_dns_service || return 1
  section_check_quiet "DNS directory exists" test -d "$DNS_DIR" || return 1
  output "dns_query_port=$(detect_dns_query_port) configured_dns_port=$MINIK8S_DNS_PORT"
  section_check_quiet "DNS addon route files are writable by bridge" test -f "$ROUTES_PATH" -o -d "$DNS_DIR" || return 1
  section_check_quiet "06 DNS manifests exist" test -f "$RS_ALPHA_MANIFEST" -a -f "$RS_BETA_MANIFEST" -a -f "$SVC_ALPHA_MANIFEST" -a -f "$SVC_BETA_MANIFEST" -a -f "$DNS_MANIFEST" -a -f "$CLIENT_MANIFEST" || return 1
  PREFLIGHT_DONE=1
}

apply_backends_services_dns() {
  section_check_run "$RS_ALPHA manifest applied" "$KUBECTL_BIN" apply -f "$RS_ALPHA_MANIFEST" || return 1
  section_check_run "$RS_BETA manifest applied" "$KUBECTL_BIN" apply -f "$RS_BETA_MANIFEST" || return 1
  wait_for_rs_running "$RS_ALPHA" 1 || return 1
  wait_for_rs_running "$RS_BETA" 1 || return 1
  section_check_run "$SVC_ALPHA manifest applied" "$KUBECTL_BIN" apply -f "$SVC_ALPHA_MANIFEST" || return 1
  section_check_run "$SVC_BETA manifest applied" "$KUBECTL_BIN" apply -f "$SVC_BETA_MANIFEST" || return 1
  wait_for_service_endpoints "$SVC_ALPHA" 1 || return 1
  wait_for_service_endpoints "$SVC_BETA" 1 || return 1
  section_check_run "$DNS_NAME manifest applied" "$KUBECTL_BIN" apply -f "$DNS_MANIFEST" || return 1
  wait_for_dns_sync || return 1
}

section_dns_object_sync() {
  section_begin "06.1 dns object and sync acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR manifests=$MANIFEST_DIR dns_dir=$DNS_DIR host=$DNS_HOST"
  preflight &&
    cleanup_all &&
    apply_backends_services_dns &&
    section_check_run "$DNS_NAME get output" "$KUBECTL_BIN" get dns "$DNS_NAME" &&
    section_check_run "$DNS_NAME describe output shows host and paths" "$KUBECTL_BIN" describe dns "$DNS_NAME" &&
    check_host_dns_query
  cleanup_all
  section_end "06.1 cleaned DNS object and sync resources"
}

section_host_ingress() {
  section_begin "06.2 host ingress path routing acceptance"
  preflight &&
    cleanup_all &&
    apply_backends_services_dns &&
    wait_for_host_http "/alpha" "route=alpha" &&
    wait_for_host_http "/beta" "route=beta" &&
    section_check_run "$DNS_NAME route snapshot" bash -c 'cat "$1"' _ "$ROUTES_PATH"
  cleanup_all
  section_end "06.2 cleaned host ingress DNS resources"
}

section_pod_dns_delete() {
  section_begin "06.3 pod dns resolution and delete acceptance"
  preflight &&
    cleanup_all &&
    apply_backends_services_dns &&
    section_check_run "$CLIENT_POD manifest applied after DNS addon is active" "$KUBECTL_BIN" apply -f "$CLIENT_MANIFEST" &&
    wait_for_pod_running "$CLIENT_POD" &&
    check_client_resolver
  if [ "$SECTION_STATUS" = "PASS" ]; then
    wait_for_pod_http "/alpha" "route=alpha" &&
      wait_for_pod_http "/beta" "route=beta" &&
      pass "client Pod resolved $DNS_HOST through cluster DNS Service and reached both paths"
    section_check_run "$DNS_NAME deleted" "$KUBECTL_BIN" delete dns "$DNS_NAME" &&
      wait_for_dns_delete_sync
    if curl --connect-timeout "$HTTP_TIMEOUT_SECONDS" --max-time "$HTTP_TIMEOUT_SECONDS" -fsS -H "Host: $DNS_HOST" "$INGRESS_URL/alpha" >/tmp/minik8s-acceptance-06-http.out 2>/dev/null; then
      section_fail "host ingress still served $DNS_HOST after DNS delete"
    else
      pass "host ingress no longer serves $DNS_HOST after DNS delete"
    fi
  fi
  cleanup_all
  section_end "06.3 cleaned Pod DNS and delete resources"
}

main() {
  if [ "${1:-}" = "cleanup" ]; then
    begin "06 dns forwarding acceptance cleanup"
    cleanup_all
    cleanup "06 cleanup completed"
    end
    return 0
  fi

  section_dns_object_sync
  section_host_ingress
  section_pod_dns_delete
  printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  if [ "$SECTION_PASS_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
