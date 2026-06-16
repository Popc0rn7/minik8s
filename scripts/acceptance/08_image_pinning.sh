#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/acceptance/lib/common.sh
source "$ROOT/scripts/acceptance/lib/common.sh"

cd "$ROOT"

begin "image-pinning acceptance"

step "scan bundled manifests and image build defaults"
manifest_dir="manifest"
if [ -d manifests ]; then
  manifest_dir="manifests"
fi

scan_paths=(
  Makefile
  scripts/build-mooring-cni-image.sh
  scripts/push-mooring-cni-image.sh
  scripts/build-gpu-submitter-image.sh
  scripts/push-gpu-submitter-image.sh
  scripts/deploy-prod.sh
  "$manifest_dir"
  internal/bridge/bootstrap
  internal/bridge/captain/job_controller.go
  internal/bridge/serverless/workload.go
  internal/cli/cli_test.go
  docs/testcase/cni.md
  docs/testcase/job-gpu.md
  docs/testcase/pod.md
)

existing_scan_paths=()
for path in "${scan_paths[@]}"; do
  if [ -e "$path" ]; then
    existing_scan_paths+=("$path")
  fi
done

if run rg -n '(:latest|imageTag:[[:space:]]*latest|IMAGE_TAG[}?":= -]*latest|gpu-submitter:latest|mooring-cni:latest)' "${existing_scan_paths[@]}"; then
  fail "bundled images must not use latest"
fi

step "scan for known floating shorthand tags"
floating_scan_paths=("$manifest_dir")
if [ -d docs/testcase ]; then
  floating_scan_paths+=(docs/testcase)
fi
if run rg -n 'imageTag:[[:space:]]*alpine($|[[:space:]]|")' "${floating_scan_paths[@]}"; then
  fail "nginx alpine shorthand must be pinned to a stable version"
fi

step "verify required final image table exists"
if [ -f docs/acceptance/images.md ]; then
  run rg -n 'ghcr.io/popc0rn7/mooring-cni:v0.1.0' docs/acceptance/images.md || fail "image table must list pinned mooring CNI image"
  run rg -n 'ghcr.io/popc0rn7/gpu-submitter:v0.1.0' docs/acceptance/images.md || fail "image table must list pinned GPU submitter image"
  run rg -n 'nginx:1.27-alpine' docs/acceptance/images.md || fail "image table must list pinned nginx image"
  run rg -n 'busybox:1.36' docs/acceptance/images.md || fail "image table must list pinned busybox image"
else
  mark_limited "docs/acceptance/images.md is not present in this install layout; skipping image table check"
fi

end
