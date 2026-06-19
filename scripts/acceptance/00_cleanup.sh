#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/00_cleanup.sh
  bash scripts/acceptance/00_cleanup.sh --all
  bash scripts/acceptance/00_cleanup.sh --local-only
  bash scripts/acceptance/00_cleanup.sh --help

Reset Minik8s acceptance resources before rerunning the acceptance flow from
00_env_check.sh.

Default mode deletes known resources created by scripts/acceptance/*.sh and
documented serverless demo testcases, removes local Minik8s systemd services,
and clears local runtime data directories including the bridge dependency etcd
data. Re-run 01_node_multinode.sh after this cleanup to recreate services.

Options:
  --all         Delete all supported non-Node resources from the current
                namespace after deleting known acceptance resources.
  --local-only  Do not contact Harbor; only remove local services, containers,
                runtime data directories, and temporary files on this machine.
EOF
}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/acceptance/env.sh
source "$ROOT/scripts/acceptance/env.sh"
# shellcheck source=scripts/acceptance/lib/common.sh
source "$ROOT/scripts/acceptance/lib/common.sh"

REMOTE_DIR="${MINIK8S_REMOTE_DIR:-/opt/minik8s}"
KUBECTL_BIN="${KUBECTL_BIN:-$REMOTE_DIR/bin/kubectl}"
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_CLEANUP_WAIT_ATTEMPTS:-20}"
WAIT_SLEEP_SECONDS="${MINIK8S_ACCEPTANCE_WAIT_SLEEP_SECONDS:-2}"
STATE_DIR="${MINIK8S_STATE_DIR:-$REMOTE_DIR/state}"
DNS_DIR="${MINIK8S_DNS_DIR:-$REMOTE_DIR/dns}"
STATIC_POD_DIR="${MINIK8S_STATIC_POD_DIR:-$REMOTE_DIR/static-pods}"

DELETE_ALL=0
LOCAL_ONLY=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --all)
      DELETE_ALL=1
      ;;
    --local-only)
      LOCAL_ONLY=1
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'unknown argument: %s\n\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

command_line() {
  local arg
  for arg in "$@"; do
    printf '%q ' "$arg"
  done | sed 's/[[:space:]]$//'
}

quiet_delete() {
  local resource="$1"
  local name="$2"
  local out code

  if ! "$KUBECTL_BIN" get "$resource" "$name" >/dev/null 2>&1; then
    printf '[OUTPUT]\n%s/%s already absent\n' "$resource" "$name"
    return 0
  fi

  printf '[RUN] %s\n' "$(command_line "$KUBECTL_BIN" delete "$resource" "$name")"
  set +e
  out="$("$KUBECTL_BIN" delete "$resource" "$name" 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  if [ "$code" -eq 0 ]; then
    pass "$resource/$name deleted"
    return 0
  fi
  mark_limited "failed to delete $resource/$name; continuing cleanup"
  return 0
}

delete_known_resources() {
  local item resource name
  local resources=(
    "eventtrigger image-uploaded"
    "eventtrigger text-branch-events"
    "eventtrigger echo-events"
    "workflow make-ranking"
    "workflow process-one-image"
    "workflow text-branch"
    "workflow workflow-echo"
    "function make-collage"
    "function score-mask"
    "function sam-segment"
    "function extract-metadata"
    "function compose-report"
    "function route"
    "function answer"
    "function summary"
    "function upper"
    "function slow-echo"
    "function echo"
    "job cuda-matmul-tiled"
    "job cuda-add-2"
    "job cuda-add"
    "hpa hpa-05-web"
    "dns dns-06-routes"
    "service artifact-store"
    "service pod-07-bare"
    "service rs-07-web"
    "service service-06-beta"
    "service service-06-alpha"
    "service rs-05-hpa"
    "service rs-04-web"
    "service svc-03-nodeport"
    "service svc-03-clusterip"
    "replicaset rs-07-web"
    "replicaset rs-06-beta"
    "replicaset rs-06-alpha"
    "replicaset rs-05-hpa"
    "replicaset rs-04-web"
    "pod artifact-store"
    "pod pod-07-local-pending"
    "pod pod-07-bare"
    "pod pod-06-client"
    "pod svc-03-client"
    "pod svc-03-nginx-b"
    "pod svc-03-nginx-a"
    "pod pod-02-sched-3"
    "pod pod-02-sched-2"
    "pod pod-02-sched-1"
    "pod pod-02-main"
  )

  for item in "${resources[@]}"; do
    resource="${item%% *}"
    name="${item#* }"
    quiet_delete "$resource" "$name"
  done
}

names_from_table() {
  local resource="$1"
  "$KUBECTL_BIN" get "$resource" 2>/dev/null |
    awk 'NR > 1 && $1 != "" { print $1 }'
}

delete_all_resource_type() {
  local resource="$1"
  local name
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    quiet_delete "$resource" "$name"
  done < <(names_from_table "$resource")
}

delete_all_resources() {
  local resource
  for resource in workflowrun eventtrigger workflow function job hpa dns service replicaset pod configmap daemonset; do
    delete_all_resource_type "$resource"
  done
}

remove_acceptance_containers() {
  if ! command -v docker >/dev/null 2>&1; then
    mark_limited "docker is not available; skipped local container cleanup"
    return 0
  fi
  if ! docker version >/dev/null 2>&1; then
    mark_limited "docker daemon is not reachable; skipped local container cleanup"
    return 0
  fi

  local case_label ids out code
  ids=""
  for case_label in \
    minik8s-acceptance-02 \
    minik8s-acceptance-03 \
    minik8s-acceptance-04 \
    minik8s-acceptance-05 \
    minik8s-acceptance-06 \
    minik8s-acceptance-07; do
    ids="$ids
$(docker ps -aq --filter label=minik8s.kind=pod-container --filter label=case="$case_label" 2>/dev/null || true)"
  done
  ids="$(printf '%s\n' "$ids" | sed '/^$/d' | sort -u)"

  if [ -z "$ids" ]; then
    printf '[OUTPUT]\nno local acceptance containers found by case label\n'
    pass "local acceptance containers already absent"
    return 0
  fi

  printf '[RUN] docker rm -f <acceptance-container-ids>\n'
  set +e
  # shellcheck disable=SC2086
  out="$(docker rm -f $ids 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  if [ "$code" -eq 0 ]; then
    pass "local acceptance containers removed"
    return 0
  fi
  mark_limited "failed to remove one or more local acceptance containers"
}

remove_minik8s_containers() {
  if ! command -v docker >/dev/null 2>&1; then
    mark_limited "docker is not available; skipped Minik8s container cleanup"
    return 0
  fi
  if ! docker version >/dev/null 2>&1; then
    mark_limited "docker daemon is not reachable; skipped Minik8s container cleanup"
    return 0
  fi

  local ids out code
  ids="$(docker ps -aq --filter label=minik8s.kind 2>/dev/null | sed '/^$/d' | sort -u)"
  if [ -z "$ids" ]; then
    printf '[OUTPUT]\nno local Minik8s containers found by minik8s.kind label\n'
    pass "local Minik8s containers already absent"
    return 0
  fi

  printf '[RUN] docker rm -f <minik8s-container-ids>\n'
  set +e
  # shellcheck disable=SC2086
  out="$(docker rm -f $ids 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  if [ "$code" -eq 0 ]; then
    pass "local Minik8s containers removed"
    return 0
  fi
  mark_limited "failed to remove one or more local Minik8s containers"
}

wait_for_local_containers_gone() {
  if ! command -v docker >/dev/null 2>&1 || ! docker version >/dev/null 2>&1; then
    return 0
  fi

  local attempt case_label ids
  attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    ids=""
    for case_label in \
      minik8s-acceptance-02 \
      minik8s-acceptance-03 \
      minik8s-acceptance-04 \
      minik8s-acceptance-05 \
      minik8s-acceptance-06 \
      minik8s-acceptance-07; do
      ids="$ids
$(docker ps -aq --filter label=minik8s.kind=pod-container --filter label=case="$case_label" 2>/dev/null || true)"
    done
    ids="$(printf '%s\n' "$ids" | sed '/^$/d' | sort -u)"
    if [ -z "$ids" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  mark_limited "some local Minik8s pod containers may still remain"
}

remove_systemd_services() {
  if ! command -v systemctl >/dev/null 2>&1; then
    mark_limited "systemctl is not available; skipped Minik8s service cleanup"
    return 0
  fi

  local unit out code
  for unit in minik8s-bridge.service minik8s-sailer.service; do
    printf '[RUN] systemctl disable --now %s\n' "$unit"
    set +e
    out="$(systemctl disable --now "$unit" 2>&1)"
    code=$?
    set -e
    printf '[EXIT] %s\n' "$code"
    printf '[OUTPUT]\n%s\n' "${out:-$unit disabled and stopped}"
    if [ "$code" -eq 0 ]; then
      pass "$unit disabled and stopped"
    else
      mark_limited "failed to disable --now $unit; continuing cleanup"
    fi
  done

  printf '[RUN] rm -f /etc/systemd/system/minik8s-bridge.service /etc/systemd/system/minik8s-sailer.service\n'
  set +e
  out="$(rm -f /etc/systemd/system/minik8s-bridge.service /etc/systemd/system/minik8s-sailer.service 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "${out:-systemd unit files removed if present}"
  if [ "$code" -eq 0 ]; then
    pass "Minik8s systemd unit files removed"
  else
    mark_limited "failed to remove one or more Minik8s systemd unit files"
  fi

  printf '[RUN] systemctl daemon-reload\n'
  set +e
  out="$(systemctl daemon-reload 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "${out:-systemd daemon reloaded}"
  if [ "$code" -eq 0 ]; then
    pass "systemd daemon reloaded"
  else
    mark_limited "systemd daemon-reload failed"
  fi
}

clean_cni_network_state() {
  if [ ! -x "$REMOTE_DIR/bin/minik8s" ]; then
    mark_limited "minik8s binary is not executable; skipped CNI network cleanup"
    return 0
  fi

  local out code
  printf '[RUN] %q doctor clean\n' "$REMOTE_DIR/bin/minik8s"
  set +e
  out="$("$REMOTE_DIR/bin/minik8s" doctor clean 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  if [ "$code" -eq 0 ]; then
    pass "local CNI network state cleaned"
  else
    mark_limited "failed to clean local CNI network state"
  fi

  printf '[RUN] rm -f %q\n' "$MINIK8S_CNI_BIN_DIR/mooring"
  set +e
  out="$(rm -f "$MINIK8S_CNI_BIN_DIR/mooring" 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "${out:-mooring CNI plugin removed if present}"
  if [ "$code" -eq 0 ]; then
    pass "local mooring CNI plugin removed"
  else
    mark_limited "failed to remove local mooring CNI plugin"
  fi
}

remove_runtime_data_dirs() {
  local out code
  printf '[RUN] rm -rf %q %q %q %q\n' "$STATE_DIR" "$DNS_DIR" "$STATIC_POD_DIR" "$REMOTE_DIR/config.json"
  set +e
  out="$(rm -rf "$STATE_DIR" "$DNS_DIR" "$STATIC_POD_DIR" "$REMOTE_DIR/config.json" 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "${out:-runtime state, dns, static pod data, and CLI config removed}"
  if [ "$code" -ne 0 ]; then
    mark_limited "failed to remove one or more Minik8s runtime data paths"
    return 0
  fi

  printf '[RUN] mkdir -p %q %q %q\n' "$STATE_DIR" "$DNS_DIR" "$STATIC_POD_DIR"
  set +e
  out="$(mkdir -p "$STATE_DIR" "$DNS_DIR" "$STATIC_POD_DIR" 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "${out:-runtime data directories recreated empty}"
  if [ "$code" -eq 0 ]; then
    pass "runtime data directories reset"
  else
    mark_limited "failed to recreate one or more Minik8s runtime data directories"
    return 0
  fi

  if [ ! -x "$REMOTE_DIR/bin/minik8s" ]; then
    mark_limited "minik8s binary is not executable; skipped static pod manifest regeneration"
    return 0
  fi
  printf '[RUN] %q init --force\n' "$REMOTE_DIR/bin/minik8s"
  set +e
  out="$("$REMOTE_DIR/bin/minik8s" init --force 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  if [ "$code" -eq 0 ]; then
    pass "static pod manifests regenerated after data reset"
    return 0
  fi
  mark_limited "failed to regenerate static pod manifests after data reset"
}

remove_temp_files() {
  printf '[RUN] rm -rf /tmp/minik8s-acceptance-*\n'
  set +e
  out="$(rm -rf /tmp/minik8s-acceptance-* 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "${out:-removed matching temporary files if any}"
  if [ "$code" -eq 0 ]; then
    pass "temporary acceptance files removed"
    return 0
  fi
  mark_limited "failed to remove one or more temporary acceptance files"
}

preflight_remote() {
  if [ "$LOCAL_ONLY" -eq 1 ]; then
    return 0
  fi
  if [ ! -x "$KUBECTL_BIN" ]; then
    mark_limited "kubectl binary is not executable at $KUBECTL_BIN; cluster resource cleanup will be skipped"
    return 1
  fi
  if ! curl -fsS -o /dev/null "$MINIK8S_HARBOR/api/v1"; then
    mark_limited "Harbor API is not reachable at $MINIK8S_HARBOR; cluster resource cleanup will be skipped"
    return 1
  fi
  return 0
}

begin "acceptance cleanup"
output "root=$ROOT remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR kubectl=$KUBECTL_BIN state_dir=$STATE_DIR dns_dir=$DNS_DIR static_pod_dir=$STATIC_POD_DIR mode=$(if [ "$LOCAL_ONLY" -eq 1 ]; then printf local-only; elif [ "$DELETE_ALL" -eq 1 ]; then printf all; else printf known; fi)"

REMOTE_READY=0
if preflight_remote; then
  REMOTE_READY=1
fi

if [ "$LOCAL_ONLY" -eq 0 ] && [ "$REMOTE_READY" -eq 1 ]; then
  step "delete known acceptance and demo resources"
  delete_known_resources
  if [ "$DELETE_ALL" -eq 1 ]; then
    step "delete every supported non-Node resource in current namespace"
    delete_all_resources
  else
    step "skip broad namespace cleanup"
    output "run with --all to delete all supported non-Node resources"
    pass "broad cleanup skipped by default"
  fi
elif [ "$LOCAL_ONLY" -eq 0 ]; then
  step "skip cluster resource cleanup"
  output "kubectl/Harbor preflight failed; continuing with service, container, and data directory cleanup"
fi

step "remove local Minik8s systemd services"
remove_systemd_services

step "clean local runtime leftovers on this node"
remove_acceptance_containers
remove_minik8s_containers
wait_for_local_containers_gone
clean_cni_network_state
remove_runtime_data_dirs
remove_temp_files

cleanup "cluster resources, Minik8s services, local containers, and runtime data directories reset"
end
