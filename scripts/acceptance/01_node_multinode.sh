#!/usr/bin/env bash
set -euo pipefail

role_from_args() {
  local role="${1:-${MINIK8S_ACCEPTANCE_ROLE:-}}"
  case "$role" in
    "") printf 'check\n' ;;
    bridge|sailer|stop|clean)
      printf '%s\n' "$role"
      ;;
    *)
      printf 'role must be bridge, sailer, stop, or clean; got %s\n' "${role:-<empty>}" >&2
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
  bash scripts/acceptance/01_node_multinode.sh stop
  bash scripts/acceptance/01_node_multinode.sh clean

Run on every target machine from /opt/minik8s:
  node-a: bash scripts/acceptance/01_node_multinode.sh bridge
  node-a: bash scripts/acceptance/01_node_multinode.sh sailer node-a
  node-b: bash scripts/acceptance/01_node_multinode.sh sailer node-b
  node-c: bash scripts/acceptance/01_node_multinode.sh sailer node-c

The bridge role starts only the control plane. Run sailer node-a separately so
node-a also participates as a worker.

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
  check_run "kubectl can list nodes" "$KUBECTL_BIN" get nodes
  check_run "kubectl get nodes shows node-a, node-b, and node-c" bash -c '"$1" get nodes | grep -F "$2" >/dev/null && "$1" get nodes | grep -F "$3" >/dev/null && "$1" get nodes | grep -F "$4" >/dev/null' _ "$KUBECTL_BIN" "$MINIK8S_NODE_A_NAME" "$MINIK8S_NODE_B_NAME" "$MINIK8S_NODE_C_NAME"
  verify_node_ready "$MINIK8S_NODE_A_NAME"
  verify_node_ready "$MINIK8S_NODE_B_NAME"
  verify_node_ready "$MINIK8S_NODE_C_NAME"
  verify_node_describe "$MINIK8S_NODE_A_NAME"
  verify_node_describe "$MINIK8S_NODE_B_NAME"
  verify_node_describe "$MINIK8S_NODE_C_NAME"

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
    stop)
      step "stop systemd services"
      stop_unit "$(unit_name_for_role sailer)"
      stop_unit "$(unit_name_for_role bridge)"
      cleanup "systemd units kept installed; use clean to remove unit files"
      end
      return 0
      ;;
    clean)
      step "clean systemd units"
      clean_unit "$(unit_name_for_role sailer)"
      clean_unit "$(unit_name_for_role bridge)"
      clean_local_state
      cleanup "systemd unit files and local runtime state removed; bridge dependency data and etcd node state left intact"
      end
      return 0
      ;;
  esac

  cleanup "systemd services intentionally remain running for later acceptance scripts; use 01_node_multinode.sh stop to stop them"
  end
}

main "$@"
