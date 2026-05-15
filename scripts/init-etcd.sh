#!/usr/bin/env bash
set -euo pipefail

# Initialize a single-node etcd for Minik8s control-plane state.
#
# Usage:
#   sudo bash scripts/init-etcd.sh
#
# Optional environment variables:
#   ETCD_NAME=minik8s-kubecaptain
#   ETCD_CLIENT_HOST=127.0.0.1
#   ETCD_PEER_HOST=127.0.0.1
#   ETCD_CLIENT_PORT=2379
#   ETCD_PEER_PORT=2380
#   ETCD_DATA_DIR=/var/lib/minik8s/etcd
#   ETCD_CONFIG_FILE=/etc/etcd/minik8s-etcd.yaml
#   ETCD_SERVICE_FILE=/etc/systemd/system/minik8s-etcd.service
#   ETCD_REUSE_SYSTEM_SERVICE=1
#
# For a cloud host where kubebridge is on the same machine, keep the default
# 127.0.0.1 listener. If another machine must access etcd directly, set
# ETCD_CLIENT_HOST to the private LAN IP and restrict the cloud firewall.

require_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    echo "Please run as root, for example: sudo bash $0" >&2
    exit 1
  fi
}

install_etcd() {
  if command -v etcd >/dev/null 2>&1 && command -v etcdctl >/dev/null 2>&1; then
    return
  fi

  if command -v apt-get >/dev/null 2>&1; then
    apt-get update
    apt-get install -y etcd etcd-client
    return
  fi

  echo "etcd/etcdctl not found, and apt-get is unavailable." >&2
  echo "Install etcd manually, then re-run this script." >&2
  exit 1
}

unit_exists() {
  systemctl cat "$1" >/dev/null 2>&1
}

print_service_diagnostics() {
  local unit="$1"
  echo
  echo "${unit} diagnostics:"
  echo
  systemctl --no-pager --full status "${unit}" || true
  echo
  journalctl -u "${unit}" -n 120 --no-pager || true
}

wait_for_etcd_health() {
  local endpoint="$1"
  local unit="$2"

  echo "Waiting for etcd health at ${endpoint}..."
  for _ in $(seq 1 30); do
    if ETCDCTL_API=3 etcdctl --endpoints="${endpoint}" endpoint health >/dev/null 2>&1; then
      ETCDCTL_API=3 etcdctl --endpoints="${endpoint}" endpoint health
      return
    fi
    if command -v curl >/dev/null 2>&1 && curl -fsS "${endpoint}/health" >/dev/null 2>&1; then
      curl -fsS "${endpoint}/health"
      return
    fi
    sleep 1
  done

  echo "etcd did not become healthy at ${endpoint}." >&2
  print_service_diagnostics "${unit}"
  exit 1
}

start_and_validate_unit() {
  local unit="$1"
  local endpoint="$2"

  systemctl daemon-reload
  if ! systemctl enable --now "${unit}"; then
    echo
    echo "${unit} failed to start. Recent diagnostics:"
    print_service_diagnostics "${unit}"
    exit 1
  fi

  wait_for_etcd_health "${endpoint}" "${unit}"
  systemctl --no-pager --full status "${unit}" || true
}

main() {
  require_root

  local etcd_name="${ETCD_NAME:-minik8s-kubecaptain}"
  local client_host="${ETCD_CLIENT_HOST:-127.0.0.1}"
  local peer_host="${ETCD_PEER_HOST:-127.0.0.1}"
  local client_port="${ETCD_CLIENT_PORT:-2379}"
  local peer_port="${ETCD_PEER_PORT:-2380}"
  local data_dir="${ETCD_DATA_DIR:-/var/lib/minik8s/etcd}"
  local config_file="${ETCD_CONFIG_FILE:-/etc/etcd/minik8s-etcd.yaml}"
  local service_file="${ETCD_SERVICE_FILE:-/etc/systemd/system/minik8s-etcd.service}"
  local endpoint="http://${client_host}:${client_port}"
  local reuse_system_service="${ETCD_REUSE_SYSTEM_SERVICE:-1}"

  install_etcd

  if [[ "${reuse_system_service}" == "1" ]] && unit_exists etcd.service; then
    if unit_exists minik8s-etcd.service; then
      systemctl disable --now minik8s-etcd.service >/dev/null 2>&1 || true
    fi

    start_and_validate_unit etcd.service "${endpoint}"

    cat <<EOF

etcd.service is running and healthy.

Use this endpoint for Minik8s:
  export MINIK8S_ETCD_ENDPOINTS=${endpoint}

Useful checks:
  curl ${endpoint}/health
  ETCDCTL_API=3 etcdctl --endpoints=${endpoint} get --prefix /registry
  journalctl -u etcd -n 100 --no-pager
EOF
    return
  fi

  local etcd_bin
  etcd_bin="$(command -v etcd)"

  useradd --system --home "${data_dir}" --shell /usr/sbin/nologin etcd 2>/dev/null || true
  mkdir -p "$(dirname "${config_file}")" "${data_dir}"
  chown -R etcd:etcd "${data_dir}"

  cat >"${config_file}" <<EOF
name: ${etcd_name}
data-dir: ${data_dir}

listen-client-urls: http://${client_host}:${client_port}
advertise-client-urls: http://${client_host}:${client_port}

listen-peer-urls: http://${peer_host}:${peer_port}
initial-advertise-peer-urls: http://${peer_host}:${peer_port}
initial-cluster: ${etcd_name}=http://${peer_host}:${peer_port}
initial-cluster-token: minik8s-etcd
initial-cluster-state: new

enable-v2: false
logger: zap
log-level: info
auto-compaction-mode: periodic
auto-compaction-retention: "1"
quota-backend-bytes: 8589934592
EOF

  cat >"${service_file}" <<EOF
[Unit]
Description=Minik8s local etcd
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=etcd
Group=etcd
ExecStart=${etcd_bin} --config-file ${config_file}
Restart=always
RestartSec=5s
LimitNOFILE=40000

[Install]
WantedBy=multi-user.target
EOF

  start_and_validate_unit minik8s-etcd "${endpoint}"

  cat <<EOF

etcd initialized.

Use this endpoint for Minik8s:
  export MINIK8S_ETCD_ENDPOINTS=${endpoint}

Useful checks:
  curl ${endpoint}/health
  ETCDCTL_API=3 etcdctl --endpoints=${endpoint} get --prefix /registry
  journalctl -u minik8s-etcd -n 100 --no-pager
EOF
}

main "$@"
