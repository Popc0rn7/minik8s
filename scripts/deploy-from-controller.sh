#!/usr/bin/env bash
set -euo pipefail

workers_file="${MINIK8S_WORKERS_FILE:-/etc/minik8s/cd-workers}"

read_workers() {
	if [[ ! -f "${workers_file}" ]]; then
		echo "workers file not found: ${workers_file}" >&2
		return 1
	fi

	awk '
		{
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0)
			if ($0 != "" && $0 !~ /^#/) {
				print $0
			}
		}
	' "${workers_file}"
}

if [[ "${1:-}" == "--print-workers" ]]; then
	read_workers
	exit 0
fi

binary_path="${1:-./minik8s}"
install_script="${2:-./install-minik8s.sh}"

if [[ ! -f "${binary_path}" ]]; then
	echo "minik8s binary not found: ${binary_path}" >&2
	exit 1
fi

if [[ ! -f "${install_script}" ]]; then
	echo "install script not found: ${install_script}" >&2
	exit 1
fi

mapfile -t workers < <(read_workers)
if [[ "${#workers[@]}" -eq 0 ]]; then
	echo "workers file has no targets: ${workers_file}" >&2
	exit 1
fi

if [[ -n "${MINIK8S_WORKER_PASSWORD_FILE:-}" ]]; then
	if [[ ! -f "${MINIK8S_WORKER_PASSWORD_FILE}" ]]; then
		echo "worker password file not found: ${MINIK8S_WORKER_PASSWORD_FILE}" >&2
		exit 1
	fi
	SSHPASS="$(<"${MINIK8S_WORKER_PASSWORD_FILE}")"
	export SSHPASS
fi

install_sshpass() {
	if command -v sshpass >/dev/null 2>&1; then
		return 0
	fi
	if command -v apt-get >/dev/null 2>&1; then
		apt-get update
		apt-get install -y sshpass
	elif command -v dnf >/dev/null 2>&1; then
		dnf install -y sshpass
	elif command -v yum >/dev/null 2>&1; then
		yum install -y sshpass
	else
		echo "sshpass is required for password-based worker deployment" >&2
		return 1
	fi
}

ssh_cmd=(ssh -o StrictHostKeyChecking=accept-new)
scp_cmd=(scp -o StrictHostKeyChecking=accept-new)
if [[ -n "${SSHPASS:-}" ]]; then
	install_sshpass
	ssh_cmd=(sshpass -e ssh -o PreferredAuthentications=password -o PubkeyAuthentication=no -o StrictHostKeyChecking=accept-new)
	scp_cmd=(sshpass -e scp -o PreferredAuthentications=password -o PubkeyAuthentication=no -o StrictHostKeyChecking=accept-new)
fi

echo "installing minik8s on controller"
bash "${install_script}" "${binary_path}"

for worker in "${workers[@]}"; do
	remote_dir="/tmp/minik8s-cd-$(date +%s)-${RANDOM}"
	echo "deploying minik8s to ${worker}"
	"${ssh_cmd[@]}" "${worker}" "mkdir -p '${remote_dir}'"
	"${scp_cmd[@]}" "${binary_path}" "${worker}:${remote_dir}/minik8s"
	"${scp_cmd[@]}" "${install_script}" "${worker}:${remote_dir}/install-minik8s.sh"
	"${ssh_cmd[@]}" "${worker}" "bash '${remote_dir}/install-minik8s.sh' '${remote_dir}/minik8s'"
	"${ssh_cmd[@]}" "${worker}" "rm -rf '${remote_dir}'"
done
