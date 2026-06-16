#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/acceptance/lib/common.sh
source "$ROOT/scripts/acceptance/lib/common.sh"

cd "$ROOT"

need_cmd() {
  local name="$1"
  local required="${2:-required}"
  if run command -v "$name"; then
    return 0
  fi
  if [ "$required" = "required" ]; then
    fail "required command missing: $name"
  fi
  mark_limited "optional environment command missing: $name"
  return 1
}

begin "env-check acceptance"

step "host and repository metadata"
run uname -a || fail "cannot read OS information"
run id -u || fail "cannot read user id"
run git branch --show-current || mark_limited "git branch unavailable"
run git rev-parse HEAD || mark_limited "git commit unavailable"
run git status --short || mark_limited "git status unavailable"

step "required build tools"
need_cmd go optional || true
need_cmd make optional || true
if command -v go >/dev/null 2>&1; then
  run go version || mark_limited "go is installed but not runnable"
fi

step "container runtime"
need_cmd docker required
if ! run docker version; then
  fail "Docker is required for runnable Minik8s acceptance"
fi

step "network and data-plane tools"
need_cmd ip optional || true
need_cmd bridge optional || true
need_cmd iptables optional || true
need_cmd nsenter optional || true
need_cmd curl optional || true

if [ "$(id -u)" != "0" ]; then
  mark_limited "CNI, VXLAN, iptables, and NodePort data-plane checks usually require root"
fi

step "installed layout"
run test -x ./bin/minik8s || fail "./bin/minik8s is required"
run test -x ./bin/kubectl || fail "./bin/kubectl is required"
run test -d ./scripts/acceptance || fail "./scripts/acceptance is required"
run test -d ./manifests || fail "./manifests is required"
run test -d ./state || mark_limited "./state is missing; first runtime command should create it or deploy-prod should sync it"
run test -d ./static-pods || mark_limited "./static-pods is missing; minik8s init should create static pod manifests here"
run test -d ./dns || mark_limited "./dns is missing; DNS addon init should create it"

step "binary smoke checks"
run ./bin/minik8s --help || fail "./bin/minik8s is not runnable"
run ./bin/kubectl --help || fail "./bin/kubectl is not runnable"

step "optional control-plane connectivity"
if ! run ./bin/kubectl version; then
  mark_limited "Harbor API is not reachable; start ./bin/minik8s bridge before feature scripts that require a live control plane"
fi

step "known verification boundary"
mark_partial "do not claim full go test ./... until the known Docker dependency resolution risk is fixed; use the documented package subset instead"

end
