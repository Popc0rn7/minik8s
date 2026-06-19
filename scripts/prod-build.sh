#!/bin/sh
set -eu

PROD_IMAGE="${PROD_IMAGE:-golang:1.25.9-bookworm}"
PROD_DIR="${PROD_DIR:-dist/prod}"
PROD_GOOS="${PROD_GOOS:-linux}"
PROD_GOARCH="${PROD_GOARCH:-amd64}"
PROD_GOPROXY="${PROD_GOPROXY:-https://goproxy.cn,https://proxy.golang.org,direct}"
PROD_DOCKER_USER="${PROD_DOCKER_USER:-$(id -u):$(id -g)}"
PROD_DOCKER_NETWORK="${PROD_DOCKER_NETWORK:-}"

network_args=""
if [ -n "${PROD_DOCKER_NETWORK}" ]; then
	network_args="--network ${PROD_DOCKER_NETWORK}"
fi

docker run --rm \
	--user "${PROD_DOCKER_USER}" \
	${network_args} \
	-v "$(pwd):/src" \
	-w /src \
	-e HOME=/tmp \
	-e GOCACHE=/tmp/go-build \
	-e GOMODCACHE=/tmp/go/pkg/mod \
	-e GOPROXY="${PROD_GOPROXY}" \
	-e HTTP_PROXY="${HTTP_PROXY:-}" \
	-e HTTPS_PROXY="${HTTPS_PROXY:-}" \
	-e ALL_PROXY="${ALL_PROXY:-}" \
	-e NO_PROXY="${NO_PROXY:-}" \
	-e http_proxy="${http_proxy:-}" \
	-e https_proxy="${https_proxy:-}" \
	-e all_proxy="${all_proxy:-}" \
	-e no_proxy="${no_proxy:-}" \
	-e CGO_ENABLED=0 \
	-e GOOS="${PROD_GOOS}" \
	-e GOARCH="${PROD_GOARCH}" \
	"${PROD_IMAGE}" \
	sh -c 'mkdir -p "'"${PROD_DIR}"'/bin" && \
		go build -trimpath -tags "netgo osusergo" -ldflags="-s -w" -o "'"${PROD_DIR}"'/bin/minik8s" ./cmd/minik8s && \
		go build -trimpath -tags "netgo osusergo" -ldflags="-s -w" -o "'"${PROD_DIR}"'/bin/kubectl" ./cmd/kubectl && \
		go build -trimpath -tags "netgo osusergo" -ldflags="-s -w" -o "'"${PROD_DIR}"'/bin/mooring" ./cmd/mooring'
