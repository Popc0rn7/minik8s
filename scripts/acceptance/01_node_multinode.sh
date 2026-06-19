#!/usr/bin/env bash
set -euo pipefail

role_from_args() {
  local role="${1:-${MINIK8S_ACCEPTANCE_ROLE:-}}"
  case "$role" in
    "") printf 'check\n' ;;
    bridge|sailer|cni-clean|stop|clean)
      printf '%s\n' "$role"
      ;;
    *)
      printf 'role must be bridge, sailer, cni-clean, stop, or clean; got %s\n' "${role:-<empty>}" >&2
      return 2
      ;;
  esac
}

node_name_for_role() {
  local role="$1"
  local requested_node="${2:-}"
  case "$role" in
    bridge) printf '%s\n' "${MINIK8S_NODE_A_NAME:-node-a}" ;;
    sailer)
      if [ -z "$requested_node" ]; then
        printf 'sailer role requires node name: node-a, node-b, or node-c\n' >&2
        return 2
      fi
      case "$requested_node" in
        "${MINIK8S_NODE_A_NAME:-node-a}"|"${MINIK8S_NODE_B_NAME:-node-b}"|"${MINIK8S_NODE_C_NAME:-node-c}")
          printf '%s\n' "$requested_node"
          ;;
        *)
          printf 'sailer node must be %s, %s, or %s; got %s\n' "${MINIK8S_NODE_A_NAME:-node-a}" "${MINIK8S_NODE_B_NAME:-node-b}" "${MINIK8S_NODE_C_NAME:-node-c}" "$requested_node" >&2
          return 2
          ;;
      esac
      ;;
    *) printf 'node name is only defined for bridge or sailer role\n' >&2; return 2 ;;
  esac
}

node_ip_for_name() {
  case "$1" in
    "${MINIK8S_NODE_A_NAME:-node-a}") printf '%s\n' "${MINIK8S_NODE_A_IP:?MINIK8S_NODE_A_IP is required}" ;;
    "${MINIK8S_NODE_B_NAME:-node-b}") printf '%s\n' "${MINIK8S_NODE_B_IP:?MINIK8S_NODE_B_IP is required}" ;;
    "${MINIK8S_NODE_C_NAME:-node-c}") printf '%s\n' "${MINIK8S_NODE_C_IP:?MINIK8S_NODE_C_IP is required}" ;;
    *) printf 'unknown acceptance node %s\n' "$1" >&2; return 2 ;;
  esac
}

unit_name_for_role() {
  case "$1" in
    bridge) printf 'minik8s-bridge.service\n' ;;
    sailer) printf 'minik8s-sailer.service\n' ;;
    *) printf 'unit name is only defined for bridge or sailer role\n' >&2; return 2 ;;
  esac
}

unit_template_for_role() {
  local root="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
  case "$1" in
    bridge) printf '%s\n' "$root/scripts/acceptance/services/minik8s-bridge.service" ;;
    sailer) printf '%s\n' "$root/scripts/acceptance/services/minik8s-sailer.service" ;;
    *) printf 'unit template is only defined for bridge or sailer role\n' >&2; return 2 ;;
  esac
}

sailer_state_path() {
  printf '%s\n' "${MINIK8S_STATE_DIR:-/opt/minik8s/state}/sailer.json"
}

sailer_config_node_name() {
  sed -n 's/.*"nodeName"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$1" | head -n 1
}

sailer_config_apiserver() {
  sed -n 's/.*"apiserver"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$1" | head -n 1
}

remote_node_url() {
  printf '%s/api/v1/nodes/%s\n' "${MINIK8S_HARBOR%/}" "$1"
}

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/01_node_multinode.sh
  bash scripts/acceptance/01_node_multinode.sh bridge
  bash scripts/acceptance/01_node_multinode.sh sailer node-a
  bash scripts/acceptance/01_node_multinode.sh sailer node-b
  bash scripts/acceptance/01_node_multinode.sh sailer node-c
  bash scripts/acceptance/01_node_multinode.sh cni-clean
  bash scripts/acceptance/01_node_multinode.sh stop
  bash scripts/acceptance/01_node_multinode.sh clean

Run on every target machine from /opt/minik8s:
  node-a: bash scripts/acceptance/01_node_multinode.sh bridge
  node-a: bash scripts/acceptance/01_node_multinode.sh sailer node-a
  node-b: bash scripts/acceptance/01_node_multinode.sh sailer node-b
  node-c: bash scripts/acceptance/01_node_multinode.sh sailer node-c

The bridge role starts only the control plane. Run sailer node-a separately so
node-a also participates as a worker.

The bridge role also applies the mooring CNI compatibility manifest after
Harbor is ready. The cni-clean role removes local mooring network state and
best-effort deletes the CNI compatibility objects from Harbor.

Run with no arguments on node-a after node-a/node-b/node-c have started to
verify the final multinode Node acceptance requirements.
EOF
}

if [ "${MINIK8S_ACCEPTANCE_LIBRARY:-}" = "1" ]; then
  return 0
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/acceptance/env.sh
source "$ROOT/scripts/acceptance/env.sh"
# shellcheck source=scripts/acceptance/lib/common.sh
source "$ROOT/scripts/acceptance/lib/common.sh"
# shellcheck source=scripts/acceptance/lib/systemd.sh
source "$ROOT/scripts/acceptance/lib/systemd.sh"

REMOTE_DIR="${MINIK8S_REMOTE_DIR:-/opt/minik8s}"
MINIK8S_BIN="${MINIK8S_BIN:-$REMOTE_DIR/bin/minik8s}"
KUBECTL_BIN="${KUBECTL_BIN:-$REMOTE_DIR/bin/kubectl}"
ACCEPTANCE_CLEANUP_ON_FAIL="inspect services with: systemctl status minik8s-bridge.service minik8s-sailer.service; stop services with: bash scripts/acceptance/01_node_multinode.sh stop"
HARBOR_READY_ATTEMPTS="${MINIK8S_HARBOR_READY_ATTEMPTS:-15}"
HARBOR_READY_SLEEP_SECONDS="${MINIK8S_HARBOR_READY_SLEEP_SECONDS:-2}"

cni_manifest_path() {
  if [ -f "$REMOTE_DIR/manifests/cni/mooring.yaml" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/cni/mooring.yaml"
    return 0
  fi
  printf '%s\n' "$ROOT/manifests/cni/mooring.yaml"
}

CNI_MANIFEST="$(cni_manifest_path)"

ensure_dirs() {
  output "systemd unit dir=${SYSTEMD_UNIT_DIR:-/etc/systemd/system}"
}

start_bridge() {
  step "start bridge control plane"
  check_run "bootstrap token configured for sailer join" "$MINIK8S_BIN" bridge token set "$MINIK8S_TOKEN" --ttl 24h
  install_unit "$(unit_template_for_role bridge)" "$(unit_name_for_role bridge)"
  restart_unit "$(unit_name_for_role bridge)"
  journal_unit "$(unit_name_for_role bridge)"
  wait_harbor_ready
  apply_cni_manifest
  verify_cni_endpoints
}

join_sailer() {
  local node_name="$1"
  local node_ip="$2"
  step "join sailer node $node_name"
  check_run "sailer join writes local node token" "$MINIK8S_BIN" sailer join --apiserver "$MINIK8S_HARBOR" --token "$MINIK8S_TOKEN" --node-name "$node_name" --node-ip "$node_ip"
}

remote_node_status() {
  local node_name="$1"
  local node_url status_code
  node_url="$(remote_node_url "$node_name")"
  if run curl -fsS "$node_url"; then
    REMOTE_NODE_STATUS=200
    return 0
  fi
  status_code="$(curl -sS -o /dev/null -w '%{http_code}' "$node_url" 2>/dev/null || true)"
  if [ -z "$status_code" ]; then
    status_code=000
  fi
  REMOTE_NODE_STATUS="$status_code"
}

delete_remote_node() {
  local node_name="$1"
  check_run "stale remote node $node_name deleted before rejoin" "$KUBECTL_BIN" delete node "$node_name"
}

ensure_sailer_joined() {
  local node_name="$1"
  local node_ip="$2"
  local state_path
  state_path="$(sailer_state_path)"

  if [ ! -f "$state_path" ]; then
    remote_node_status "$node_name"
    case "$REMOTE_NODE_STATUS" in
      200)
        output "local sailer state is missing but Harbor still has node $node_name; remote node will be replaced"
        delete_remote_node "$node_name"
        ;;
      404)
        output "local sailer state is missing and Harbor has no node $node_name"
        ;;
      *)
        fail "unexpected Harbor status $REMOTE_NODE_STATUS while checking unjoined sailer node $node_name"
        ;;
    esac
    join_sailer "$node_name" "$node_ip"
    return 0
  fi

  local joined_node joined_apiserver expected_apiserver
  joined_node="$(sailer_config_node_name "$state_path")"
  joined_apiserver="$(sailer_config_apiserver "$state_path")"
  expected_apiserver="${MINIK8S_HARBOR%/}"

  if [ "$joined_node" != "$node_name" ]; then
    fail "local sailer state belongs to node=${joined_node:-<unknown>}, expected node=$node_name; run clean before reusing this host as another node"
  fi
  if [ "$joined_apiserver" != "$expected_apiserver" ]; then
    fail "local sailer state uses apiserver=${joined_apiserver:-<unknown>}, expected $expected_apiserver; run clean before switching bridge endpoint"
  fi

  local status_code
  remote_node_status "$node_name"
  status_code="$REMOTE_NODE_STATUS"
  if [ "$status_code" = "200" ]; then
    pass "sailer already joined as $node_name"
    return 0
  fi
  case "$status_code" in
    404)
      output "remote node $node_name is missing from Harbor; local join state will be refreshed"
      check_run "stale local sailer state removed" rm -f "$state_path" "$REMOTE_DIR/config.json"
      join_sailer "$node_name" "$node_ip"
      return 0
      ;;
    *)
      fail "unexpected Harbor status $status_code while checking joined sailer node $node_name"
      ;;
  esac
}

retry_run() {
  local message="$1"
  local attempts="$2"
  local sleep_seconds="$3"
  shift 3
  local attempt=1
  while [ "$attempt" -le "$attempts" ]; do
    if run "$@"; then
      pass "$message"
      return 0
    fi
    if [ "$attempt" -lt "$attempts" ]; then
      output "retry $attempt/$attempts failed; sleeping ${sleep_seconds}s before next attempt"
      sleep "$sleep_seconds"
    fi
    attempt=$((attempt + 1))
  done
  return 1
}

wait_harbor_ready() {
  step "wait for Harbor API readiness"
  if retry_run "Harbor API is reachable at $MINIK8S_HARBOR" "$HARBOR_READY_ATTEMPTS" "$HARBOR_READY_SLEEP_SECONDS" curl -fsS "$MINIK8S_HARBOR/api/v1"; then
    return 0
  fi
  status_unit "$(unit_name_for_role bridge)"
  journal_unit "$(unit_name_for_role bridge)"
  fail "Harbor API is reachable at $MINIK8S_HARBOR"
}

http_status() {
  curl -sS -o /dev/null -w '%{http_code}' "$1" 2>/dev/null || true
}

harbor_ready_quiet() {
  [ "$(http_status "$MINIK8S_HARBOR/api/v1")" = "200" ]
}

cni_configmap_url() {
  local namespace="$1"
  local name="$2"
  printf '%s/api/v1/namespaces/%s/configmaps/%s\n' "${MINIK8S_HARBOR%/}" "$namespace" "$name"
}

cni_daemonset_url() {
  local namespace="$1"
  local name="$2"
  printf '%s/apis/apps/v1/namespaces/%s/daemonsets/%s\n' "${MINIK8S_HARBOR%/}" "$namespace" "$name"
}

verify_endpoint_200() {
  local message="$1"
  local url="$2"
  check_run "$message" bash -c 'test "$(curl -sS -o /dev/null -w "%{http_code}" "$1")" = "200"' _ "$url"
}

apply_cni_manifest() {
  step "apply mooring CNI compatibility manifest"
  check_run "mooring CNI manifest exists" test -f "$CNI_MANIFEST"
  check_run "mooring CNI manifest applied" "$KUBECTL_BIN" apply -f "$CNI_MANIFEST"
}

verify_cni_endpoints() {
  step "verify mooring CNI compatibility endpoints"
  verify_endpoint_200 "mooring ConfigMap endpoint is present" "$(cni_configmap_url kube-mooring mooring-cni-cfg)"
  verify_endpoint_200 "mooring DaemonSet endpoint is present" "$(cni_daemonset_url kube-mooring mooring-cni-ds)"
}

delete_compat_object_if_present() {
  local kind="$1"
  local namespace="$2"
  local name="$3"
  local url="$4"
  local status
  status="$(http_status "$url")"
  case "$status" in
    200)
      check_run "$kind/$name deleted from $namespace" "$KUBECTL_BIN" delete "$kind" "$name" -n "$namespace"
      ;;
    404)
      pass "$kind/$name already absent from $namespace"
      ;;
    000)
      mark_limited "Harbor is unreachable; skipped deleting $kind/$name from $namespace"
      ;;
    *)
      mark_limited "unexpected Harbor status $status; skipped deleting $kind/$name from $namespace"
      ;;
  esac
}

clean_cni_control_plane_objects() {
  step "clean CNI compatibility objects from Harbor"
  if ! harbor_ready_quiet; then
    mark_limited "Harbor API is not reachable; skipped CNI compatibility object deletion"
    return 0
  fi
  delete_compat_object_if_present configmap kube-mooring mooring-cni-cfg "$(cni_configmap_url kube-mooring mooring-cni-cfg)"
  delete_compat_object_if_present daemonset kube-mooring mooring-cni-ds "$(cni_daemonset_url kube-mooring mooring-cni-ds)"
  delete_compat_object_if_present configmap kube-flannel kube-flannel-cfg "$(cni_configmap_url kube-flannel kube-flannel-cfg)"
  delete_compat_object_if_present daemonset kube-flannel kube-flannel-ds "$(cni_daemonset_url kube-flannel kube-flannel-ds)"
}

clean_local_cni_state() {
  step "clean local CNI network state"
  if run "$MINIK8S_BIN" doctor clean; then
    pass "local mooring network state cleaned"
  else
    mark_limited "local mooring network cleanup reported a non-fatal failure"
  fi
  run rm -f "$MINIK8S_CNI_BIN_DIR/mooring" || true
  pass "local mooring plugin file removed if present"
}

clean_cni() {
  clean_cni_control_plane_objects
  clean_local_cni_state
}

start_sailer() {
  local node_name="$1"
  step "start sailer worker $node_name"
  install_unit "$(unit_template_for_role sailer)" "$(unit_name_for_role sailer)"
  restart_unit "$(unit_name_for_role sailer)"
  journal_unit "$(unit_name_for_role sailer)"
}

show_join_interface() {
  step "show CLI node join interface"
  check_run "node-a join CLI form is shown" printf '%s\n' "$MINIK8S_BIN sailer join --apiserver $MINIK8S_HARBOR --token <token> --node-name $MINIK8S_NODE_A_NAME --node-ip $MINIK8S_NODE_A_IP"
  check_run "node-b join CLI form is shown" printf '%s\n' "$MINIK8S_BIN sailer join --apiserver $MINIK8S_HARBOR --token <token> --node-name $MINIK8S_NODE_B_NAME --node-ip $MINIK8S_NODE_B_IP"
  check_run "node-c join CLI form is shown" printf '%s\n' "$MINIK8S_BIN sailer join --apiserver $MINIK8S_HARBOR --token <token> --node-name $MINIK8S_NODE_C_NAME --node-ip $MINIK8S_NODE_C_IP"
}

verify_local_sailer_state() {
  local node_name="$1"
  local state_path
  state_path="$(sailer_state_path)"
  step "verify local sailer join state"
  check_run "local sailer.json is present" test -f "$state_path"
  check_run "local sailer.json content is shown" sed -n '1,120p' "$state_path"
  local joined_node joined_apiserver
  joined_node="$(sailer_config_node_name "$state_path")"
  joined_apiserver="$(sailer_config_apiserver "$state_path")"
  if [ "$joined_node" != "$node_name" ]; then
    fail "local sailer.json nodeName=$joined_node, expected $node_name"
  fi
  if [ "$joined_apiserver" != "${MINIK8S_HARBOR%/}" ]; then
    fail "local sailer.json apiserver=$joined_apiserver, expected ${MINIK8S_HARBOR%/}"
  fi
  pass "local sailer.json matches node $node_name and Harbor $joined_apiserver"
}

verify_node_ready() {
  local node_name="$1"
  check_run "$node_name is registered and Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$node_name"
}

verify_node_describe() {
  local node_name="$1"
  check_run "$node_name can be described" "$KUBECTL_BIN" describe node "$node_name"
}

verify_multinode_deployment() {
  step "verify multinode deployment"
  verify_local_sailer_state "$MINIK8S_NODE_A_NAME"
  show_join_interface

  step "verify node-a runs control plane and worker"
  status_unit "$(unit_name_for_role bridge)"
  check_run "node-a bridge service is active" "$SYSTEMCTL_BIN" is-active --quiet "$(unit_name_for_role bridge)"
  status_unit "$(unit_name_for_role sailer)"
  check_run "node-a sailer service is active" "$SYSTEMCTL_BIN" is-active --quiet "$(unit_name_for_role sailer)"

  step "verify Node status from Harbor"
  verify_cni_endpoints
  check_run "kubectl can list nodes" "$KUBECTL_BIN" get nodes
  check_run "kubectl get nodes shows node-a, node-b, and node-c" bash -c '"$1" get nodes | grep -F "$2" >/dev/null && "$1" get nodes | grep -F "$3" >/dev/null && "$1" get nodes | grep -F "$4" >/dev/null' _ "$KUBECTL_BIN" "$MINIK8S_NODE_A_NAME" "$MINIK8S_NODE_B_NAME" "$MINIK8S_NODE_C_NAME"
  verify_node_ready "$MINIK8S_NODE_A_NAME"
  verify_node_ready "$MINIK8S_NODE_B_NAME"
  verify_node_ready "$MINIK8S_NODE_C_NAME"
  verify_node_describe "$MINIK8S_NODE_A_NAME"
  verify_node_describe "$MINIK8S_NODE_B_NAME"
  verify_node_describe "$MINIK8S_NODE_C_NAME"
  step "verify local mooring CNI files"
  check_run "local mooring CNI config exists" test -f "$MINIK8S_CNI_CONF_DIR/10-mooring.conf"
  check_run "local mooring CNI plugin exists" test -x "$MINIK8S_CNI_BIN_DIR/mooring"

  pass "three-node cluster is visible; node-a is both bridge and worker, node-b/node-c are worker nodes"
}

clean_local_state() {
  local state_dir="${MINIK8S_STATE_DIR:-$REMOTE_DIR/state}"
  step "clean local acceptance state"
  if [ -d "$state_dir" ]; then
    check_run "local Minik8s state removed except bridge dependency data" find "$state_dir" -mindepth 1 -maxdepth 1 ! -name bridge-deps -exec rm -rf {} +
  else
    pass "local Minik8s state directory does not exist"
  fi
  check_run "local CLI config removed" rm -f "$REMOTE_DIR/config.json"
}

main() {
  local requested="${1:-${MINIK8S_ACCEPTANCE_ROLE:-}}"
  local requested_node="${2:-}"
  if [ "$requested" = "-h" ] || [ "$requested" = "--help" ]; then
    usage
    exit 0
  fi

  begin "node-multinode acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR node_a=$MINIK8S_NODE_A_NAME/$MINIK8S_NODE_A_IP node_b=$MINIK8S_NODE_B_NAME/$MINIK8S_NODE_B_IP node_c=$MINIK8S_NODE_C_NAME/$MINIK8S_NODE_C_IP"
  ensure_dirs

  local role
  if ! role="$(role_from_args "$requested" 2>&1)"; then
    fail "$role"
  fi

  case "$role" in
    check)
      verify_multinode_deployment
      ;;
    bridge)
      local node_name node_ip
      if ! node_name="$(node_name_for_role bridge 2>&1)"; then
        fail "$node_name"
      fi
      if ! node_ip="$(node_ip_for_name "$node_name" 2>&1)"; then
        fail "$node_ip"
      fi
      output "node=$node_name node_ip=$node_ip role=$role"
      start_bridge
      ;;
    sailer)
      local node_name node_ip
      if ! node_name="$(node_name_for_role sailer "$requested_node" 2>&1)"; then
        fail "$node_name"
      fi
      if ! node_ip="$(node_ip_for_name "$node_name" 2>&1)"; then
        fail "$node_ip"
      fi
      output "node=$node_name node_ip=$node_ip role=$role"
      ensure_sailer_joined "$node_name" "$node_ip"
      start_sailer "$node_name"
      ;;
    cni-clean)
      clean_cni
      cleanup "local CNI state cleaned and CNI compatibility objects removed when Harbor was reachable"
      end
      return 0
      ;;
    stop)
      step "stop systemd services"
      stop_unit "$(unit_name_for_role sailer)"
      stop_unit "$(unit_name_for_role bridge)"
      cleanup "systemd units kept installed; use clean to remove unit files"
      end
      return 0
      ;;
    clean)
      clean_cni
      step "clean systemd units"
      clean_unit "$(unit_name_for_role sailer)"
      clean_unit "$(unit_name_for_role bridge)"
      clean_local_state
      cleanup "systemd unit files, local CNI state, and local runtime state removed; bridge dependency data and etcd node state left intact"
      end
      return 0
      ;;
  esac

  cleanup "systemd services intentionally remain running for later acceptance scripts; use 01_node_multinode.sh stop to stop them"
  end
}

main "$@"
