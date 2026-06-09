#!/usr/bin/env sh
set -eu

NODE1="${NODE1:-node-1}"
NODE2="${NODE2:-node-2}"
REMOTE_DIR="${REMOTE_DIR:-/root/minik8s/bin}"
PROD_DIR="${PROD_DIR:-dist/prod}"
SSH_OPTS="${SSH_OPTS:-}"
SCP_OPTS="${SCP_OPTS:-}"

FILES="minik8s kubectl mooring"

usage() {
	printf 'Usage: %s [--build]\n' "$0"
	printf '\n'
	printf 'Environment:\n'
	printf '  NODE1       first hop host, default: node-1\n'
	printf '  NODE2       second host reachable from NODE1, default: node-2\n'
	printf '  REMOTE_DIR  install directory on both nodes, default: /root/minik8s/bin\n'
	printf '  PROD_DIR    local prod artifact directory, default: dist/prod\n'
	printf '  SSH_OPTS    extra ssh options, for example: -i ~/.ssh/id_rsa\n'
	printf '  SCP_OPTS    extra scp options, for example: -i ~/.ssh/id_rsa\n'
}

build=0
case "${1:-}" in
	"")
		;;
	--build)
		build=1
		;;
	-h|--help)
		usage
		exit 0
		;;
	*)
		usage >&2
		exit 2
		;;
esac

if [ "$build" -eq 1 ]; then
	make prod
fi

for file in $FILES; do
	if [ ! -f "$PROD_DIR/$file" ]; then
		printf 'missing artifact: %s\n' "$PROD_DIR/$file" >&2
		printf 'run `make prod` first, or use `%s --build`\n' "$0" >&2
		exit 1
	fi
done

printf 'creating %s on %s\n' "$REMOTE_DIR" "$NODE1"
ssh $SSH_OPTS "$NODE1" "mkdir -p '$REMOTE_DIR'"

printf 'uploading prod artifacts to %s:%s\n' "$NODE1" "$REMOTE_DIR"
scp $SCP_OPTS "$PROD_DIR"/minik8s "$PROD_DIR"/kubectl "$PROD_DIR"/mooring "$NODE1:$REMOTE_DIR/"

printf 'creating %s on %s via %s\n' "$REMOTE_DIR" "$NODE2" "$NODE1"
ssh $SSH_OPTS "$NODE1" "ssh $SSH_OPTS '$NODE2' 'mkdir -p '\''$REMOTE_DIR'\'''"

printf 'uploading prod artifacts from %s to %s:%s\n' "$NODE1" "$NODE2" "$REMOTE_DIR"
ssh $SSH_OPTS "$NODE1" "scp $SCP_OPTS '$REMOTE_DIR/minik8s' '$REMOTE_DIR/kubectl' '$REMOTE_DIR/mooring' '$NODE2:$REMOTE_DIR/'"

printf 'deployment artifacts copied to %s and %s\n' "$NODE1" "$NODE2"
