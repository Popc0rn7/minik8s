#!/bin/sh
set -eu

PROD_IMAGE="${PROD_IMAGE:-golang:1.25.9-bookworm}"
PROD_DIR="${PROD_DIR:-dist/prod}"
PROD_GOOS="${PROD_GOOS:-linux}"
PROD_GOARCH="${PROD_GOARCH:-amd64}"
PROD_GOPROXY="${PROD_GOPROXY:-https://goproxy.cn,https://proxy.golang.org,direct}"
PROD_DOCKER_USER="${PROD_DOCKER_USER:-$(id -u):$(id -g)}"

docker run --rm \
	--user "${PROD_DOCKER_USER}" \
	-v "$(pwd):/src" \
	-w /src \
	-e HOME=/tmp \
	-e GOCACHE=/tmp/go-build \
	-e GOMODCACHE=/tmp/go/pkg/mod \
	-e GOPROXY="${PROD_GOPROXY}" \
	-e CGO_ENABLED=0 \
	-e GOOS="${PROD_GOOS}" \
	-e GOARCH="${PROD_GOARCH}" \
	"${PROD_IMAGE}" \
	sh -c 'mkdir -p "'"${PROD_DIR}"'" && \
		go build -trimpath -tags "netgo osusergo" -ldflags="-s -w" -o "'"${PROD_DIR}"'/minik8s" ./cmd/minik8s && \
		go build -trimpath -tags "netgo osusergo" -ldflags="-s -w" -o "'"${PROD_DIR}"'/kubectl" ./cmd/kubectl && \
		go build -trimpath -tags "netgo osusergo" -ldflags="-s -w" -o "'"${PROD_DIR}"'/mooring" ./cmd/mooring'
