# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25.9
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct

FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src

ARG GOPROXY
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
COPY internal/thirdparty/pkgerrors/go.mod internal/thirdparty/pkgerrors/go.mod
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -trimpath -ldflags="-s -w" -o /out/minik8s ./cmd/minik8s

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -trimpath -ldflags="-s -w" -o /out/minik8s-bridge ./cmd/minik8s-bridge

FROM debian:bookworm-slim AS runtime

LABEL org.opencontainers.image.title="Minik8s" \
	org.opencontainers.image.description="A small Kubernetes-like lab control plane and node agent" \
	org.opencontainers.image.source="https://github.com/Popc0rn7/minik8s" \
	org.opencontainers.image.licenses="Apache-2.0"

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/minik8s /usr/local/bin/minik8s
COPY --from=builder /out/minik8s-bridge /opt/cni/bin/minik8s-bridge

ENTRYPOINT ["minik8s"]
CMD ["--help"]
