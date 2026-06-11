#!/bin/sh
set -eu

MOORING_CNI_IMAGE="${MOORING_CNI_IMAGE:-ghcr.io/popc0rn7/mooring-cni}"
IMAGE_TAG="${IMAGE_TAG:-latest}"

docker push "${MOORING_CNI_IMAGE}:${IMAGE_TAG}"
