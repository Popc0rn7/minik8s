#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="${repo_root}/scripts/deploy-from-controller.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

workers_file="${tmpdir}/workers"
cat >"${workers_file}" <<'WORKERS'
# controller-local worker list

worker@10.0.0.2
  node-2
WORKERS

output="$(MINIK8S_WORKERS_FILE="${workers_file}" bash "${script}" --print-workers)"
expected=$'worker@10.0.0.2\nnode-2'
if [[ "${output}" != "${expected}" ]]; then
	echo "unexpected worker parsing output" >&2
	echo "expected:" >&2
	printf '%s\n' "${expected}" >&2
	echo "actual:" >&2
	printf '%s\n' "${output}" >&2
	exit 1
fi

missing_output="$(MINIK8S_WORKERS_FILE="${tmpdir}/missing" bash "${script}" --print-workers 2>&1 >/dev/null || true)"
if [[ "${missing_output}" != *"workers file not found"* ]]; then
	echo "missing workers file did not produce a clear error" >&2
	printf '%s\n' "${missing_output}" >&2
	exit 1
fi
