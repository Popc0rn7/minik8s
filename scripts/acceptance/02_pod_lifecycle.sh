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
  02.1 Pod lifecycle, parameters, delete, and restartCount
  02.2 Same-Pod localhost communication and volume sharing
  02.3 Multi-node scheduler assignment

Each section creates only the Pods it needs, cleans them up, emits its own
[END] status=<PASS|FAIL>, then the script emits a final [END] status=N/3PASS.
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
SECTION_TOTAL=3
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

section_check_run() {
  local message="$1"
  shift
  if run "$@"; then
    pass "$message"
    return 0
  fi
  section_fail "$message"
  return 1
}

section_check_quiet() {
  local message="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    pass "$message"
    return 0
  fi
  section_fail "$message"
  return 1
}

section_output_run() {
  local message="$1"
  shift
  printf '[OUTPUT]\n'
  "$@" 2>&1 || {
    section_fail "$message"
    return 1
  }
  pass "$message"
}

section_end() {
  cleanup "$1"
  printf '[END] status=%s\n' "$SECTION_STATUS"
  if [ "$SECTION_STATUS" = "PASS" ]; then
    SECTION_PASS_COUNT=$((SECTION_PASS_COUNT + 1))
  fi
}

container_id_cmd() {
  local pod_name="$1"
  local container_name="$2"
  printf "docker ps -aq --filter label=minik8s.kind=pod-container --filter label=minik8s.pod.name=%q --filter label=minik8s.container.name=%q | head -n 1" "$pod_name" "$container_name"
}

pod_node() {
  "$KUBECTL_BIN" get pod "$1" -o yaml | sed -n 's/^[[:space:]]*nodeName:[[:space:]]*//p' | head -n 1
}

pod_status() {
  "$KUBECTL_BIN" describe pod "$1" | sed -n 's/^Status:[[:space:]]*//p' | head -n 1
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
    s/^[[:space:]]*path:[[:space:]]*/path: /p
    s/^[[:space:]]*restartPolicy:[[:space:]]*/restartPolicy: /p
    s/^[[:space:]]*node:[[:space:]]*/nodeSelector.node: /p
  ' "$1"
}

wait_note() {
  local message="$1"
  local attempt="$2"
  if [ "$attempt" -eq 1 ] || [ "$attempt" -eq "$WAIT_ATTEMPTS" ] || [ $((attempt % 5)) -eq 0 ]; then
    output "$message attempt=$attempt/$WAIT_ATTEMPTS"
  fi
}

wait_for_pod_running() {
  local pod_name="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if "$KUBECTL_BIN" describe pod "$pod_name" 2>/dev/null | grep -Eq '^Status:[[:space:]]*Running'; then
      pass "$pod_name is Running"
      return 0
    fi
    wait_note "waiting for pod $pod_name Running" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_check_run "$pod_name final describe before failure" "$KUBECTL_BIN" describe pod "$pod_name" || true
  section_fail "$pod_name did not become Running"
  return 1
}

wait_for_pod_scheduled() {
  local pod_name="$1"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local node_name
    node_name="$(pod_node "$pod_name" || true)"
    if [ -n "$node_name" ]; then
      pass "$pod_name has scheduler-assigned nodeName=$node_name"
      return 0
    fi
    wait_note "waiting for pod $pod_name scheduler assignment" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "$pod_name did not receive spec.nodeName"
  return 1
}

wait_for_restart_count() {
  local pod_name="$1"
  local container_name="$2"
  local before="$3"
  local attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local current
    current="$("$KUBECTL_BIN" describe pod "$pod_name" | sed -n "s/.*${container_name} ready=.* restarts=\\([0-9][0-9]*\\).*/\\1/p" | head -n 1)"
    if [ -n "$current" ] && [ "$current" -gt "$before" ]; then
      pass "container $container_name restartCount increased from $before to $current"
      return 0
    fi
    wait_note "waiting for restartCount pod=$pod_name container=$container_name current=${current:-unknown} before=$before" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  section_fail "container $container_name restartCount did not increase"
  return 1
}

delete_pod_if_exists() {
  local pod_name="$1"
  if "$KUBECTL_BIN" get pod "$pod_name" >/dev/null 2>&1; then
    run "$KUBECTL_BIN" delete pod "$pod_name" || true
  else
    output "pod $pod_name already absent"
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
      pass "no local acceptance Pod containers remain"
      break
    fi
    wait_note "waiting for local runtime cleanup" "$attempt"
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  run rm -rf "$SHARED_DIR" || true
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
    pass "shared preflight already passed"
    return $?
  fi
  section_check_quiet "kubectl binary exists" test -x "$KUBECTL_BIN" || return 1
  section_check_run "Harbor API is reachable" curl -fsS -o /dev/null -w 'http=%{http_code}\n' "$MINIK8S_HARBOR/api/v1" || return 1
  section_check_run "kubectl can list nodes" "$KUBECTL_BIN" get nodes || return 1
  section_check_quiet "$LOCAL_NODE_NAME is Ready" bash -c '"$1" get node "$2" -o yaml | grep -Eq "phase:[[:space:]]*Ready"' _ "$KUBECTL_BIN" "$LOCAL_NODE_NAME" || return 1
  section_check_run "local Docker is usable for Pod runtime inspection" docker version --format 'client={{.Client.Version}} server={{.Server.Version}}' || return 1
  section_check_quiet "main Pod manifest exists" test -f "$MAIN_MANIFEST" || return 1
  section_check_quiet "scheduler Pod manifests exist" test -f "${SCHED_MANIFESTS[0]}" -a -f "${SCHED_MANIFESTS[1]}" -a -f "${SCHED_MANIFESTS[2]}" || return 1
  PREFLIGHT_DONE=1
}

prepare_main_pod() {
  cleanup_pods "$POD_MAIN"
  section_check_run "shared hostPath directory created" mkdir -p "$SHARED_DIR" || return 1
  section_check_run "main Pod manifest summary shows required fields" manifest_summary "$MAIN_MANIFEST" || return 1
  section_check_run "main Pod manifest applied" "$KUBECTL_BIN" apply -f "$MAIN_MANIFEST" || return 1
  wait_for_pod_running "$POD_MAIN" || return 1
  section_check_run "main Pod status summary exposes node, phase, IP, images, and restarts" pod_summary "$POD_MAIN" || return 1
  section_output_run "main Pod describe exposes containers and restart counters" bash -c '
    "$1" describe pod "$2" | sed -n "/^Containers:/,/^Events:/p" | sed -n "1,30p"
  ' _ "$KUBECTL_BIN" "$POD_MAIN" || return 1

  local node_name
  node_name="$(pod_node "$POD_MAIN")"
  if [ "$node_name" != "$LOCAL_NODE_NAME" ]; then
    section_fail "$POD_MAIN scheduled to $node_name, expected $LOCAL_NODE_NAME by nodeSelector"
    return 1
  fi
  pass "$POD_MAIN scheduled to $node_name by nodeSelector"
}

verify_runtime_parameters() {
  section_check_run "web and sidecar containers exist locally" \
    docker ps --filter label=minik8s.pod.name="$POD_MAIN" --filter label=minik8s.kind=pod-container --format '{{.Names}} {{.Image}} {{.Status}}' || return 1
  section_check_run "web image command port and resource limits are applied" bash -lc \
    "cid=\$($(container_id_cmd "$POD_MAIN" web)); test -n \"\$cid\"; docker inspect \"\$cid\" --format 'name={{.Name}} image={{.Config.Image}} entrypoint={{json .Config.Entrypoint}} cmd={{json .Config.Cmd}} memory={{.HostConfig.Memory}} nanoCPUs={{.HostConfig.NanoCpus}} network={{.HostConfig.NetworkMode}}'" || return 1
  section_check_run "sidecar image command and resource limits are applied" bash -lc \
    "cid=\$($(container_id_cmd "$POD_MAIN" sidecar)); test -n \"\$cid\"; docker inspect \"\$cid\" --format 'name={{.Name}} image={{.Config.Image}} entrypoint={{json .Config.Entrypoint}} cmd={{json .Config.Cmd}} memory={{.HostConfig.Memory}} nanoCPUs={{.HostConfig.NanoCpus}} network={{.HostConfig.NetworkMode}}'" || return 1
}

section_lifecycle() {
  section_begin "02.1 pod lifecycle and restart acceptance"
  output "remote_dir=$REMOTE_DIR harbor=$MINIK8S_HARBOR local_node=$LOCAL_NODE_NAME local_ip=$LOCAL_NODE_IP manifests=$MANIFEST_DIR"
  preflight &&
    prepare_main_pod &&
    verify_runtime_parameters &&
    section_check_run "sidecar container is killed to simulate a crash" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" sidecar)); test -n \"\$cid\"; docker kill \"\$cid\"" &&
    wait_for_restart_count "$POD_MAIN" sidecar 0
  cleanup_pods "$POD_MAIN"
  cleanup_runtime
  section_end "02.1 cleaned Pod $POD_MAIN and local runtime state"
}

section_localhost_volume() {
  section_begin "02.2 pod localhost and volume acceptance"
  output "using manifest=$MAIN_MANIFEST"
  preflight &&
    prepare_main_pod &&
    section_check_run "sidecar reaches nginx through localhost inside shared Pod network namespace" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" sidecar)); test -n \"\$cid\"; docker exec \"\$cid\" wget -qO- http://127.0.0.1/ | head -n 3" &&
    section_check_run "web writes a file to shared volume" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" web)); test -n \"\$cid\"; docker exec \"\$cid\" sh -c 'echo volume-from-web > /usr/share/nginx/html/shared/from-web.txt'" &&
    section_check_run "sidecar reads the file written by web through the same volume" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" sidecar)); test -n \"\$cid\"; docker exec \"\$cid\" cat /shared/from-web.txt" &&
    section_check_run "web reads the sidecar startup marker through the same volume" bash -lc "cid=\$($(container_id_cmd "$POD_MAIN" web)); test -n \"\$cid\"; docker exec \"\$cid\" cat /usr/share/nginx/html/shared/sidecar.txt"
  cleanup_pods "$POD_MAIN"
  cleanup_runtime
  section_end "02.2 cleaned Pod $POD_MAIN and shared volume state"
}

section_scheduler() {
  section_begin "02.3 pod multinode scheduler acceptance"
  output "scheduler policy: Harbor uses Ready nodes sorted by name, filters nodeSelector and resource requests, then assigns unscheduled Pods by round-robin."
  if preflight; then
    cleanup_pods "$POD_SCHED_1" "$POD_SCHED_2" "$POD_SCHED_3"

    local manifest pod_name
    for manifest in "${SCHED_MANIFESTS[@]}"; do
      pod_name="$(sed -n 's/^[[:space:]]*name:[[:space:]]*//p' "$manifest" | head -n 1)"
      section_check_run "$pod_name manifest applied" "$KUBECTL_BIN" apply -f "$manifest" || break
      wait_for_pod_scheduled "$pod_name" || break
      section_check_run "$pod_name summary shows assigned nodeName" pod_summary "$pod_name" || break
    done

    if [ "$SECTION_STATUS" = "PASS" ]; then
      section_check_run "scheduled Pods and nodes are summarized" bash -c '
        KUBECTL_BIN="$1"
        shift
        for pod_name in "$@"; do
          node="$("$KUBECTL_BIN" get pod "$pod_name" -o yaml | sed -n "s/^[[:space:]]*nodeName:[[:space:]]*//p" | head -n 1)"
          status="$("$KUBECTL_BIN" describe pod "$pod_name" | sed -n "s/^Status:[[:space:]]*//p" | head -n 1)"
          printf "%s node=%s status=%s\n" "$pod_name" "$node" "$status"
        done
      ' _ "$KUBECTL_BIN" "$POD_SCHED_1" "$POD_SCHED_2" "$POD_SCHED_3" || true
      section_check_run "scheduler assigned acceptance Pods to at least two distinct nodes" bash -c '
        KUBECTL_BIN="$1"
        shift
        for pod_name in "$@"; do
          "$KUBECTL_BIN" get pod "$pod_name" -o yaml | sed -n "s/^[[:space:]]*nodeName:[[:space:]]*//p" | head -n 1
        done | sort -u | wc -l | awk "{exit !(\$1 >= 2)}"
      ' _ "$KUBECTL_BIN" "$POD_SCHED_1" "$POD_SCHED_2" "$POD_SCHED_3" || true
    fi
  fi

  cleanup_pods "$POD_SCHED_1" "$POD_SCHED_2" "$POD_SCHED_3"
  section_end "02.3 cleaned scheduler Pods"
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
  section_localhost_volume
  section_scheduler
  printf '[END] status=%s/%sPASS\n' "$SECTION_PASS_COUNT" "$SECTION_TOTAL"
  if [ "$SECTION_PASS_COUNT" -ne "$SECTION_TOTAL" ]; then
    return 1
  fi
}

main "$@"
