#!/bin/sh
set -eu

MOORING_CNI_IMAGE="${MOORING_CNI_IMAGE:-ghcr.io/popc0rn7/mooring-cni}"
IMAGE_TAG="${IMAGE_TAG:-v0.1.0}"
PLATFORM="${PLATFORM:-linux/amd64}"

docker build \
	--platform "${PLATFORM}" \
	-f Dockerfile.mooring-cni \
	-t "${MOORING_CNI_IMAGE}:${IMAGE_TAG}" \
	.
