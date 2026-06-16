# 远程服务器部署

本文记录把 Minik8s 部署到远程 Linux 服务器的流程，并明确区分：

- **只做一次**：目标机器环境准备、目录和权限、确认控制面内网地址。
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

后续所有 node-b 命令都必须使用 node-a 可访问的 Harbor 地址，不要使用 node-b 本机的
`localhost`。`sailer join` 默认会按访问该 Harbor 地址的 UDP 路由探测本机 node IP；
如机器多网卡或探测结果不符合预期，可显式传 `--node-ip <本机内网 IP>`。

## 2. 只做一次：构建/部署机器准备

目标机器是 Ubuntu 22 且已安装 Go 1.25.9 时，可以直接在仓库根目录本机构建二进制，
不再需要通过 Docker 容器交叉构建：

```bash
make prod
```

`make prod` 和 `make prod-build` 都是 `make build` 的本地别名，产物位于仓库根目录：

```text
./minik8s
./kubectl
.minik8s/cni/bin/mooring
```

如果需要从当前机器同步到另一台机器，再确认下面命令可用：

```bash
ssh root@10.119.16.213 -i ~/.ssh/id_ed25519_minik8s
rsync --version
docker version
```

远端同步必须显式给出目标和 SSH 参数，不再默认假设 `node-1`/`node-2`：

```bash
export DEPLOY_NODES="root@10.119.16.213"
export SSH_OPTS="-i ~/.ssh/id_ed25519_minik8s"
```

## 3. 每次更新：本机发布

在目标机器本机运行时，更新代码后直接构建：

```bash
make prod
```

需要更新 `mooring-cni` 镜像时，单独构建并推送固定 tag：

```bash
docker login ghcr.io
make prod-cni
```

默认本机发布：

- 二进制目录：仓库根目录
- 远端目录：`/opt/minik8s`
- CNI 镜像：`ghcr.io/popc0rn7/mooring-cni:v0.1.0`

## 4. 可选：远端同步

从开发机同步到目标机器时，显式传 SSH 目标：

```bash
make deploy-prod DEPLOY_ARGS="--sync-only" \
  DEPLOY_NODES="root@10.119.16.213" \
  SSH_OPTS="-i ~/.ssh/id_ed25519_minik8s"
```

`scripts/deploy-prod.sh` 只在显式给出 `DEPLOY_NODES` 后执行远端动作：

- 用当前本机产物 `./minik8s`、`./kubectl` 同步到目标机器的 `/opt/minik8s/bin/`。
- 同步 `manifest/`、`scripts/acceptance/` 和 serverless triage demo 到目标机器。
- 带 `--pull-image` 时，在目标机器上 `docker pull ghcr.io/popc0rn7/mooring-cni:v0.1.0`。

默认部署脚本不会把 `.minik8s/cni/bin/mooring` 复制到目标机器；自研 CNI 插件由
`mooring-cni` 镜像安装到 `/opt/cni/bin/mooring`。

同步后可以用完整 SSH 命令验证目标目录和二进制：

```bash
make prod-verify SSH="ssh root@10.119.16.213 -i ~/.ssh/id_ed25519_minik8s"
```

## 5. 每次更新：拆分执行

需要分步排查时，可以拆开执行。

构建二进制：

```bash
make prod
```

产物位于：

```text
./minik8s
./kubectl
.minik8s/cni/bin/mooring
```

构建并推送 mooring CNI 安装镜像：

```bash
docker login ghcr.io
make mooring-cni-image
make push-mooring-cni-image
```

只同步已构建好的二进制和 manifests：

```bash
make deploy-prod DEPLOY_ARGS="--sync-only" \
  DEPLOY_NODES="root@10.119.16.213" \
  SSH_OPTS="-i ~/.ssh/id_ed25519_minik8s"
```

只同步并让目标机器预拉镜像：

```bash
make deploy-prod DEPLOY_ARGS="--pull-image" \
  DEPLOY_NODES="root@10.119.16.213" \
  SSH_OPTS="-i ~/.ssh/id_ed25519_minik8s"
```


## 6. 启动控制面

在 node-a 启动 `bridge`。

先初始化 static deps manifests。该命令只生成本地文件，不启动进程；`bridge`
后续会始终启动核心 `storage-etcd`，并按 `--addons` 启动可选 addon：

```bash
cd /opt/minik8s
./minik8s init --force
```

设置 worker 加入集群用的临时 bootstrap token。该 token 存在 node-a 本机
`.minik8s/state/bootstrap-token.json`，`sailer join` 时使用；过期后可重新 set。

```bash
cd /opt/minik8s
export BOOTSTRAP_TOKEN=$(openssl rand -hex 24)
./minik8s bridge token set "$BOOTSTRAP_TOKEN" --ttl 24h
./minik8s bridge token status
```

启动控制面。默认只启用核心 Pod `storage-etcd`；如需 DNS、metrics 或 serverless，
显式传 `--addons`。`bridge` 启动后会根据 `--listen` 写入本机
`.minik8s/config.json`；因此 node-a 上的 `./kubectl` 后续不需要额外传 Harbor 地址。

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

如果 `kubectl` 提示 Harbor 未配置，确认 `bridge` 已在当前目录生成
`.minik8s/config.json`。临时排查时也可以用
`MINIK8S_HARBOR=http://127.0.0.1:18080 ./kubectl version` 覆盖本地配置。

## 7. 启用 mooring CNI

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

## 8. 加入并启动 worker

`sailer join` 会向控制面注册 Node、获取 node token 和 PodCIDR，并写入两份本地文件：

- `.minik8s/state/sailer.json`：后续 `sailer run` 使用的 worker 身份和 node token。
- `.minik8s/config.json`：本机 `./kubectl` 默认使用的 Harbor 地址。

`--node-name` 可选；不传时会生成 `node-xxxxx`。建议人工双机验收时显式传
`--node-name node-a` / `--node-name node-b`，便于阅读调度结果。

`join` 成功后 Node 会先以 `Unknown` 出现在控制面；只有 `sailer run` 启动并成功心跳后，
该 Node 才会变为 `Ready` 并进入跨节点网络注册。
正常停止 `sailer run`（例如 `Ctrl-C` 或 SIGTERM）会主动把 Node 标记为 `Unknown`；
如果进程被 `kill -9` 强杀，控制面会在默认 30s Node TTL 后通过 liveness loop 标记为
`Unknown`。

在 node-a 加入并启动本机 `sailer`：

```bash
cd /opt/minik8s
export BOOTSTRAP_TOKEN=<与 node-a bridge token set 相同的 token>
./minik8s sailer join \
  --apiserver http://127.0.0.1:18080 \
  --token "$BOOTSTRAP_TOKEN" \
  --node-name node-a
./minik8s sailer run
```

在 node-b 加入并启动 worker `sailer`。这里的 `--apiserver` 必须使用 node-b 能访问的
node-a 内网地址，不能写 node-b 本机的 `localhost`。

```bash
cd /opt/minik8s
export NODE_A_IP=<node-a 内网 IP>
export HARBOR=http://${NODE_A_IP}:18080
export BOOTSTRAP_TOKEN=<与 node-a bridge token set 相同的 token>
./minik8s sailer join \
  --apiserver "$HARBOR" \
  --token "$BOOTSTRAP_TOKEN" \
  --node-name node-b
./minik8s sailer run
```

`sailer join` 成功后，如果只是在更新二进制或重启 worker，后续直接运行：

```bash
cd /opt/minik8s
./minik8s sailer run
```

兼容旧调试路径仍可直接运行
`./minik8s sailer manifest/node/node_b.yaml --harbor "$HARBOR"`，但远程部署主路径建议使用
`join`/`run`，避免每次手工传 Harbor 地址和 bootstrap 信息。

`sailer run` 启动时会：

1. 向 bridge 注册 Node heartbeat。
2. 获取控制面分配的 `spec.podCIDR`。
3. 读取 `kube-mooring/mooring-cni-cfg` 和 `kube-mooring/mooring-cni-ds`。
4. 通过 `ghcr.io/popc0rn7/mooring-cni:v0.1.0` 安装 `/opt/cni/bin/mooring`。
5. 写入 `/etc/cni/net.d/10-mooring.conf`。
6. 启用内置 netagent，同步 VXLAN/FDB/route。

更新二进制后，正在运行的 `bridge` 和 `sailer` 需要手动停止并重新启动，才能使用新版本。
worker 已经 join 过后，重启 worker 只需要重新执行 `./minik8s sailer run`。

## 9. 验证部署

在 node-a 的 CLI 终端：

```bash
cd /opt/minik8s
./kubectl get nodes
```

预期：

- `node-a` 和 `node-b` 均出现。
- 状态为 `Ready`。
- 两个节点获得不同 PodCIDR，例如 `10.244.0.0/24` 和 `10.244.1.0/24`。

如果需要移除 worker，先停止该 worker 上的 `sailer run`，再在 node-a 删除 Node：

```bash
./kubectl delete node node-b
./kubectl get nodes
```

Node 删除会级联删除已经调度到该 Node 的 Pod，并撤销旧 node token。恢复该 worker 时需要重新执行
`sailer join`，然后再运行 `sailer run`。

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
cd /opt/minik8s
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./kubectl get pods
```

网络实验后需要清理本地 mooring 网络状态时，在每台 worker 上执行：

```bash
./minik8s doctor clean
```
