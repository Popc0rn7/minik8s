#!/bin/sh
set -eu

GPU_SUBMITTER_IMAGE="${GPU_SUBMITTER_IMAGE:-ghcr.io/popc0rn7/gpu-submitter}"
IMAGE_TAG="${IMAGE_TAG:-latest}"

docker push "${GPU_SUBMITTER_IMAGE}:${IMAGE_TAG}"
