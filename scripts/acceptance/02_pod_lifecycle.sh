#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/02_pod_lifecycle.sh
  bash scripts/acceptance/02_pod_lifecycle.sh cleanup
  bash scripts/acceptance/02_pod_lifecycle.sh --help

Run on node-a after 01_node_multinode.sh has started bridge and all sailers.
This script uses fixed manifests under manifests/pod/ in the deployed tree
or manifest/pod/ in the source tree. It does not create temporary manifests.

Sections:
  02.1 Pod create, start, delete, parameters, and restartCount
  02.2 Same-Pod localhost communication
  02.3 Multi-node scheduler assignment
  02.4 Same-Pod volume file sharing

Preparatory checks and cleanup are quiet unless they fail. Evidence commands
print [RUN], [EXIT], [OUTPUT], and a conclusion for TA-readable logs.
EOF
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/acceptance/env.sh
source "$ROOT/scripts/acceptance/env.sh"
# shellcheck source=scripts/acceptance/lib/common.sh
source "$ROOT/scripts/acceptance/lib/common.sh"

REMOTE_DIR="${MINIK8S_REMOTE_DIR:-/opt/minik8s}"
KUBECTL_BIN="${KUBECTL_BIN:-$REMOTE_DIR/bin/kubectl}"
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_WAIT_ATTEMPTS:-30}"
WAIT_SLEEP_SECONDS="${MINIK8S_ACCEPTANCE_WAIT_SLEEP_SECONDS:-2}"
SHARED_DIR="${MINIK8S_ACCEPTANCE_02_SHARED_DIR:-/tmp/minik8s-acceptance-02-shared}"
LOCAL_NODE_NAME="${MINIK8S_ACCEPTANCE_LOCAL_NODE:-${MINIK8S_NODE_A_NAME:-node-a}}"
LOCAL_NODE_IP="${MINIK8S_NODE_A_IP:-192.168.1.4}"

POD_MAIN="pod-02-main"
POD_SCHED_1="pod-02-sched-1"
POD_SCHED_2="pod-02-sched-2"
POD_SCHED_3="pod-02-sched-3"
ALL_PODS=("$POD_MAIN" "$POD_SCHED_1" "$POD_SCHED_2" "$POD_SCHED_3")

SECTION_STATUS="PASS"
SECTION_PASS_COUNT=0
SECTION_TOTAL=4
PREFLIGHT_DONE=0

manifest_dir() {
  if [ -d "$REMOTE_DIR/manifests/pod" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/pod"
    return 0
  fi
  printf '%s\n' "$ROOT/manifest/pod"
}

MANIFEST_DIR="$(manifest_dir)"
MAIN_MANIFEST="$MANIFEST_DIR/pod_02_acceptance_main.yaml"
SCHED_MANIFESTS=(
  "$MANIFEST_DIR/pod_02_acceptance_sched_1.yaml"
  "$MANIFEST_DIR/pod_02_acceptance_sched_2.yaml"
  "$MANIFEST_DIR/pod_02_acceptance_sched_3.yaml"
)

section_begin() {
  SECTION_STATUS="PASS"
  begin "$1"
}

section_fail() {
  SECTION_STATUS="FAIL"
  printf '[FAIL] %s\n' "$*"
}

section_end() {
  cleanup "$1"
  printf '[END] status=%s\n' "$SECTION_STATUS"
  if [ "$SECTION_STATUS" = "PASS" ]; then
    SECTION_PASS_COUNT=$((SECTION_PASS_COUNT + 1))
  fi
}

command_line() {
  local arg
  for arg in "$@"; do
    printf '%q ' "$arg"
  done | sed 's/[[:space:]]$//'
}

quiet_check() {
  local message="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    return 0
  fi
  section_fail "$message"
  return 1
}

quiet_run() {
  local message="$1"
  shift
  local out code
  set +e
  out="$("$@" 2>&1)"
  code=$?
  set -e
  if [ "$code" -eq 0 ]; then
    return 0
  fi
  printf '[RUN] %s\n' "$(command_line "$@")"
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  section_fail "$message"
  return 1
}

evidence_run() {
  local message="$1"
  shift
  local out code
  printf '[RUN] %s\n' "$(command_line "$@")"
  set +e
  out="$("$@" 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  if [ "$code" -eq 0 ]; then
    pass "$message"
    return 0
  fi
  section_fail "$message"
  return 1
}

container_id_cmd() {
  local pod_name="$1"
  local container_name="$2"
  printf "docker ps -aq --filter label=minik8s.kind=pod-container --filter label=minik8s.pod.name=%q --filter label=minik8s.container.name=%q | head -n 1" "$pod_name" "$container_name"
}

pod_node() {
  "$KUBECTL_BIN" get pod "$1" -o yaml | sed -n 's/^[[:space:]]*nodeName:[[:space:]]*//p' | head -n 1
}

pod_summary() {
  local pod_name="$1"
  local yaml
  yaml="$("$KUBECTL_BIN" get pod "$pod_name" -o yaml)"
  printf '%s\n' "$yaml" | awk '
    /^[[:space:]]*name:[[:space:]]*/ && name == "" { name=$2 }
    /^[[:space:]]*nodeName:[[:space:]]*/ { node=$2 }
    /^[[:space:]]*phase:[[:space:]]*/ { phase=$2 }
    /^[[:space:]]*podIP:[[:space:]]*/ { ip=$2 }
    /^[[:space:]]*image:[[:space:]]*/ {
      img=$2
      if (images == "") images=img; else images=images "," img
    }
    /^[[:space:]]*restartCount:[[:space:]]*/ {
      rc=$2
      if (restarts == "") restarts=rc; else restarts=restarts "," rc
    }
    END {
      printf "name=%s node=%s phase=%s podIP=%s images=%s restarts=%s\n", name, node, phase, ip, images, restarts
    }
  '
}

manifest_summary() {
  sed -n '
    s/^kind:/kind:/p
    s/^apiVersion:/apiVersion:/p
    s/^[[:space:]]*name:[[:space:]]*/name: /p
    s/^[[:space:]]*image:[[:space:]]*/image: /p
    s/^[[:space:]]*imageTag:[[:space:]]*/imageTag: /p
    s/^[[:space:]]*command:[[:space:]]*/command: /p
    s/^[[:space:]]*args:[[:space:]]*/args: /p
    s/^[[:space:]]*containerPort:[[:space:]]*/containerPort: /p
    s/^[[:space:]]*cpu:[[:space:]]*/cpu: /p
    s/^[[:space:]]*memory:[[:space:]]*/memory: /p
    s/^[[:space:]]*mountPath:[[:space:]]*/mountPath: /p
    s/^[[:space:]]*path:[[:space:]]*/hostPath.path: /p
    s/^[[:space:]]*restartPolicy:[[:space:]]*/restartPolicy: /p
    s/^[[:space:]]*node:[[:space:]]*/nodeSelector.node: /p
  ' "$1"
}

get_restart_count() {
  local pod_name="$1"
  local container_name="$2"
  "$KUBECTL_BIN" describe pod "$pod_name" |
    sed -n "s/.*${container_name} ready=.* restarts=\\([0-9][0-9]*\\).*/\\1/p" |
    head -n 1
}

wait_for_pod_running() {
  local pod_name="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if "$KUBECTL_BIN" describe pod "$pod_name" 2>/dev/null | grep -Eq '^Status:[[:space:]]*Running'; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$pod_name final describe before failure" "$KUBECTL_BIN" describe pod "$pod_name" || true
  section_fail "$pod_name did not become Running"
  return 1
}

wait_for_pod_scheduled() {
  local pod_name="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if [ -n "$(pod_node "$pod_name" 2>/dev/null || true)" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$pod_name did not receive spec.nodeName"
  return 1
}

wait_for_restart_count_gt() {
  local pod_name="$1"
  local container_name="$2"
  local before="$3"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local current
    current="$(get_restart_count "$pod_name" "$container_name" || true)"
    if [ -n "$current" ] && [ "$current" -gt "$before" ]; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$pod_name final describe before restartCount failure" "$KUBECTL_BIN" describe pod "$pod_name" || true
  section_fail "container $container_name restartCount did not increase"
  return 1
}

delete_pod_if_exists() {
  local pod_name="$1"
  if "$KUBECTL_BIN" get pod "$pod_name" >/dev/null 2>&1; then
    quiet_run "delete stale pod $pod_name" "$KUBECTL_BIN" delete pod "$pod_name" || true
  fi
}

cleanup_pods() {
  local pod_name
  for pod_name in "$@"; do
    delete_pod_if_exists "$pod_name"
  done
}

cleanup_runtime() {
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if bash -lc 'test -z "$(docker ps -aq --filter label=minik8s.kind=pod-container --filter label=case=minik8s-acceptance-02)"' >/dev/null 2>&1; then
      break
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  quiet_run "remove shared hostPath directory" rm -rf "$SHARED_DIR" || true
}

cleanup_all() {
  cleanup_pods "${ALL_PODS[@]}"
  cleanup_runtime
}

cleanup_trap() {
  cleanup_all >/dev/null 2>&1 || true
}
trap cleanup_trap EXIT

preflight() {
  if [ "$PREFLIGHT_DONE" -eq 1 ]; then
    return 0
  fi
  quiet_check "kubectl binary exists" test -x "$KUBECTL_BIN" || return 1
  quiet_check "Harbor API is reachable" curl -fsS -o /dev/null "$MINIK8S_HARBOR/api/v1" || return 1
  quiet_check "$LOCAL_NODE_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$LOCAL_NODE_NAME" || return 1
  quiet_check "local Docker is usable for Pod runtime inspection" docker version --format 'client={{.Client.Version}} server={{.Server.Version}}' || return 1
  quiet_check "main Pod manifest exists" test -f "$MAIN_MANIFEST" || return 1
  quiet_check "scheduler Pod manifests exist" test -f "${SCHED_MANIFESTS[0]}" -a -f "${SCHED_MANIFESTS[1]}" -a -f "${SCHED_MANIFESTS[2]}" || return 1
  PREFLIGHT_DONE=1
}

apply_main_pod() {
  local show_manifest="${1:-0}"
  cleanup_pods "$POD_MAIN"
  quiet_run "create shared hostPath directory" mkdir -p "$SHARED_DIR" || return 1
  if [ "$show_manifest" = "1" ]; then
    evidence_run "main Pod manifest shows kind, name, images, commands, ports, resources, and volume" manifest_summary "$MAIN_MANIFEST" || return 1
  fi
  evidence_run "main Pod manifest applied" "$KUBECTL_BIN" apply -f "$MAIN_MANIFEST" || return 1
  wait_for_pod_running "$POD_MAIN" || return 1
  evidence_run "main Pod status summary shows node, phase, IP, images, and restarts" pod_summary "$POD_MAIN" || return 1

  local node_name
  node_name="$(pod_node "$POD_MAIN")"
  if [ "$node_name" != "$LOCAL_NODE_NAME" ]; then
    section_fail "$POD_MAIN scheduled to $node_name, expected $LOCAL_NODE_NAME by nodeSelector"
    return 1
  fi
}

verify_runtime_parameters() {
  evidence_run "web and sidecar containers exist locally" \
    docker ps --filter label=minik8s.pod.name="$POD_MAIN" --filter label=minik8s.kind=pod-container --format '{{.Names}} {{.Image}} {{.Status}}' || return 1
  evidence_run "web image command and resource limits are applied" bash -lc \
    "cid=\$($(container_id_cmd "$POD_MAIN" web)); test -n \"\$cid\"; docker inspect \"\$cid\" --format 'name={{.Name}} image={{.Config.Image}} entrypoint={{json .Config.Entrypoint}} cmd={{json .Config.Cmd}} memory={{.HostConfig.Memory}} nanoCPUs={{.HostConfig.NanoCpus}} network={{.HostConfig.NetworkMode}}'" || return 1
  evidence_run "sidecar image command and resource limits are applied" bash -lc \
    "cid=\$($(container_id_cmd "$POD_MAIN" sidecar)); test -n \"\$cid\"; docker inspect \"\$cid\" --format 'name={{.Name}} image={{.Config.Image}} entrypoint={{json .Config.Entrypoint}} cmd={{json .Config.Cmd}} memory={{.HostConfig.Memory}} nanoCPUs={{.HostConfig.NanoCpus}} network={{.HostConfig.NetworkMode}}'" || return 1
}

section_lifecycle() {
  section_begin "02.1 pod create start delete and restart acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR local_node=$LOCAL_NODE_NAME local_ip=$LOCAL_NODE_IP manifests=$MANIFEST_DIR"
  if preflight &&
    apply_main_pod 1 &&
    evidence_run "kubectl describe shows Pod containers, status, and restart counters" "$KUBECTL_BIN" describe pod "$POD_MAIN" &&
    verify_runtime_parameters; then
    local before after
    before="$(get_restart_count "$POD_MAIN" sidecar || true)"
    before="${before:-0}"
    evidence_run "sidecar container is killed to simulate a crash" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" sidecar)); test -n \"\$cid\"; docker kill \"\$cid\"" &&
      wait_for_restart_count_gt "$POD_MAIN" sidecar "$before" || true
    after="$(get_restart_count "$POD_MAIN" sidecar || true)"
    after="${after:-0}"
    if [ "$SECTION_STATUS" = "PASS" ]; then
      evidence_run "sidecar restartCount increased after crash" bash -c 'before="$1"; after="$2"; printf "sidecar restartCount before=%s after=%s\n" "$before" "$after"; test "$after" -gt "$before"' _ "$before" "$after" || true
    fi
    if [ "$SECTION_STATUS" = "PASS" ]; then
      evidence_run "main Pod deleted through kubectl" "$KUBECTL_BIN" delete pod "$POD_MAIN" || true
      evidence_run "main Pod is absent after delete" bash -c 'if "$1" get pod "$2" >/dev/null 2>&1; then "$1" get pod "$2"; exit 1; fi; printf "%s absent\n" "$2"' _ "$KUBECTL_BIN" "$POD_MAIN" || true
    fi
  fi
  cleanup_pods "$POD_MAIN"
  cleanup_runtime
  section_end "02.1 cleaned only Pod $POD_MAIN and its local shared state"
}

section_localhost() {
  section_begin "02.2 same Pod localhost communication acceptance"
  output "scenario: web container serves nginx on port 80; sidecar container accesses it through 127.0.0.1 in the same Pod network namespace"
  if preflight &&
    apply_main_pod 0; then
    evidence_run "sidecar reaches nginx through localhost" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" sidecar)); test -n \"\$cid\"; docker exec \"\$cid\" wget -qO- http://127.0.0.1/ | head -n 3" || true
  fi
  if [ "$SECTION_STATUS" != "PASS" ]; then
    cleanup_pods "$POD_MAIN"
    cleanup_runtime
    section_end "02.2 cleaned Pod $POD_MAIN after localhost communication failure"
    return 0
  fi
  section_end "02.2 kept Pod $POD_MAIN for 02.4 volume sharing when successful"
}

section_scheduler() {
  section_begin "02.3 pod multinode scheduler acceptance"
  output "scheduler policy: Harbor filters Ready nodes and nodeSelector/resource requests, then assigns unscheduled Pods by round-robin across candidate nodes."
  if preflight; then
    cleanup_pods "$POD_SCHED_1" "$POD_SCHED_2" "$POD_SCHED_3"

    local manifest pod_name
    for manifest in "${SCHED_MANIFESTS[@]}"; do
      pod_name="$(sed -n 's/^[[:space:]]*name:[[:space:]]*//p' "$manifest" | head -n 1)"
      evidence_run "$pod_name manifest applied" "$KUBECTL_BIN" apply -f "$manifest" || break
      wait_for_pod_scheduled "$pod_name" || break
      evidence_run "$pod_name assigned node is visible" pod_summary "$pod_name" || break
    done

    if [ "$SECTION_STATUS" = "PASS" ]; then
      evidence_run "scheduled Pods are distributed to at least two nodes" bash -c '
        KUBECTL_BIN="$1"
        shift
        nodes=""
        for pod_name in "$@"; do
          node="$("$KUBECTL_BIN" get pod "$pod_name" -o yaml | sed -n "s/^[[:space:]]*nodeName:[[:space:]]*//p" | head -n 1)"
          status="$("$KUBECTL_BIN" describe pod "$pod_name" | sed -n "s/^Status:[[:space:]]*//p" | head -n 1)"
          printf "%s node=%s status=%s\n" "$pod_name" "$node" "$status"
          nodes="${nodes}${node}
"
        done
        distinct="$(printf "%s\n" "$nodes" | sed "/^$/d" | sort -u | wc -l | tr -d " ")"
        printf "distinct_nodes=%s\n" "$distinct"
        test "$distinct" -ge 2
      ' _ "$KUBECTL_BIN" "$POD_SCHED_1" "$POD_SCHED_2" "$POD_SCHED_3" || true
    fi
  fi

  cleanup_pods "$POD_SCHED_1" "$POD_SCHED_2" "$POD_SCHED_3"
  section_end "02.3 cleaned only scheduler Pods"
}

section_volume() {
  section_begin "02.4 same Pod volume file sharing acceptance"
  output "scenario: web and sidecar containers mount the same hostPath volume at different paths"
  if preflight; then
    if ! "$KUBECTL_BIN" describe pod "$POD_MAIN" 2>/dev/null | grep -Eq '^Status:[[:space:]]*Running'; then
      apply_main_pod 0 || true
    fi
    if [ "$SECTION_STATUS" = "PASS" ]; then
      evidence_run "web writes a file to the shared volume" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" web)); test -n \"\$cid\"; docker exec \"\$cid\" sh -c 'echo volume-from-web > /usr/share/nginx/html/shared/from-web.txt; cat /usr/share/nginx/html/shared/from-web.txt'" &&
        evidence_run "sidecar reads the file written by web through the same volume" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" sidecar)); test -n \"\$cid\"; docker exec \"\$cid\" cat /shared/from-web.txt" &&
        evidence_run "web reads the sidecar startup marker through the same volume" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" web)); test -n \"\$cid\"; docker exec \"\$cid\" cat /usr/share/nginx/html/shared/sidecar.txt" || true
    fi
  fi
  cleanup_pods "$POD_MAIN"
  cleanup_runtime
  section_end "02.4 cleaned Pod $POD_MAIN and shared volume state"
}

main() {
  if [ "${1:-}" = "cleanup" ]; then
    begin "02 pod acceptance cleanup"
    cleanup_all
    cleanup "02 cleanup completed"
    end
    return 0
  fi

  section_lifecycle
  section_localhost
  section_scheduler
  section_volume
  printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  if [ "$SECTION_PASS_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
