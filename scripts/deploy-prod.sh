#!/usr/bin/env sh
set -eu

REMOTE_DIR="${REMOTE_DIR:-/opt/minik8s}"
PROD_DIR="${PROD_DIR:-dist/prod}"
MOORING_CNI_IMAGE="${MOORING_CNI_IMAGE:-ghcr.io/popc0rn7/mooring-cni}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
REMOTE_DOCKER="${REMOTE_DOCKER:-docker}"
SSH_CONFIG="${SSH_CONFIG:-$HOME/.ssh/config}"
SSH_OPTS="${SSH_OPTS:-}"
RSYNC_RSH="${RSYNC_RSH:-}"
RSYNC_OPTS="${RSYNC_OPTS:-}"
NODE1="${NODE1:-node-1}"
NODE2="${NODE2:-node-2}"

if [ -z "$SSH_OPTS" ] && [ -f "$SSH_CONFIG" ]; then
	SSH_OPTS="-F $SSH_CONFIG"
fi
if [ -z "$RSYNC_RSH" ]; then
	RSYNC_RSH="ssh $SSH_OPTS"
fi

usage() {
	printf 'Usage: %s [--build] [--push-image] [--pull-image] [--sync-only]\n' "$0"
	printf '       %s\n' "$0"
	printf '\n'
	printf 'With no flags, the script runs the default update flow: build, push image, sync, and remote pull.\n'
	printf 'When any stage flag is provided, only the selected optional stages run before/after sync.\n'
	printf 'Each deploy node must be reachable from this host via ssh; use ~/.ssh/config ProxyJump if needed.\n'
	printf '\n'
	printf 'Environment:\n'
	printf '  DEPLOY_NODES       space-separated ssh targets, for example: "root@10.0.0.1 root@10.0.0.2"\n'
	printf '  NODE1              default first direct ssh target when DEPLOY_NODES is unset, default: node-1\n'
	printf '  NODE2              default second direct ssh target when DEPLOY_NODES is unset, default: node-2\n'
	printf '  REMOTE_DIR         install directory on each node, default: /opt/minik8s\n'
	printf '  PROD_DIR           local prod artifact directory, default: dist/prod\n'
	printf '  MOORING_CNI_IMAGE  mooring CNI image repository, default: ghcr.io/popc0rn7/mooring-cni\n'
	printf '  IMAGE_TAG          image tag, default: latest\n'
	printf '  REMOTE_DOCKER      docker command on remote nodes, default: docker\n'
	printf '  SSH_CONFIG         ssh config file, default: $HOME/.ssh/config when present\n'
	printf '  SSH_OPTS           extra ssh options, for example: -i ~/.ssh/id_rsa\n'
	printf '  RSYNC_RSH          rsync remote shell, default: ssh plus SSH_OPTS\n'
	printf '  RSYNC_OPTS         extra rsync options, not for ssh -e options\n'
	printf '\n'
	printf 'Defaults:\n'
	printf '  If DEPLOY_NODES is unset, this host deploys directly to NODE1 and NODE2.\n'
}

default_update=0
if [ "$#" -eq 0 ]; then
	default_update=1
fi

build=0
push_image=0
pull_image=0
while [ "$#" -gt 0 ]; do
	case "$1" in
		--build)
			build=1
			;;
		--push-image)
			push_image=1
			;;
		--pull-image)
			pull_image=1
			;;
		--sync-only)
			build=0
			push_image=0
			pull_image=0
			default_update=0
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
	shift
done

if [ "$default_update" -eq 1 ]; then
	build=1
	push_image=1
	pull_image=1
fi

if [ -z "${DEPLOY_NODES:-}" ]; then
	DEPLOY_NODES="$NODE1 $NODE2"
fi

if [ "$build" -eq 1 ]; then
	make prod
fi

if [ "$push_image" -eq 1 ]; then
	MOORING_CNI_IMAGE="$MOORING_CNI_IMAGE" IMAGE_TAG="$IMAGE_TAG" make mooring-cni-image
	MOORING_CNI_IMAGE="$MOORING_CNI_IMAGE" IMAGE_TAG="$IMAGE_TAG" make push-mooring-cni-image
fi

for file in minik8s kubectl; do
	if [ ! -f "$PROD_DIR/$file" ]; then
		printf 'missing artifact: %s\n' "$PROD_DIR/$file" >&2
		printf 'run `make prod` first, or use `%s --build`\n' "$0" >&2
		exit 1
	fi
done

if [ ! -d manifest ]; then
	printf 'missing manifest directory\n' >&2
	exit 1
fi

for node in $DEPLOY_NODES; do
	printf 'creating %s on %s\n' "$REMOTE_DIR" "$node"
	ssh $SSH_OPTS "$node" "mkdir -p '$REMOTE_DIR'"

	printf 'syncing binaries and manifests to %s:%s\n' "$node" "$REMOTE_DIR"
	rsync -az --delete -e "$RSYNC_RSH" $RSYNC_OPTS "$PROD_DIR"/minik8s "$PROD_DIR"/kubectl manifest "$node:$REMOTE_DIR/"

	printf 'setting executable bits on %s\n' "$node"
	ssh $SSH_OPTS "$node" "chmod +x '$REMOTE_DIR/minik8s' '$REMOTE_DIR/kubectl'"

	if [ "$pull_image" -eq 1 ]; then
		printf 'pulling %s:%s on %s\n' "$MOORING_CNI_IMAGE" "$IMAGE_TAG" "$node"
		ssh $SSH_OPTS "$node" "$REMOTE_DOCKER pull '$MOORING_CNI_IMAGE:$IMAGE_TAG'"
	fi
done

printf 'deployment artifacts updated on: %s\n' "$DEPLOY_NODES"
