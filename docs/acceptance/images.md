# Minik8s Acceptance Image Table

本文记录最终验收建议提前准备的 Docker 镜像。所有项目自带 manifest、控制面依赖和构建脚本默认值都应使用固定 tag，不使用 `latest` 或浮动 shorthand tag。

## Image Table

| 层级 | 用途 | 镜像 | 来源 | 说明 |
| --- | --- | --- | --- | --- |
| 基础必备 | Pod / Service / ReplicaSet / HPA nginx workload | `nginx:1.27-alpine` | Docker Hub | 所有 nginx 示例统一使用该 tag。 |
| 基础必备 | Busybox client / volume / metrics placeholder | `busybox:1.36` | Docker Hub | 所有 busybox 示例统一使用该 tag。 |
| 基础必备 | 控制面 etcd static dependency | `quay.io/coreos/etcd:v3.5.15` | Quay | `minik8s bridge` 默认 dependency pod 使用。 |
| CNI 必备 | 自研 Mooring CNI 安装镜像 | `ghcr.io/popc0rn7/mooring-cni:v0.1.0` | GHCR | `manifest/cni/mooring.yaml` 和部署脚本默认使用。 |
| DNS | CoreDNS | `coredns/coredns:1.11.1` | Docker Hub | 启用 `--addons dns` 时使用。 |
| DNS | DNS gateway nginx | `nginx:1.27-alpine` | Docker Hub | 启用 `--addons dns` 时使用。 |
| DNS | route-proxy sidecar base | `alpine:3.20` | Docker Hub | 挂载本地 `minik8s` 二进制运行 route-proxy。 |
| Serverless | NATS event bus | `nats:2` | Docker Hub | 启用 `--addons serverless` 时使用。 |
| Serverless | Python Function runtime | `python:3.11-slim` | Docker Hub | 内联 Python Function 后端使用。 |
| Serverless demo | SAM / image workflow runtime | `minik8s/sam-cpu:demo` | 本地构建 | 由 `docker build -t minik8s/sam-cpu:demo demo/serverless/sam` 生成。 |
| GPU personal | Slurm submitter | `ghcr.io/popc0rn7/gpu-submitter:v0.1.0` | GHCR | GPU Job 个人作业使用。 |

## Prepare Images Online

在网络较好的机器上提前执行：

```bash
docker pull nginx:1.27-alpine
docker pull busybox:1.36
docker pull quay.io/coreos/etcd:v3.5.15
docker pull ghcr.io/popc0rn7/mooring-cni:v0.1.0
docker pull coredns/coredns:1.11.1
docker pull alpine:3.20
docker pull nats:2
docker pull python:3.11-slim
docker pull ghcr.io/popc0rn7/gpu-submitter:v0.1.0
```

如果需要展示 SAM/image workflow：

```bash
docker build -t minik8s/sam-cpu:demo demo/serverless/sam
```

## Export Bundle

```bash
mkdir -p /tmp/minik8s-images
docker save \
  nginx:1.27-alpine \
  busybox:1.36 \
  quay.io/coreos/etcd:v3.5.15 \
  ghcr.io/popc0rn7/mooring-cni:v0.1.0 \
  coredns/coredns:1.11.1 \
  alpine:3.20 \
  nats:2 \
  python:3.11-slim \
  ghcr.io/popc0rn7/gpu-submitter:v0.1.0 \
  minik8s/sam-cpu:demo \
  -o /tmp/minik8s-images/minik8s-acceptance-images.tar
```

如果不展示 SAM/image workflow，可去掉 `minik8s/sam-cpu:demo`。

## Import on Every Worker

每台运行 `sailer` 的 worker 都要导入同一份镜像包；不要只在控制面机器导入。

```bash
docker load -i /tmp/minik8s-acceptance-images.tar
docker images | grep -E 'nginx|busybox|etcd|mooring-cni|coredns|alpine|nats|python|sam-cpu|gpu-submitter'
```

## Publish Local Project Images

如果 `v0.1.0` 尚未发布到 GHCR，先在有网络的机器上构建并推送：

```bash
IMAGE_TAG=v0.1.0 make mooring-cni-image
IMAGE_TAG=v0.1.0 make push-mooring-cni-image

IMAGE_TAG=v0.1.0 make gpu-submitter-image
IMAGE_TAG=v0.1.0 make push-gpu-submitter-image
```
