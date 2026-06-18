#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/acceptance/env.sh
source "$ROOT/scripts/acceptance/env.sh"
# shellcheck source=scripts/acceptance/lib/common.sh
source "$ROOT/scripts/acceptance/lib/common.sh"

ACCEPTANCE_CLEANUP_ON_FAIL="00_env_check creates no cluster resources and has no environment cleanup to run"

REMOTE_DIR="${MINIK8S_REMOTE_DIR:-/opt/minik8s}"
REQUIRED_GO_VERSION="${MINIK8S_REQUIRED_GO_VERSION:-1.25.9}"
REQUIRED_OS_ID="${MINIK8S_REQUIRED_OS_ID:-ubuntu}"
REQUIRED_OS_VERSION="${MINIK8S_REQUIRED_OS_VERSION:-22.04}"
REQUIRED_TCP_PORTS="${MINIK8S_REQUIRED_FREE_TCP_PORTS:-$MINIK8S_DNS_PORT 80 2379 2380 4222 8080 8088 18080 30080 30082 30085}"
REQUIRED_UDP_PORTS="${MINIK8S_REQUIRED_FREE_UDP_PORTS:-$MINIK8S_DNS_PORT 4789}"

need_cmd() {
  local name="$1"
  check_run "required command available: $name" command -v "$name"
}

port_free_tcp() {
  local port
  for port in $REQUIRED_TCP_PORTS; do
    if ss -H -ltn | awk '{print $4}' | grep -Eq "(^|[.:])${port}$"; then
      printf 'tcp:%s\n' "$port"
      return 1
    fi
  done
}

port_free_udp() {
  local port
  for port in $REQUIRED_UDP_PORTS; do
    if ss -H -lun | awk '{print $4}' | grep -Eq "(^|[.:])${port}$"; then
      printf 'udp:%s\n' "$port"
      return 1
    fi
  done
}

begin "env-check acceptance"

step "local target"
output "install_root=$REMOTE_DIR node_a=$MINIK8S_NODE_A_IP node_b=$MINIK8S_NODE_B_IP node_c=$MINIK8S_NODE_C_IP dns_port=$MINIK8S_DNS_PORT required_os=$REQUIRED_OS_ID-$REQUIRED_OS_VERSION required_go=$REQUIRED_GO_VERSION"

step "host metadata"
check_run "kernel and host metadata readable" uname -a
check_run "user id readable" id -u
check_run "acceptance scripts run as root" bash -lc '[ "$(id -u)" = 0 ]'
check_run "target machine runs Ubuntu 22.04" bash -lc ". /etc/os-release && test \"\$ID\" = '$REQUIRED_OS_ID' && test \"\$VERSION_ID\" = '$REQUIRED_OS_VERSION'"
output "os=$(bash -lc '. /etc/os-release && printf "%s-%s" "$ID" "$VERSION_ID"') kernel=$(uname -r) arch=$(uname -m)"

step "required runtime commands"
need_cmd docker
need_cmd go
need_cmd make
need_cmd ip
need_cmd bridge
need_cmd iptables
need_cmd nsenter
need_cmd curl
need_cmd ping
need_cmd ss
need_cmd systemctl
need_cmd journalctl

step "go toolchain"
check_run "go is installed and runnable" go version
check_run "target machine uses Go $REQUIRED_GO_VERSION" bash -lc "go version | grep -F 'go$REQUIRED_GO_VERSION '"

step "docker runtime"
check_run "Docker is usable by root" docker version
check_run "Docker daemon is healthy" docker info --format '{{.ServerVersion}}'

step "port availability"
check_run "required TCP acceptance ports are free: $REQUIRED_TCP_PORTS" bash -lc "$(declare -f port_free_tcp); REQUIRED_TCP_PORTS='$REQUIRED_TCP_PORTS'; port_free_tcp"
check_run "required UDP acceptance ports are free: $REQUIRED_UDP_PORTS" bash -lc "$(declare -f port_free_udp); REQUIRED_UDP_PORTS='$REQUIRED_UDP_PORTS'; port_free_udp"

step "locked install layout"
check_run "$REMOTE_DIR exists" test -d "$REMOTE_DIR"
check_run "$REMOTE_DIR/bin/minik8s is executable" test -x "$REMOTE_DIR/bin/minik8s"
check_run "$REMOTE_DIR/bin/kubectl is executable" test -x "$REMOTE_DIR/bin/kubectl"
check_run "minik8s binary is not duplicated at $REMOTE_DIR/minik8s" test ! -e "$REMOTE_DIR/minik8s"
check_run "kubectl binary is not duplicated at $REMOTE_DIR/kubectl" test ! -e "$REMOTE_DIR/kubectl"
check_run "$REMOTE_DIR/scripts/acceptance exists" test -d "$REMOTE_DIR/scripts/acceptance"
check_run "$REMOTE_DIR/manifests exists" test -d "$REMOTE_DIR/manifests"
if run test -d "$REMOTE_DIR/demo/serverless/harbor-incident-triage"; then
  pass "$REMOTE_DIR/demo/serverless/harbor-incident-triage exists"
else
  mark_limited "$REMOTE_DIR/demo/serverless/harbor-incident-triage is missing; triage demo is not available"
fi
check_run "$REMOTE_DIR/state exists" test -d "$REMOTE_DIR/state"
check_run "$REMOTE_DIR/static-pods exists" test -d "$REMOTE_DIR/static-pods"
check_run "$REMOTE_DIR/dns exists" test -d "$REMOTE_DIR/dns"
if run test -d "$REMOTE_DIR/secrets/gpu-ssh"; then
  pass "$REMOTE_DIR/secrets/gpu-ssh exists"
else
  mark_limited "$REMOTE_DIR/secrets/gpu-ssh is missing; GPU Job SSH credentials are not staged"
fi

step "CNI host paths"
check_run "$MINIK8S_CNI_CONF_DIR exists" test -d "$MINIK8S_CNI_CONF_DIR"
check_run "$MINIK8S_CNI_BIN_DIR exists" test -d "$MINIK8S_CNI_BIN_DIR"

step "binary smoke checks"
check_run "$REMOTE_DIR/bin/minik8s is runnable" "$REMOTE_DIR/bin/minik8s" --help
check_run "$REMOTE_DIR/bin/kubectl is runnable" "$REMOTE_DIR/bin/kubectl" --help

step "three-node network connectivity"
check_run "node-a $MINIK8S_NODE_A_IP is reachable by ping" ping -c 1 -W 2 "$MINIK8S_NODE_A_IP"
check_run "node-b $MINIK8S_NODE_B_IP is reachable by ping" ping -c 1 -W 2 "$MINIK8S_NODE_B_IP"
check_run "node-c $MINIK8S_NODE_C_IP is reachable by ping" ping -c 1 -W 2 "$MINIK8S_NODE_C_IP"
check_run "route to node-a $MINIK8S_NODE_A_IP exists" ip route get "$MINIK8S_NODE_A_IP"
check_run "route to node-b $MINIK8S_NODE_B_IP exists" ip route get "$MINIK8S_NODE_B_IP"
check_run "route to node-c $MINIK8S_NODE_C_IP exists" ip route get "$MINIK8S_NODE_C_IP"

cleanup "00_env_check creates no cluster resources and has no environment cleanup to run"

end
