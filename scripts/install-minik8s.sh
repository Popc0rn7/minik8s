#!/usr/bin/env bash
set -euo pipefail

binary_path="${1:-./minik8s}"
service_name="${MINIK8S_SERVICE_NAME:-minik8s}"
install_path="${MINIK8S_INSTALL_PATH:-/usr/local/bin/minik8s}"
state_dir="${MINIK8S_STATE_DIR:-/var/lib/minik8s/state}"
work_dir="${MINIK8S_WORK_DIR:-/var/lib/minik8s}"
service_file="/etc/systemd/system/${service_name}.service"

if [[ ! -f "${binary_path}" ]]; then
	echo "minik8s binary not found: ${binary_path}" >&2
	exit 1
fi

sudo_cmd=()
if [[ "${EUID}" -ne 0 ]]; then
	sudo_cmd=(sudo)
fi

tmp_service="$(mktemp)"
trap 'rm -f "${tmp_service}"' EXIT

cat >"${tmp_service}" <<SERVICE
[Unit]
Description=Minik8s controller
Wants=network-online.target
After=network-online.target docker.service

[Service]
Type=simple
WorkingDirectory=${work_dir}
Environment=MINIK8S_STATE_DIR=${state_dir}
ExecStart=${install_path} controller
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SERVICE

"${sudo_cmd[@]}" install -d -m 0755 "$(dirname "${install_path}")" "${work_dir}" "${state_dir}" "$(dirname "${service_file}")"
"${sudo_cmd[@]}" install -m 0755 "${binary_path}" "${install_path}"
"${sudo_cmd[@]}" install -m 0644 "${tmp_service}" "${service_file}"
"${sudo_cmd[@]}" systemctl daemon-reload
"${sudo_cmd[@]}" systemctl enable "${service_name}"
"${sudo_cmd[@]}" systemctl restart "${service_name}"
"${sudo_cmd[@]}" systemctl --no-pager --full status "${service_name}"
