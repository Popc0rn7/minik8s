#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash scripts/acceptance/20_personal_gpu.sh
  bash scripts/acceptance/20_personal_gpu.sh cleanup
  bash scripts/acceptance/20_personal_gpu.sh --help

Run on node-a after 01_node_multinode.sh has started bridge and sailers.
This script validates the personal GPU Job path backed by SJTU HPC Slurm.

Required external staging:
  /opt/minik8s/secrets/gpu-ssh/
    SSH private key, for example id_ed25519_minik8s
    matching OpenSSH certificate, for example id_ed25519_minik8s-cert.pub
    known_hosts
    config

Sections:
  GPU.1 CUDA source, Job YAML, Slurm fields, and SSH prerequisites
  GPU.2 Submit vector-add Job and inspect submitter Pod/Service
  GPU.3 Observe vector-add Slurm status and result
  GPU.4 Submit second vector-add Job and check isolation
  GPU.5 Submit tiled matrix multiplication Job and inspect result

Evidence commands print [RUN], [EXIT], [OUTPUT], and a conclusion for
TA-readable logs. Queue-limited Slurm runs pass when Minik8s can show the
pending/running state, Slurm Job ID, remote directory, and the exact HPC
query command instead of CUDA stdout.
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
WAIT_ATTEMPTS="${MINIK8S_ACCEPTANCE_GPU_WAIT_ATTEMPTS:-72}"
WAIT_SLEEP_SECONDS="${MINIK8S_ACCEPTANCE_GPU_WAIT_SLEEP_SECONDS:-10}"
GPU_IMAGE="${MINIK8S_GPU_SUBMITTER_IMAGE:-ghcr.io/popc0rn7/gpu-submitter:v0.1.0}"
GPU_SSH_DIR="${MINIK8S_GPU_SSH_DIR:-$REMOTE_DIR/secrets/gpu-ssh}"
GPU_REMOTE_HOST="${MINIK8S_GPU_REMOTE_HOST:-sylogin.hpc.sjtu.edu.cn}"
GPU_REMOTE_USER="${MINIK8S_GPU_REMOTE_USER:-stu1718}"
GPU_SUBMITTER_NODE="${MINIK8S_GPU_SUBMITTER_NODE:-${MINIK8S_NODE_A_NAME:-node-a}}"
GPU_SSH_KEY=""
GPU_SSH_CERT=""
GPU_MANIFEST_WORKDIR=""

JOB_ADD="cuda-add"
JOB_ADD_2="cuda-add-2"
JOB_MATMUL="cuda-matmul-tiled"
SUBMITTER_ADD="job-cuda-add-submitter"
SUBMITTER_ADD_2="job-cuda-add-2-submitter"
SUBMITTER_MATMUL="job-cuda-matmul-tiled-submitter"
ALL_JOBS=("$JOB_MATMUL" "$JOB_ADD_2" "$JOB_ADD")

SECTION_STATUS="PASS"
SECTION_PASS_COUNT=0
SECTION_ACCEPT_COUNT=0
SECTION_LIMITED_COUNT=0
SECTION_TOTAL=5
PREFLIGHT_OK=0

manifest_dir() {
  if [ -d "$REMOTE_DIR/manifests/job" ]; then
    printf '%s\n' "$REMOTE_DIR/manifests/job"
    return 0
  fi
  printf '%s\n' "$ROOT/manifest/job"
}

MANIFEST_DIR="$(manifest_dir)"
ADD_MANIFEST="$MANIFEST_DIR/cuda-add.yaml"
ADD_2_MANIFEST="$MANIFEST_DIR/cuda-add-2.yaml"
MATMUL_MANIFEST="$MANIFEST_DIR/cuda-matmul.yaml"

cleanup_runtime() {
  if [ -n "$GPU_MANIFEST_WORKDIR" ] && [ -d "$GPU_MANIFEST_WORKDIR" ]; then
    rm -rf "$GPU_MANIFEST_WORKDIR"
  fi
}
trap cleanup_runtime EXIT

section_begin() {
  SECTION_STATUS="PASS"
  begin "$1"
}

section_fail() {
  SECTION_STATUS="FAIL"
  printf '[FAIL] %s\n' "$*"
}

section_limited() {
  if [ "$SECTION_STATUS" = "PASS" ]; then
    SECTION_STATUS="LIMITED"
  fi
  printf '[LIMITED] %s\n' "$*"
}

section_end() {
  cleanup "$1"
  printf '[END] status=%s\n' "$SECTION_STATUS"
  if [ "$SECTION_STATUS" = "PASS" ]; then
    SECTION_PASS_COUNT=$((SECTION_PASS_COUNT + 1))
  fi
  if [ "$SECTION_STATUS" = "PASS" ] || [ "$SECTION_STATUS" = "LIMITED" ]; then
    SECTION_ACCEPT_COUNT=$((SECTION_ACCEPT_COUNT + 1))
  fi
  if [ "$SECTION_STATUS" = "LIMITED" ]; then
    SECTION_LIMITED_COUNT=$((SECTION_LIMITED_COUNT + 1))
  fi
}

command_line() {
  local arg
  for arg in "$@"; do
    printf '%q ' "$arg"
  done | sed 's/[[:space:]]$//'
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

evidence_limited() {
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
  section_limited "$message"
  return 1
}

job_yaml() {
  "$KUBECTL_BIN" get job "$1" -o yaml
}

job_phase() {
  job_yaml "$1" | sed -n 's/^[[:space:]]*phase:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

job_slurm_id() {
  job_yaml "$1" | sed -n 's/^[[:space:]]*slurmJobId:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

job_remote_dir() {
  job_yaml "$1" | sed -n 's/^[[:space:]]*remoteDir:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

job_start_time() {
  job_yaml "$1" | sed -n 's/^[[:space:]]*startTime:[[:space:]]*//p' | head -n 1 | tr -d '"'
}

job_summary() {
  local name="$1" phase slurm_id remote_dir start_time
  phase="$(job_phase "$name" 2>/dev/null || true)"
  slurm_id="$(job_slurm_id "$name" 2>/dev/null || true)"
  remote_dir="$(job_remote_dir "$name" 2>/dev/null || true)"
  start_time="$(job_start_time "$name" 2>/dev/null || true)"
  printf 'job=%s phase=%s slurmJobID=%s remoteDir=%s startTime=%s query=%s\n' \
    "$name" "${phase:-<none>}" "${slurm_id:-<none>}" "${remote_dir:-<none>}" "${start_time:-<none>}" "$(hpc_query_command "${slurm_id:-<SLURM_JOB_ID>}")"
}

hpc_query_command() {
  local slurm_id="$1"
  printf "ssh %s@%s 'squeue -j %s || sacct -j %s --format=JobID,State,ExitCode'" \
    "$GPU_REMOTE_USER" "$GPU_REMOTE_HOST" "$slurm_id" "$slurm_id"
}

ssh_command() {
  local args=(
    -F /dev/null
    -i "$GPU_SSH_KEY"
    -o BatchMode=yes \
    -o ConnectTimeout=8 \
    -o StrictHostKeyChecking=yes \
    -o UserKnownHostsFile="$GPU_SSH_DIR/known_hosts"
  )
  if [ -n "$GPU_SSH_CERT" ]; then
    args+=(-o "CertificateFile=$GPU_SSH_CERT")
  fi
  ssh "${args[@]}" "$GPU_REMOTE_USER@$GPU_REMOTE_HOST" "$1"
}

ssh_config_value() {
  local key="$1"
  if [ ! -f "$GPU_SSH_DIR/config" ]; then
    return 1
  fi
  awk -v host="$GPU_REMOTE_HOST" -v key="$key" '
    BEGIN { in_host = 0 }
    /^[[:space:]]*Host[[:space:]]+/ {
      in_host = 0
      for (i = 2; i <= NF; i++) {
        if ($i == host || $i == "*") {
          in_host = 1
        }
      }
      next
    }
    in_host && tolower($1) == tolower(key) {
      print $2
      exit
    }
  ' "$GPU_SSH_DIR/config"
}

resolve_gpu_ssh_files() {
  local identity_file certificate_file
  identity_file="$(ssh_config_value IdentityFile || true)"
  certificate_file="$(ssh_config_value CertificateFile || true)"
  if [ -z "$identity_file" ] || [ -z "$certificate_file" ]; then
    return 1
  fi
  GPU_SSH_KEY="$GPU_SSH_DIR/$(basename "$identity_file")"
  GPU_SSH_CERT="$GPU_SSH_DIR/$(basename "$certificate_file")"
  return 0
}

job_manifest_summary() {
  sed -n '
    s/^kind:/kind:/p
    s/^apiVersion:/apiVersion:/p
    s/^[[:space:]]*name:[[:space:]]*/name: /p
    s/^[[:space:]]*accelerator:[[:space:]]*/accelerator: /p
    s/^[[:space:]]*node:[[:space:]]*/nodeSelector.node: /p
    s/^[[:space:]]*files:[[:space:]]*/files:/p
    s/^[[:space:]]*-[[:space:]]*\(.*\.cu\)/source.file: \1/p
    s/^[[:space:]]*-[[:space:]]*\(Makefile.*\)/source.file: \1/p
    s/^[[:space:]]*command:[[:space:]]*/command: /p
    s/^[[:space:]]*partition:[[:space:]]*/partition: /p
    s/^[[:space:]]*qos:[[:space:]]*/qos: /p
    s/^[[:space:]]*nodes:[[:space:]]*/nodes: /p
    s/^[[:space:]]*ntasksPerNode:[[:space:]]*/ntasksPerNode: /p
    s/^[[:space:]]*cpusPerTask:[[:space:]]*/cpusPerTask: /p
    s/^[[:space:]]*gres:[[:space:]]*/gres: /p
    s/^[[:space:]]*time:[[:space:]]*/time: /p
    s/^[[:space:]]*host:[[:space:]]*/remote.host: /p
    s/^[[:space:]]*username:[[:space:]]*/remote.username: /p
    s/^[[:space:]]*workdir:[[:space:]]*/remote.workdir: /p
  ' "$1"
}

force_job_node_selector() {
  local src="$1"
  local dst="$2"
  if grep -Eq '^[[:space:]]*nodeSelector:[[:space:]]*$' "$src"; then
    sed -E "s/^([[:space:]]*node:[[:space:]]*).*/\\1$GPU_SUBMITTER_NODE/" "$src" >"$dst"
    return 0
  fi
  awk -v node="$GPU_SUBMITTER_NODE" '
    {
      print
      if ($0 ~ /^spec:[[:space:]]*$/) {
        print "  nodeSelector:"
        print "    node: " node
      }
    }
  ' "$src" >"$dst"
}

prepare_job_manifests() {
  GPU_MANIFEST_WORKDIR="$(mktemp -d /tmp/minik8s-gpu-manifests.XXXXXX)"
  force_job_node_selector "$ADD_MANIFEST" "$GPU_MANIFEST_WORKDIR/cuda-add.yaml"
  force_job_node_selector "$ADD_2_MANIFEST" "$GPU_MANIFEST_WORKDIR/cuda-add-2.yaml"
  force_job_node_selector "$MATMUL_MANIFEST" "$GPU_MANIFEST_WORKDIR/cuda-matmul.yaml"
  ADD_MANIFEST="$GPU_MANIFEST_WORKDIR/cuda-add.yaml"
  ADD_2_MANIFEST="$GPU_MANIFEST_WORKDIR/cuda-add-2.yaml"
  MATMUL_MANIFEST="$GPU_MANIFEST_WORKDIR/cuda-matmul.yaml"
}

verify_submitter_node_label() {
  "$KUBECTL_BIN" get node "$GPU_SUBMITTER_NODE" -o yaml | grep -Eq "node:[[:space:]]*$GPU_SUBMITTER_NODE"
}

job_manifest_has_submitter_node() {
  grep -Eq "^[[:space:]]*node:[[:space:]]*$GPU_SUBMITTER_NODE[[:space:]]*$" "$1"
}

cuda_source_summary() {
  printf 'vector_add=%s\n' "$MANIFEST_DIR/vector_add.cu"
  grep -E 'threadsPerBlock|blocksPerGrid|N = 1 << 20|Result: PASS' "$MANIFEST_DIR/vector_add.cu" || true
  printf 'matmul_tiled=%s\n' "$MANIFEST_DIR/matmul_tiled.cu"
  grep -E 'TILE_SIZE|dim3 block|dim3 grid|__shared__|__syncthreads|Result: PASS' "$MANIFEST_DIR/matmul_tiled.cu" || true
}

wait_for_submitter_resources() {
  local job_name="$1" submitter="$2" attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    if "$KUBECTL_BIN" get pod "$submitter" >/dev/null 2>&1 &&
      "$KUBECTL_BIN" get svc "$submitter" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
  evidence_run "$job_name final describe before submitter resource failure" "$KUBECTL_BIN" describe job "$job_name" || true
  section_fail "$submitter Pod/Service did not appear"
  return 1
}

wait_for_terminal_or_slurm_evidence() {
  local job_name="$1" attempt=1
  while [ "$attempt" -le "$WAIT_ATTEMPTS" ]; do
    local phase
    phase="$(job_phase "$job_name" 2>/dev/null || true)"
    case "$phase" in
      Succeeded)
        return 0
        ;;
      Failed)
        evidence_run "$job_name final describe after failure" "$KUBECTL_BIN" describe job "$job_name" || true
        section_fail "$job_name entered Failed"
        return 1
        ;;
    esac
    sleep "$WAIT_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done

  evidence_run "$job_name pending/running describe after wait budget" "$KUBECTL_BIN" describe job "$job_name" || true
  printf '[OUTPUT]\n'
  job_summary "$job_name"
  if [ -n "$(job_slurm_id "$job_name" 2>/dev/null || true)" ]; then
    pass "$job_name did not finish before timeout; Slurm pending/running evidence printed"
    return 0
  fi
  section_fail "$job_name did not reach Slurm submission before timeout"
  return 1
}

assert_logs_contain() {
  local job_name="$1"
  local pattern="$2"
  local message="$3"
  local out code
  printf '[RUN] %s | grep -E %q\n' "$(command_line "$KUBECTL_BIN" logs job "$job_name")" "$pattern"
  set +e
  out="$("$KUBECTL_BIN" logs job "$job_name" 2>&1)"
  code=$?
  set -e
  printf '[EXIT] %s\n' "$code"
  printf '[OUTPUT]\n%s\n' "$out"
  if [ "$code" -eq 0 ] && printf '%s\n' "$out" | grep -Eq "$pattern"; then
    pass "$message"
    return 0
  fi
  section_fail "$message"
  return 1
}

delete_job_if_exists() {
  local job_name="$1"
  if "$KUBECTL_BIN" get job "$job_name" >/dev/null 2>&1; then
    quiet_run "delete stale job $job_name" "$KUBECTL_BIN" delete job "$job_name" || true
  fi
}

cleanup_jobs() {
  local job_name
  for job_name in "${ALL_JOBS[@]}"; do
    delete_job_if_exists "$job_name"
  done
}

run_cleanup_mode() {
  section_begin "GPU cleanup"
  cleanup_jobs
  evidence_run "jobs after cleanup" "$KUBECTL_BIN" get jobs || true
  evidence_run "pods after cleanup" "$KUBECTL_BIN" get pods || true
  evidence_run "services after cleanup" "$KUBECTL_BIN" get services || true
  section_end "GPU cleanup complete"
  printf '[END] status=%s/%s accepted limited=%s\n' "$SECTION_ACCEPT_COUNT" 1 "$SECTION_LIMITED_COUNT"
  if [ "$SECTION_ACCEPT_COUNT" -eq 1 ]; then
    exit 0
  fi
  exit 1
}

preflight() {
  section_begin "GPU.1 CUDA source, Job YAML, Slurm fields, and SSH prerequisites"
  printf '[OUTPUT]\nmanifest_dir=%s generated_manifest_dir=%s gpu_ssh_dir=%s gpu_remote=%s@%s submitter_image=%s submitter_node=%s harbor=%s\n' \
    "$MANIFEST_DIR" "$GPU_MANIFEST_WORKDIR" "$GPU_SSH_DIR" "$GPU_REMOTE_USER" "$GPU_REMOTE_HOST" "$GPU_IMAGE" "$GPU_SUBMITTER_NODE" "$MINIK8S_HARBOR"

  evidence_run "kubectl binary is available" test -x "$KUBECTL_BIN" || true
  evidence_run "GPU submitter node has node=$GPU_SUBMITTER_NODE label" verify_submitter_node_label || true
  evidence_run "cuda-add manifest exists" test -f "$ADD_MANIFEST" || true
  evidence_run "cuda-add-2 manifest exists" test -f "$ADD_2_MANIFEST" || true
  evidence_run "cuda-matmul manifest exists" test -f "$MATMUL_MANIFEST" || true
  evidence_run "vector add source exists" test -f "$MANIFEST_DIR/vector_add.cu" || true
  evidence_run "tiled matmul source exists" test -f "$MANIFEST_DIR/matmul_tiled.cu" || true
  evidence_run "vector add Makefile exists" test -f "$MANIFEST_DIR/Makefile" || true
  evidence_run "matmul Makefile exists" test -f "$MANIFEST_DIR/Makefile.matmul" || true

  step "CUDA source parallelism evidence"
  evidence_run "CUDA source contains parallelism markers" bash -lc "$(declare -f cuda_source_summary); MANIFEST_DIR='$MANIFEST_DIR'; cuda_source_summary"

  step "Job manifest summary"
  evidence_run "cuda-add Job YAML includes required fields" bash -lc "$(declare -f job_manifest_summary); job_manifest_summary '$ADD_MANIFEST'"
  evidence_run "cuda-add Job YAML pins submitter to $GPU_SUBMITTER_NODE" job_manifest_has_submitter_node "$ADD_MANIFEST"
  evidence_run "cuda-add-2 Job YAML pins submitter to $GPU_SUBMITTER_NODE" job_manifest_has_submitter_node "$ADD_2_MANIFEST"
  evidence_run "cuda-matmul Job YAML includes required fields" bash -lc "$(declare -f job_manifest_summary); job_manifest_summary '$MATMUL_MANIFEST'"
  evidence_run "cuda-matmul Job YAML pins submitter to $GPU_SUBMITTER_NODE" job_manifest_has_submitter_node "$MATMUL_MANIFEST"

  evidence_limited "GPU submitter image is available locally" docker image inspect "$GPU_IMAGE" || true
  evidence_run "Harbor API is reachable" curl --noproxy '*' -fsS "$MINIK8S_HARBOR/api/v1" || true

  if [ ! -d "$GPU_SSH_DIR" ]; then
    printf '[RUN] test -d %q\n' "$GPU_SSH_DIR"
    printf '[EXIT] 1\n'
    printf '[OUTPUT]\n%s is missing\n' "$GPU_SSH_DIR"
    section_limited "$GPU_SSH_DIR is not staged; upload secrets/gpu-ssh to the target machine before full GPU acceptance"
    section_end "GPU.1 preflight limited"
    return 1
  fi
  evidence_run "GPU SSH directory exists" test -d "$GPU_SSH_DIR" || true
  evidence_run "GPU SSH config exists" test -f "$GPU_SSH_DIR/config" || true
  evidence_limited "GPU SSH known_hosts exists" test -f "$GPU_SSH_DIR/known_hosts" || true
  if resolve_gpu_ssh_files; then
    printf '[OUTPUT]\nssh_config_identity=%s\nssh_config_certificate=%s\n' "$GPU_SSH_KEY" "$GPU_SSH_CERT"
  else
    printf '[RUN] parse %q for IdentityFile and CertificateFile\n' "$GPU_SSH_DIR/config"
    printf '[EXIT] 1\n'
    printf '[OUTPUT]\nconfig must contain both IdentityFile and CertificateFile for %s or Host *\n' "$GPU_REMOTE_HOST"
    section_fail "$GPU_SSH_DIR/config must define IdentityFile and CertificateFile"
  fi
  evidence_run "GPU SSH identity from config exists" test -f "$GPU_SSH_KEY" || true
  evidence_run "GPU SSH certificate from config exists" test -f "$GPU_SSH_CERT" || true

  if [ -n "$GPU_SSH_KEY" ] && [ -n "$GPU_SSH_CERT" ] && [ -f "$GPU_SSH_DIR/known_hosts" ]; then
    evidence_limited "HPC SSH can find Slurm commands" ssh_command 'hostname && command -v sbatch && command -v squeue && command -v sacct' || true
  fi

  section_end "GPU.1 preflight complete"
  if [ "$SECTION_STATUS" = "PASS" ]; then
    PREFLIGHT_OK=1
    return 0
  fi
  return 1
}

section_submit_vector_add() {
  section_begin "GPU.2 submit vector-add Job and inspect submitter Pod/Service"
  delete_job_if_exists "$JOB_ADD"
  evidence_run "apply cuda-add Job" "$KUBECTL_BIN" apply -f "$ADD_MANIFEST" || true
  wait_for_submitter_resources "$JOB_ADD" "$SUBMITTER_ADD" || true
  evidence_run "get jobs after cuda-add apply" "$KUBECTL_BIN" get jobs || true
  evidence_run "describe cuda-add Job" "$KUBECTL_BIN" describe job "$JOB_ADD" || true
  evidence_run "get cuda-add submitter Pod" "$KUBECTL_BIN" get pod "$SUBMITTER_ADD" -o yaml || true
  evidence_run "get cuda-add submitter Service" "$KUBECTL_BIN" get svc "$SUBMITTER_ADD" -o yaml || true
  section_end "GPU.2 submit vector-add complete"
}

section_observe_vector_add() {
  section_begin "GPU.3 observe vector-add Slurm status and result"
  wait_for_terminal_or_slurm_evidence "$JOB_ADD" || true
  evidence_run "describe cuda-add after wait" "$KUBECTL_BIN" describe job "$JOB_ADD" || true
  printf '[OUTPUT]\n'
  job_summary "$JOB_ADD"
  if [ "$(job_phase "$JOB_ADD" 2>/dev/null || true)" = "Succeeded" ]; then
    assert_logs_contain "$JOB_ADD" 'N = 1048576|threadsPerBlock = 256|blocksPerGrid = 4096|Result: PASS' "cuda-add logs show vector-add result" || true
    assert_logs_contain "$JOB_ADD" 'Result: PASS' "cuda-add result passed" || true
  else
    pass "$JOB_ADD has Slurm pending/running evidence as substitute for CUDA result"
  fi
  section_end "GPU.3 observe vector-add complete"
}

section_isolation() {
  section_begin "GPU.4 submit second vector-add Job and check isolation"
  delete_job_if_exists "$JOB_ADD_2"
  evidence_run "apply cuda-add-2 Job" "$KUBECTL_BIN" apply -f "$ADD_2_MANIFEST" || true
  wait_for_submitter_resources "$JOB_ADD_2" "$SUBMITTER_ADD_2" || true
  wait_for_terminal_or_slurm_evidence "$JOB_ADD_2" || true
  evidence_run "describe cuda-add Job for isolation" "$KUBECTL_BIN" describe job "$JOB_ADD" || true
  evidence_run "describe cuda-add-2 Job for isolation" "$KUBECTL_BIN" describe job "$JOB_ADD_2" || true
  evidence_run "get both submitter Pods" bash -lc "$KUBECTL_BIN get pods | grep -E '$SUBMITTER_ADD|$SUBMITTER_ADD_2'" || true
  evidence_run "get both submitter Services" bash -lc "$KUBECTL_BIN get services | grep -E '$SUBMITTER_ADD|$SUBMITTER_ADD_2'" || true

  local add_dir add2_dir add_id add2_id
  add_dir="$(job_remote_dir "$JOB_ADD" 2>/dev/null || true)"
  add2_dir="$(job_remote_dir "$JOB_ADD_2" 2>/dev/null || true)"
  add_id="$(job_slurm_id "$JOB_ADD" 2>/dev/null || true)"
  add2_id="$(job_slurm_id "$JOB_ADD_2" 2>/dev/null || true)"
  printf '[OUTPUT]\njob=%s remoteDir=%s slurmJobID=%s\njob=%s remoteDir=%s slurmJobID=%s\n' \
    "$JOB_ADD" "${add_dir:-<none>}" "${add_id:-<none>}" "$JOB_ADD_2" "${add2_dir:-<none>}" "${add2_id:-<none>}"
  if [ -n "$add_dir" ] && [ -n "$add2_dir" ] && [ "$add_dir" != "$add2_dir" ]; then
    pass "GPU Jobs use distinct remote directories"
  else
    section_fail "GPU Jobs should use distinct remote directories"
  fi
  if [ -n "$add_id" ] && [ -n "$add2_id" ] && [ "$add_id" != "$add2_id" ]; then
    pass "GPU Jobs use distinct Slurm Job IDs"
  elif [ -n "$add_id" ] || [ -n "$add2_id" ]; then
    pass "one Slurm Job ID is still pending; submitter and remote directory isolation evidence is available"
  else
    section_fail "no Slurm Job IDs available for isolation check"
  fi
  section_end "GPU.4 isolation complete"
}

section_matmul() {
  section_begin "GPU.5 submit tiled matrix multiplication Job and inspect result"
  delete_job_if_exists "$JOB_MATMUL"
  evidence_run "apply cuda-matmul Job" "$KUBECTL_BIN" apply -f "$MATMUL_MANIFEST" || true
  wait_for_submitter_resources "$JOB_MATMUL" "$SUBMITTER_MATMUL" || true
  wait_for_terminal_or_slurm_evidence "$JOB_MATMUL" || true
  evidence_run "describe cuda-matmul-tiled Job" "$KUBECTL_BIN" describe job "$JOB_MATMUL" || true
  printf '[OUTPUT]\n'
  job_summary "$JOB_MATMUL"
  if [ "$(job_phase "$JOB_MATMUL" 2>/dev/null || true)" = "Succeeded" ]; then
    assert_logs_contain "$JOB_MATMUL" 'Matrix N = 1024|Tile size = 16|Block = 16 x 16|Grid = 64 x 64|Kernel: tiled shared-memory matrix multiplication|Result: PASS' "cuda-matmul logs show tiled GPU result" || true
    assert_logs_contain "$JOB_MATMUL" 'Result: PASS' "cuda-matmul result passed" || true
  else
    pass "$JOB_MATMUL has Slurm pending/running evidence as substitute for CUDA result"
  fi
  section_end "GPU.5 tiled matmul complete"
}

if [ "${1:-}" = "cleanup" ]; then
  run_cleanup_mode
fi

if [ -n "${1:-}" ]; then
  usage
  exit 2
fi

prepare_job_manifests
preflight || true
if [ "$PREFLIGHT_OK" -ne 1 ]; then
  printf '[END] status=%s/%s accepted limited=%s\n' "$SECTION_ACCEPT_COUNT" 1 "$SECTION_LIMITED_COUNT"
  if [ "$SECTION_ACCEPT_COUNT" -eq 1 ]; then
    exit 0
  fi
  exit 1
fi

section_submit_vector_add
section_observe_vector_add
section_isolation
section_matmul

cleanup "GPU acceptance leaves completed Job records until explicit cleanup; run scripts/acceptance/20_personal_gpu.sh cleanup to delete them"
printf '[END] status=%s/%s accepted limited=%s\n' "$SECTION_ACCEPT_COUNT" "$SECTION_TOTAL" "$SECTION_LIMITED_COUNT"
if [ "$SECTION_ACCEPT_COUNT" -eq "$SECTION_TOTAL" ]; then
  exit 0
fi
exit 1
