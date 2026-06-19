#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/acceptance/lib/common.sh
source "$ROOT/scripts/acceptance/lib/common.sh"

ACCEPTANCE_CLEANUP_ON_FAIL="08_image_pinning only scans files and has no resources to clean up"

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
else
  pass "bundled manifests and image build defaults do not use latest tags"
fi

step "scan for known floating shorthand tags"
floating_scan_paths=("$manifest_dir")
if [ -d docs/testcase ]; then
  floating_scan_paths+=(docs/testcase)
fi
if run rg -n 'imageTag:[[:space:]]*alpine($|[[:space:]]|")' "${floating_scan_paths[@]}"; then
  fail "nginx alpine shorthand must be pinned to a stable version"
else
  pass "nginx alpine shorthand tags are not present"
fi
if run rg -n '(python:3\.11\.9|ImageTag:[[:space:]]*"3\.11\.9)' internal docs demo "${floating_scan_paths[@]}"; then
  fail "serverless Python runtime must not use patch-level 3.11.9 tags"
else
  pass "serverless Python runtime image uses the agreed python:3.11-slim tag"
fi

step "verify required final image table exists"
if [ -f docs/acceptance/images.md ]; then
  check_run "image table lists pinned mooring CNI image" rg -n 'ghcr.io/popc0rn7/mooring-cni:v0.1.0' docs/acceptance/images.md
  check_run "image table lists pinned GPU submitter image" rg -n 'ghcr.io/popc0rn7/gpu-submitter:v0.1.0' docs/acceptance/images.md
  check_run "image table lists pinned nginx image" rg -n 'nginx:1.27-alpine' docs/acceptance/images.md
  check_run "image table lists pinned busybox image" rg -n 'busybox:1.36' docs/acceptance/images.md
  check_run "image table lists pinned HPA stress image" rg -n 'polinux/stress:1.0.4' docs/acceptance/images.md
  check_run "image table lists NATS major image" rg -n 'nats:2' docs/acceptance/images.md
  check_run "image table lists Python 3.11 slim runtime image" rg -n 'python:3.11-slim' docs/acceptance/images.md
else
  mark_limited "docs/acceptance/images.md is not present in this install layout; skipping image table check"
fi

cleanup "08_image_pinning only scans files and has no resources to clean up"

end
