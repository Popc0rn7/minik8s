# 双机公共启动流程

本文档是所有 v0.1.0 双机 case 的公共前置步骤。先完成这里，再执行 `pod.md`、`cni.md`、`service.md`、`etcd.md`。

## CASE-00：环境预检

目标：确认两台局域网机器可以互通，并具备运行 CNI/kube-proxy 的基础能力。

在 node-a：

```bash
ping -c 3 ${NODE_B_IP}
docker version
which ip
which iptables
which nsenter
iptables-save -t nat >/tmp/minik8s-iptables-a.txt
```

在 node-b：

```bash
ping -c 3 ${NODE_A_IP}
docker version
which ip
which iptables
which nsenter
iptables-save -t nat >/tmp/minik8s-iptables-b.txt
```

期望：

- 两台机器互 ping 成功。
- Docker client/server 正常。
- `ip`、`iptables`、`nsenter` 均存在。
- `iptables-save` 可用，说明网络规则检查能力正常。

失败排查：

- ping 失败时先修复局域网、防火墙或 VM 网络模式。
- Docker 失败时确认 Docker daemon 正常运行，且当前 root 环境可访问。

## 构建二进制

在两台机器的仓库根目录执行：

```bash
make build
./minik8s version --server ${KUBEHARBOR} || true
```

`version` 在控制面未启动前可以失败；这里只确认二进制已构建。

## 初始化 CNI

在 node-a：

```bash
unset MINIK8S_CNI_DISABLED
./minik8s cni init \
  --pod-cidr ${POD_CIDR_A} \
  --gateway 10.244.0.1
./minik8s doctor network
```

在 node-b：

```bash
unset MINIK8S_CNI_DISABLED
./minik8s cni init \
  --pod-cidr ${POD_CIDR_B} \
  --gateway 10.244.1.1
./minik8s doctor network
```

期望：

- `doctor network` 显示 `config: present`。
- `bridge: mk8s0`。
- node-a 的 `podCIDR` 为 `10.244.0.0/24`，node-b 为 `10.244.1.0/24`。

## 启动控制面

在 node-a 终端 1：

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
./minik8s kubebridge --listen :18080
```

期望：

- kubebridge 输出 `kubebridge listening on :18080`。
- node-b 可访问 `curl -fsS ${KUBEHARBOR}/version`。
- node-b 可访问 `curl -fsS ${KUBEHARBOR}/nodes`，返回网络节点注册列表。

## 启动两个 kubesailer

在 node-a 终端 3：

```bash
./minik8s kubesailer \
  --node-name node-a \
  --kubeharbor ${KUBEHARBOR} \
  --node-ip ${NODE_A_IP} \
  --pod-cidr ${POD_CIDR_A}
```

在 node-b 终端 1：

```bash
./minik8s kubesailer \
  --node-name node-b \
  --kubeharbor ${KUBEHARBOR} \
  --node-ip ${NODE_B_IP} \
  --pod-cidr ${POD_CIDR_B}
```

期望：

- `kubesailer` 同时注册节点心跳、同步 assigned Pods，并通过 Kubeharbor `/nodes` 同步 host-gw route。
- 两边 `ip route` 能看到对端 PodCIDR：

```bash
ip route | grep 10.244
```

失败排查：

- 如果没有对端 route，先确认 `KUBEHARBOR` 指向 node-a 局域网 IP，且 node-b 可以访问 `${KUBEHARBOR}/nodes`。
- 可先执行一次 `kubesailer --once`，便于快速暴露错误。

在 node-a 的 CLI 终端验证：

```bash
./minik8s get nodes
```

期望：

- 输出包含 `node-a` 和 `node-b`。
- 两个节点状态均为 `Ready`。

失败排查：

- 只看到 node-a：检查 node-b 的 `KUBEHARBOR` 是否能 curl 到 node-a。
- 节点短暂消失：默认 Node TTL 是 30s，确认 kubesailer 没退出。

## 可选：静态 route 模式

如果启动 `kubesailer` 时不带 `--node-ip` 和 `--pod-cidr`，也可以在 CNI 初始化时写静态 route。

node-a：

```bash
./minik8s cni init \
  --pod-cidr ${POD_CIDR_A} \
  --gateway 10.244.0.1 \
  --route ${POD_CIDR_B}=${NODE_B_IP}
```

node-b：

```bash
./minik8s cni init \
  --pod-cidr ${POD_CIDR_B} \
  --gateway 10.244.1.1 \
  --route ${POD_CIDR_A}=${NODE_A_IP}
```

v0.1.0 推荐使用带 `--node-ip` 和 `--pod-cidr` 的 `kubesailer`，因为 route 会周期性恢复。
