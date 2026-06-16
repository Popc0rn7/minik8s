#!/usr/bin/env bash

DEMO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$DEMO_ROOT/../../.." && pwd)"

resolve_tool() {
  local env_name="$1"
  local default_path="$2"
  local tool_name="$3"
  local value="${!env_name:-}"

  if [[ -n "$value" ]]; then
    if [[ -x "$value" ]]; then
      printf '%s\n' "$value"
      return 0
    fi
    echo "$env_name=$value is not executable" >&2
    return 1
  fi

  if [[ -x "$default_path" ]]; then
    printf '%s\n' "$default_path"
    return 0
  fi

  if command -v "$tool_name" >/dev/null 2>&1; then
    command -v "$tool_name"
    return 0
  fi

  cat >&2 <<EOF
missing $tool_name binary

Expected executable:
  $default_path

Fix from the development machine, following docs/deploy.md:
  make deploy-prod

Or copy built binaries manually after make prod:
  scp dist/prod/minik8s dist/prod/kubectl node-1:$REPO_ROOT/
  scp dist/prod/minik8s dist/prod/kubectl node-2:$REPO_ROOT/

You can also override the path:
  $env_name=/path/to/$tool_name ./scripts/demo.sh
EOF
  return 1
}

CLI="$(resolve_tool CLI "$REPO_ROOT/kubectl" kubectl)"
MINIK8S="$(resolve_tool MINIK8S "$REPO_ROOT/minik8s" minik8s)"
