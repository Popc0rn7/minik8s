#!/usr/bin/env sh
set -eu

REMOTE_DIR="${REMOTE_DIR:-/opt/minik8s}"
DEMO_DIR="${DEMO_DIR:-demo/serverless/harbor-incident-triage}"
SSH_CONFIG="${SSH_CONFIG:-$HOME/.ssh/config}"
SSH_OPTS="${SSH_OPTS:-}"
NODE1="${NODE1:-node-1}"
NODE2="${NODE2:-node-2}"

if [ -z "$SSH_OPTS" ] && [ -f "$SSH_CONFIG" ]; then
	SSH_OPTS="-F $SSH_CONFIG"
fi

if [ -z "${DEPLOY_NODES:-}" ]; then
	DEPLOY_NODES="$NODE1 $NODE2"
fi

if [ ! -d "$DEMO_DIR" ]; then
	printf 'missing demo directory: %s\n' "$DEMO_DIR" >&2
	exit 1
fi

for node in $DEPLOY_NODES; do
	printf 'creating demo parent directory on %s\n' "$node"
	ssh $SSH_OPTS "$node" "mkdir -p '$REMOTE_DIR/demo/serverless'"

	printf 'copying %s to %s:%s/demo/serverless/\n' "$DEMO_DIR" "$node" "$REMOTE_DIR"
	scp $SSH_OPTS -r "$DEMO_DIR" "$node:$REMOTE_DIR/demo/serverless/"
done

printf 'demo synced to: %s\n' "$DEPLOY_NODES"
