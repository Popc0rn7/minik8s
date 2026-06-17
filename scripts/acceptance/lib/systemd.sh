#!/usr/bin/env bash

set -u

SYSTEMD_UNIT_DIR="${SYSTEMD_UNIT_DIR:-/etc/systemd/system}"
SYSTEMCTL_BIN="${SYSTEMCTL_BIN:-systemctl}"
JOURNALCTL_BIN="${JOURNALCTL_BIN:-journalctl}"

render_unit_template() {
  local template="$1"
  local output_path="$2"
  if [ ! -f "$template" ]; then
    printf 'unit template not found: %s\n' "$template" >&2
    return 1
  fi
  sed \
    -e "s#__MINIK8S_REMOTE_DIR__#$MINIK8S_REMOTE_DIR#g" \
    -e "s#__MINIK8S_STATE_DIR__#$MINIK8S_STATE_DIR#g" \
    -e "s#__MINIK8S_NODE_A_IP__#$MINIK8S_NODE_A_IP#g" \
    -e "s#__MINIK8S_HARBOR_PORT__#$MINIK8S_HARBOR_PORT#g" \
    -e "s#__MINIK8S_CLUSTER_CIDR__#$MINIK8S_CLUSTER_CIDR#g" \
    -e "s#__MINIK8S_SERVICE_CIDR__#$MINIK8S_SERVICE_CIDR#g" \
    -e "s#__MINIK8S_NODE_PORT_RANGE__#$MINIK8S_NODE_PORT_RANGE#g" \
    "$template" >"$output_path"
  if grep -E "__[A-Z0-9_]+__" "$output_path" >/dev/null; then
    printf 'unrendered placeholder remains in %s\n' "$output_path" >&2
    return 1
  fi
}

install_unit() {
  local template="$1"
  local unit_name="$2"
  local target="$SYSTEMD_UNIT_DIR/$unit_name"
  local tmp
  tmp="$(mktemp)"
  render_unit_template "$template" "$tmp"
  check_run "$unit_name unit installed to $target" install -m 0644 "$tmp" "$target"
  rm -f "$tmp"
  check_run "systemd daemon reloaded after installing $unit_name" "$SYSTEMCTL_BIN" daemon-reload
}

restart_unit() {
  local unit_name="$1"
  check_run "$unit_name restarted" "$SYSTEMCTL_BIN" restart "$unit_name"
  check_run "$unit_name is active" "$SYSTEMCTL_BIN" is-active --quiet "$unit_name"
}

stop_unit() {
  local unit_name="$1"
  if run "$SYSTEMCTL_BIN" stop "$unit_name"; then
    pass "$unit_name stop requested"
  else
    mark_limited "$unit_name stop failed or unit is not installed"
  fi
}

status_unit() {
  local unit_name="$1"
  if run "$SYSTEMCTL_BIN" status "$unit_name" --no-pager; then
    pass "$unit_name status is readable"
  else
    mark_limited "$unit_name is not active or status is unavailable"
  fi
}

journal_unit() {
  local unit_name="$1"
  if run "$JOURNALCTL_BIN" -u "$unit_name" -n 10 --no-pager; then
    pass "$unit_name journal is readable"
  else
    mark_limited "$unit_name journal is unavailable"
  fi
}

clean_unit() {
  local unit_name="$1"
  stop_unit "$unit_name"
  run "$SYSTEMCTL_BIN" disable "$unit_name" || true
  run rm -f "$SYSTEMD_UNIT_DIR/$unit_name" || true
  run "$SYSTEMCTL_BIN" daemon-reload || true
  run "$SYSTEMCTL_BIN" reset-failed "$unit_name" || true
  pass "$unit_name unit cleaned"
}
