# 远程服务器部署

本文记录把 Minik8s 部署到远程 Linux 服务器的流程，并明确区分：

- **只做一次**：目标机器环境准备、目录和权限、Node YAML 的真实内网 IP。
- **每次更新版本都做**：构建二进制、构建/推送 `mooring-cni` 镜像、同步产物和
  manifests 到目标机器、重启运行中的 `bridge`/`sailer`。

课程规格仍以 [Handout.md](Handout.md) 为准。本文只描述当前代码可运行的部署方式。

## 1. 只做一次：目标机器准备

假设控制面机器为 `node-a`，worker 机器为 `node-b`，目标目录统一使用
`/opt/minik8s`。

准备运行目录和 CNI 目录：

```bash
mkdir -p /opt/minik8s /etc/cni/net.d /opt/cni/bin
chown "$USER":"$USER" /opt/minik8s
```

目标机器还需要 Docker daemon、`ip`、`bridge`、`iptables`、`nsenter` 等 Linux
网络工具和足够的 root/network 权限。`sailer` 会在启动时写入 `/etc/cni/net.d`，
并通过 `mooring-cni` 镜像安装 `/opt/cni/bin/mooring`。

确认 `manifest/node/node_a.yaml` 和 `manifest/node/node_b.yaml` 中的
`InternalIP` 已改成真实内网地址。后续所有 node-b 命令都必须使用 node-a 可访问的
Harbor 地址，不要使用 node-b 本机的 `localhost`。

## 2. 只做一次：开发机准备

先确保下面命令在开发机上都能直接成功：

```bash
ssh node-1
ssh node-2
rsync --version
docker version
```

如果 SSH 主机名不同，可以用环境变量覆盖部署目标：

```bash
export DEPLOY_NODES="root@10.0.0.11 root@10.0.0.12"
```

## 3. 每次更新：一条命令发布

构建二进制、构建并推送 `mooring-cni` 镜像、同步到所有目标机器、并让目标机器预拉镜像：

```bash
make deploy-prod
```

默认发布：

- 二进制目录：`dist/prod`
- 远端目录：`/opt/minik8s`
- CNI 镜像：`ghcr.io/popc0rn7/mooring-cni:latest`

`scripts/deploy-prod.sh` 实际执行：

- `make prod` 构建 `dist/prod/minik8s`、`dist/prod/kubectl` 和 `dist/prod/mooring`。
- 构建并推送 `ghcr.io/popc0rn7/mooring-cni:latest`。
- 用 `rsync` 同步 `minik8s`、`kubectl` 和 `manifest/` 到每个目标机器。
- 在每个目标机器上 `docker pull` 预拉 `mooring-cni` 镜像。

默认部署脚本不会把 `dist/prod/mooring` 复制到目标机器；自研 CNI 插件由
`mooring-cni` 镜像安装到 `/opt/cni/bin/mooring`。

## 4. 每次更新：拆分执行

需要分步排查时，可以拆开执行。

构建二进制：

```bash
make prod
```

产物位于：

```text
dist/prod/minik8s
dist/prod/kubectl
dist/prod/mooring
```

构建并推送 mooring CNI 安装镜像：

```bash
docker login ghcr.io
make mooring-cni-image
make push-mooring-cni-image
```

只同步已构建好的二进制和 manifests：

```bash
make deploy-prod DEPLOY_ARGS="--sync-only"
```

只同步并让目标机器预拉镜像：

```bash
make deploy-prod DEPLOY_ARGS="--pull-image"
```


## 5. 启动控制面

在 node-a 启动 `bridge`。

先初始化 static deps manifests。该命令只生成本地文件，不启动进程；`bridge`
后续会始终启动核心 `storage-etcd`，并按 `--addons` 启动可选 addon：

```bash
cd /opt/minik8s
./minik8s init --force
```

启动控制面。默认只启用核心 Pod `storage-etcd`；如需 DNS、metrics 或 serverless，
显式传 `--addons`。

```bash
cd /opt/minik8s
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr 10.244.0.0/16 \
  --node-cidr-mask-size 24
```

在另一个终端确认 Harbor API 可访问：

```bash
export NODE_A_IP=<node-a 内网 IP>
export HARBOR=http://${NODE_A_IP}:18080
cd /opt/minik8s
./kubectl version
```

## 6. 启用 mooring CNI

在 bridge 已启动后，向控制面 apply mooring CNI manifest：

```bash
cd /opt/minik8s
./kubectl apply -f manifest/cni/mooring.yaml
```

预期输出包含：

```text
namespace/kube-mooring accepted
configmap/mooring-cni-cfg created
daemonset/mooring-cni-ds created
```

这个 DaemonSet 是 Minik8s 兼容对象，用于让 `sailer` 找到 mooring CNI 安装镜像；
当前项目没有实现通用 Kubernetes DaemonSet controller。

## 7. 启动 worker

在 node-a 启动本机 `sailer`：

```bash
cd /opt/minik8s
export NODE_A_IP=<node-a 内网 IP>
export HARBOR=http://${NODE_A_IP}:18080
./minik8s sailer \
  manifest/node/node_a.yaml \
  --harbor $HARBOR
```

在 node-b 启动 worker `sailer`：

```bash
cd /opt/minik8s
export NODE_A_IP=<node-a 内网 IP>
export HARBOR=http://${NODE_A_IP}:18080
./minik8s sailer \
  manifest/node/node_b.yaml \
  --harbor $HARBOR
```

`sailer` 启动时会：

1. 向 bridge 注册 Node heartbeat。
2. 获取控制面分配的 `spec.podCIDR`。
3. 读取 `kube-mooring/mooring-cni-cfg` 和 `kube-mooring/mooring-cni-ds`。
4. 通过 `ghcr.io/popc0rn7/mooring-cni:latest` 安装 `/opt/cni/bin/mooring`。
5. 写入 `/etc/cni/net.d/10-mooring.conf`。
6. 启用内置 netagent，同步 VXLAN/FDB/route。

更新二进制后，正在运行的 `bridge` 和 `sailer` 需要手动停止并重新启动，才能使用新版本。

## 8. 验证部署

在 node-a 的 CLI 终端：

```bash
cd /opt/minik8s
./kubectl get nodes
```

预期：

- `node-a` 和 `node-b` 均出现。
- 状态为 `Ready`。
- 两个节点获得不同 PodCIDR，例如 `10.244.0.0/24` 和 `10.244.1.0/24`。

在每台 worker 上检查 mooring CNI：

```bash
ls -l /opt/cni/bin/mooring
cat /etc/cni/net.d/10-mooring.conf
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

部署 Pod 并查看调度结果：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./kubectl get pods
```

网络实验后需要清理本地 mooring 网络状态时，在每台 worker 上执行：

```bash
./minik8s doctor clean
```
