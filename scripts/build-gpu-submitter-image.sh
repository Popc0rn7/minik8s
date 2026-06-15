#!/bin/sh
set -eu

GPU_SUBMITTER_IMAGE="${GPU_SUBMITTER_IMAGE:-ghcr.io/popc0rn7/gpu-submitter}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
PLATFORM="${PLATFORM:-linux/amd64}"

docker build \
	--platform "${PLATFORM}" \
	-f Dockerfile.gpu-submitter \
	-t "${GPU_SUBMITTER_IMAGE}:${IMAGE_TAG}" \
	.
