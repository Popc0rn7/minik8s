#!/usr/bin/env sh
set -eu

REMOTE_DIR="${REMOTE_DIR:-/opt/minik8s}"
PROD_DIR="${PROD_DIR:-.}"
MOORING_CNI_IMAGE="${MOORING_CNI_IMAGE:-ghcr.io/popc0rn7/mooring-cni}"
IMAGE_TAG="${IMAGE_TAG:-v0.1.0}"
REMOTE_DOCKER="${REMOTE_DOCKER:-docker}"
SSH_CONFIG="${SSH_CONFIG:-$HOME/.ssh/config}"
SSH_OPTS="${SSH_OPTS:-}"
RSYNC_RSH="${RSYNC_RSH:-}"
RSYNC_OPTS="${RSYNC_OPTS:-}"

if [ -z "$SSH_OPTS" ] && [ -f "$SSH_CONFIG" ]; then
	SSH_OPTS="-F $SSH_CONFIG"
fi
if [ -z "$RSYNC_RSH" ]; then
	RSYNC_RSH="ssh $SSH_OPTS"
fi

usage() {
	printf 'Usage: %s [--build] [--push-image] [--pull-image] [--pull-image-only] [--sync-only]\n' "$0"
	printf '       %s\n' "$0"
	printf '\n'
	printf 'With no flags, the script runs the default update flow: build, push image, sync, and remote pull.\n'
	printf 'When any stage flag is provided, only the selected optional stages run before/after sync.\n'
	printf '--pull-image-only pulls the CNI image on remote nodes without syncing artifacts.\n'
	printf 'Each deploy node must be reachable from this host via ssh; use ~/.ssh/config ProxyJump if needed.\n'
	printf '\n'
	printf 'Environment:\n'
	printf '  DEPLOY_NODES       required space-separated ssh targets, for example: "root@10.119.16.213"\n'
	printf '  REMOTE_DIR         install directory on each node, default: /opt/minik8s\n'
	printf '  PROD_DIR           local prod artifact directory, default: repository root\n'
	printf '  MOORING_CNI_IMAGE  mooring CNI image repository, default: ghcr.io/popc0rn7/mooring-cni\n'
	printf '  IMAGE_TAG          image tag, default: v0.1.0\n'
	printf '  REMOTE_DOCKER      docker command on remote nodes, default: docker\n'
	printf '  SSH_CONFIG         ssh config file, default: $HOME/.ssh/config when present\n'
	printf '  SSH_OPTS           extra ssh options, for example: -i ~/.ssh/id_ed25519_minik8s\n'
	printf '  RSYNC_RSH          rsync remote shell, default: ssh plus SSH_OPTS\n'
	printf '  RSYNC_OPTS         extra rsync options, not for ssh -e options\n'
	printf '\n'
	printf 'Example:\n'
	printf '  DEPLOY_NODES="root@10.119.16.213" SSH_OPTS="-i ~/.ssh/id_ed25519_minik8s" %s --sync-only\n' "$0"
}

default_update=0
if [ "$#" -eq 0 ]; then
	default_update=1
fi

build=0
push_image=0
pull_image=0
sync=1
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
		--pull-image-only)
			pull_image=1
			sync=0
			default_update=0
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
	printf 'DEPLOY_NODES is required for remote deploy.\n' >&2
	printf 'Example: DEPLOY_NODES="root@10.119.16.213" SSH_OPTS="-i ~/.ssh/id_ed25519_minik8s" %s --sync-only\n' "$0" >&2
	exit 2
fi

if [ "$build" -eq 1 ]; then
	make prod
fi

if [ "$push_image" -eq 1 ]; then
	MOORING_CNI_IMAGE="$MOORING_CNI_IMAGE" IMAGE_TAG="$IMAGE_TAG" make mooring-cni-image
	MOORING_CNI_IMAGE="$MOORING_CNI_IMAGE" IMAGE_TAG="$IMAGE_TAG" make push-mooring-cni-image
fi

if [ "$sync" -eq 1 ]; then
	for file in minik8s kubectl; do
		if [ ! -f "$PROD_DIR/$file" ]; then
			printf 'missing artifact: %s\n' "$PROD_DIR/$file" >&2
			printf 'run `make prod` first, or use `%s --build`\n' "$0" >&2
			exit 1
		fi
	done

	if [ ! -d manifests ]; then
		printf 'missing manifests directory\n' >&2
		exit 1
	fi
	if [ ! -d scripts/acceptance ]; then
		printf 'missing scripts/acceptance directory\n' >&2
		exit 1
	fi
	if [ ! -d demo/serverless/harbor-incident-triage ]; then
		printf 'missing demo/serverless/harbor-incident-triage directory\n' >&2
		exit 1
	fi
fi

for node in $DEPLOY_NODES; do
	if [ "$sync" -eq 1 ]; then
		printf 'creating %s on %s\n' "$REMOTE_DIR" "$node"
		ssh $SSH_OPTS "$node" "mkdir -p '$REMOTE_DIR/bin' '$REMOTE_DIR/scripts' '$REMOTE_DIR/manifests' '$REMOTE_DIR/demo/serverless' '$REMOTE_DIR/state' '$REMOTE_DIR/static-pods' '$REMOTE_DIR/dns' '$REMOTE_DIR/secrets/gpu-ssh' /etc/cni/net.d /opt/cni/bin"

		printf 'syncing binaries to %s:%s/bin\n' "$node" "$REMOTE_DIR"
		rsync -az --delete -e "$RSYNC_RSH" $RSYNC_OPTS "$PROD_DIR"/minik8s "$PROD_DIR"/kubectl "$node:$REMOTE_DIR/bin/"

		printf 'syncing acceptance scripts to %s:%s/scripts/acceptance\n' "$node" "$REMOTE_DIR"
		rsync -az --delete -e "$RSYNC_RSH" $RSYNC_OPTS scripts/acceptance/ "$node:$REMOTE_DIR/scripts/acceptance/"

		printf 'syncing manifests to %s:%s/manifests\n' "$node" "$REMOTE_DIR"
		rsync -az --delete -e "$RSYNC_RSH" $RSYNC_OPTS manifests/ "$node:$REMOTE_DIR/manifests/"

		printf 'syncing triage demo to %s:%s/demo/serverless/harbor-incident-triage\n' "$node" "$REMOTE_DIR"
		rsync -az --delete -e "$RSYNC_RSH" $RSYNC_OPTS demo/serverless/harbor-incident-triage/ "$node:$REMOTE_DIR/demo/serverless/harbor-incident-triage/"

		if [ -d secrets ]; then
			printf 'syncing secrets to %s:%s/secrets\n' "$node" "$REMOTE_DIR"
			rsync -az --delete -e "$RSYNC_RSH" $RSYNC_OPTS secrets/ "$node:$REMOTE_DIR/secrets/"
		fi

		printf 'setting executable bits and secret permissions on %s\n' "$node"
		ssh $SSH_OPTS "$node" "chmod +x '$REMOTE_DIR/bin/minik8s' '$REMOTE_DIR/bin/kubectl' '$REMOTE_DIR/scripts/acceptance/01_pod_network.fish' '$REMOTE_DIR/scripts/acceptance/'*.sh 2>/dev/null || true; chown -R root:root '$REMOTE_DIR/secrets/gpu-ssh' 2>/dev/null || true; chmod 700 '$REMOTE_DIR/secrets/gpu-ssh' 2>/dev/null || true; find '$REMOTE_DIR/secrets/gpu-ssh' -type f -name 'id_*' ! -name '*.pub' -exec chmod 600 {} + 2>/dev/null || true; chmod 600 '$REMOTE_DIR/secrets/gpu-ssh/config' 2>/dev/null || true; chmod 644 '$REMOTE_DIR/secrets/gpu-ssh/'*.pub '$REMOTE_DIR/secrets/gpu-ssh/known_hosts' 2>/dev/null || true"
	fi

	if [ "$pull_image" -eq 1 ]; then
		printf 'pulling %s:%s on %s\n' "$MOORING_CNI_IMAGE" "$IMAGE_TAG" "$node"
		ssh $SSH_OPTS "$node" "$REMOTE_DOCKER pull '$MOORING_CNI_IMAGE:$IMAGE_TAG'"
	fi
done

printf 'deployment artifacts updated on: %s\n' "$DEPLOY_NODES"
