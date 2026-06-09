# 双机公共启动流程

本文档是所有 v0.1.0 双机 case 的公共前置步骤。先完成这里，再执行 `pod.md`、`cni.md`、`service.md`、`logbook.md`。

## CASE-00：环境预检

目标：确认两台局域网机器可以互通，并具备运行 CNI/kube-proxy 的基础能力。

在 node-a：

```bash
ping -c 3 ${NODE_B_IP}
docker version
which ip
which bridge
which iptables
which nsenter
iptables-save -t nat >/tmp/minik8s-iptables-a.txt
```

在 node-b：

```bash
ping -c 3 ${NODE_A_IP}
docker version
which ip
which bridge
which iptables
which nsenter
iptables-save -t nat >/tmp/minik8s-iptables-b.txt
```

期望：

- 两台机器互 ping 成功。
- Docker client/server 正常。
- `ip`、`bridge`、`iptables`、`nsenter` 均存在。
- `iptables-save` 可用，说明网络规则检查能力正常。

失败排查：

- ping 失败时先修复局域网、防火墙或 VM 网络模式。
- Docker 失败时确认 Docker daemon 正常运行，且当前 root 环境可访问。

## 构建二进制

在两台机器的仓库根目录执行：

```bash
make build
./kubectl version --server ${HARBOR} || true
```

`version` 在控制面未启动前可以失败；这里只确认二进制已构建。

## 准备 Node YAML

确认 `manifest/node/node_a.yaml` 和 `manifest/node/node_b.yaml` 中的 `InternalIP` 与当前两台机器一致；如果不同，先按实际地址更新。Node YAML 不需要写 `spec.podCIDR`，控制面会从集群 CIDR 自动分配。

## 启动控制面

在 node-a 终端 1：

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
export MINIK8S_HARBOR=${HARBOR}
export CLUSTER_CIDR=10.244.0.0/16
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr ${CLUSTER_CIDR} \
  --node-cidr-mask-size 24
```

期望：

- bridge 输出 `bridge listening on :18080`。
- node-b 可访问 `curl -fsS ${HARBOR}/version`。
- node-b 可访问 `curl -fsS ${HARBOR}/nodes`，返回网络节点注册列表。

## 启动两个 sailer

在 node-a 终端 3：

```bash
./minik8s sailer \
  manifest/node/node_a.yaml \
  --harbor ${HARBOR}
```

在 node-b 终端 1：

```bash
./minik8s sailer \
  manifest/node/node_b.yaml \
  --harbor ${HARBOR}
```

期望：

- `sailer` 先注册节点心跳，从控制面获得 `spec.podCIDR`，自动写入本机 CNI 配置，然后同步 assigned Pods，并通过 Harbor `/nodes` 同步 VXLAN overlay。
- 两边 `ip route` 能看到对端 PodCIDR，并且 `mk8s-vxlan` FDB 指向对端 NodeIP：

```bash
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

失败排查：

- 如果没有对端 route 或 `mk8s-vxlan`，先确认 `HARBOR` 指向 node-a 局域网 IP，且 node-b 可以访问 `${HARBOR}/nodes`。
- 云主机或安全组环境下，确认 node-a/node-b 双向放通 UDP `4789`。
- 可先执行一次 `sailer --once`，便于快速暴露错误。

在 node-a 的 CLI 终端验证：

```bash
./kubectl get nodes
```

期望：

- 输出包含 `node-a` 和 `node-b`。
- 两个节点状态均为 `Ready`。
- `node-a` 的 `podCIDR` 为 `10.244.0.0/24`，`node-b` 为 `10.244.1.0/24`。

失败排查：

- 只看到 node-a：检查 node-b 的 `HARBOR` 是否能 curl 到 node-a。
- 节点短暂消失：默认 Node TTL 是 30s，确认 sailer 没退出。

## 可选：静态 route 模式

如果只想排查静态路由，可以停掉两个 sailer 后手动执行 `cni init --route`。主路径不需要这一步，sailer 会使用控制面分配的 PodCIDR 自动配置 VXLAN。

node-a：

```bash
./minik8s cni init \
  --pod-cidr 10.244.0.0/24 \
  --gateway 10.244.0.1 \
  --route 10.244.1.0/24=${NODE_B_IP}
```

node-b：

```bash
./minik8s cni init \
  --pod-cidr 10.244.1.0/24 \
  --gateway 10.244.1.1 \
  --route 10.244.0.0/24=${NODE_A_IP}
```

v0.1.0 推荐使用 Node YAML 启动 `sailer`，因为控制面分配 PodCIDR、CNI 配置、VXLAN、FDB 和 route 会自动恢复。
