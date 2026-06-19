# Minik8s Acceptance Image Table

本文记录最终验收建议提前准备的 Docker 镜像。所有项目自带 manifest、控制面依赖和构建脚本默认值都应使用固定 tag，不使用 `latest` 或浮动 shorthand tag。

## Target Machines

最终验收按三台 identical Ubuntu 22.04 节点准备，用来模拟三节点 Minik8s 环境。三台机器应使用相同操作系统、内核、CPU 架构、Go 工具链和 Docker 配置，并都把交付产物安装在 `/opt/minik8s`。

固定节点为 node-a、node-b、node-c；node-a 作为 bridge 节点，node-b 和 node-c 作为同配置 worker。验收脚本在目标机器本地运行；先加载标准环境变量，再运行环境检查：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/00_env_check.sh
```

三台机器的基础要求：

- OS：Ubuntu 22.04。
- Kernel：三台机器内核版本必须一致；Ubuntu 22.04 常见为 `5.15.x` 或同一 HWE 内核线。
- Go：`go version` 必须匹配 `go.mod`，当前为 `go1.25.9`。
- 必要命令：`docker`、`go`、`make`、`ip`、`bridge`、`iptables`、`nsenter`、`curl`、`ping`、`ss`。
- 安装目录：`/opt/minik8s/bin/{minik8s,kubectl}`、`/opt/minik8s/scripts/acceptance`、`/opt/minik8s/manifests`、`/opt/minik8s/demo/serverless/harbor-incident-triage`。
- CNI host path：`/etc/cni/net.d` 和 `/opt/cni/bin`。
- 网络：三台机器必须能互相 `ping`，并且 node-b/node-c 能访问 node-a 的 Harbor API `TCP 18080`；三台机器之间需要放通 VXLAN `UDP 4789`。
- 端口占用：验收启动前应空闲 `TCP 153,80,2379,2380,4222,8080,8088,18080,30080` 和 `UDP 153,4789`。

## Image Table

| 层级 | 用途 | 镜像 | 来源 | 说明 |
| --- | --- | --- | --- | --- |
| 基础必备 | Pod / Service / ReplicaSet / HPA nginx workload | `nginx:1.27-alpine` | Docker Hub | 所有 nginx 示例统一使用该 tag。 |
| 基础必备 | Busybox client / volume / metrics placeholder | `busybox:1.36` | Docker Hub | 所有 busybox 示例统一使用该 tag。 |
| HPA | CPU 压测 sidecar | `polinux/stress:1.0.4` | Docker Hub | 05 HPA 验收使用真实 `stress` binary 制造 CPU load。 |
| 基础必备 | 控制面 etcd static dependency | `quay.io/coreos/etcd:v3.5.15` | Quay | `minik8s bridge` 默认 dependency pod 使用。 |
| CNI 必备 | 自研 Mooring CNI 安装镜像 | `ghcr.io/popc0rn7/mooring-cni:v0.1.0` | GHCR | `manifest/cni/mooring.yaml` 和部署脚本默认使用。 |
| DNS | CoreDNS | `coredns/coredns:1.11.1` | Docker Hub | 启用 `--addons dns` 时使用。 |
| DNS | DNS gateway nginx | `nginx:1.27-alpine` | Docker Hub | 启用 `--addons dns` 时使用。 |
| DNS | route-proxy sidecar base | `alpine:3.20` | Docker Hub | 挂载本地 `minik8s` 二进制运行 route-proxy。 |
| Serverless | NATS event bus | `nats:2` | Docker Hub | 启用 `--addons serverless` 时使用；验收环境统一预拉取该 major tag。 |
| Serverless | Python Function runtime | `python:3.11-slim` | Docker Hub | 内联 Python Function 后端使用。 |
| Serverless demo | SAM / image workflow runtime | `minik8s/sam-cpu:demo` | 本地构建 | 由 `docker build -t minik8s/sam-cpu:demo demo/serverless/sam` 生成。 |
| GPU personal | Slurm submitter | `ghcr.io/popc0rn7/gpu-submitter:v0.1.0` | GHCR | GPU Job 个人作业使用。 |

## Prepare Images Online

在网络较好的机器上提前执行：

```bash
docker pull nginx:1.27-alpine
docker pull busybox:1.36
docker pull polinux/stress:1.0.4
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
  polinux/stress:1.0.4 \
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

## Import on All Nodes

三台 identical 节点都要导入同一份镜像包；不要只在 node-a / bridge 机器导入。这样调度到任意节点的 Pod 都不会在验收时临时访问外网拉取镜像。

```bash
scp /tmp/minik8s-images/minik8s-acceptance-images.tar root@10.119.16.213:/tmp/
scp /tmp/minik8s-images/minik8s-acceptance-images.tar root@<node-b-ip>:/tmp/
scp /tmp/minik8s-images/minik8s-acceptance-images.tar root@<node-c-ip>:/tmp/

ssh root@10.119.16.213 'docker load -i /tmp/minik8s-acceptance-images.tar'
ssh root@<node-b-ip> 'docker load -i /tmp/minik8s-acceptance-images.tar'
ssh root@<node-c-ip> 'docker load -i /tmp/minik8s-acceptance-images.tar'

ssh root@10.119.16.213 "docker images | grep -E 'nginx|busybox|polinux/stress|etcd|mooring-cni|coredns|alpine|nats|python|sam-cpu|gpu-submitter'"
ssh root@<node-b-ip> "docker images | grep -E 'nginx|busybox|polinux/stress|etcd|mooring-cni|coredns|alpine|nats|python|sam-cpu|gpu-submitter'"
ssh root@<node-c-ip> "docker images | grep -E 'nginx|busybox|polinux/stress|etcd|mooring-cni|coredns|alpine|nats|python|sam-cpu|gpu-submitter'"
```

## Publish Local Project Images

如果 `v0.1.0` 尚未发布到 GHCR，先在有网络的机器上构建并推送：

```bash
IMAGE_TAG=v0.1.0 make mooring-cni-image
IMAGE_TAG=v0.1.0 make push-mooring-cni-image

IMAGE_TAG=v0.1.0 make gpu-submitter-image
IMAGE_TAG=v0.1.0 make push-gpu-submitter-image
```
