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
sudo iptables-save -t nat >/tmp/minik8s-iptables-a.txt
```

在 node-b：

```bash
ping -c 3 ${NODE_A_IP}
docker version
which ip
which iptables
which nsenter
sudo iptables-save -t nat >/tmp/minik8s-iptables-b.txt
```

期望：

- 两台机器互 ping 成功。
- Docker client/server 正常。
- `ip`、`iptables`、`nsenter` 均存在。
- `iptables-save` 可用，说明 sudo 权限足够。

失败排查：

- ping 失败时先修复局域网、防火墙或 VM 网络模式。
- Docker 失败时确认当前用户可访问 Docker daemon，或后续命令统一使用 `sudo`。

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
sudo env MINIK8S_PLAIN=1 NO_COLOR=1 ./minik8s cni init \
  --pod-cidr ${POD_CIDR_A} \
  --gateway 10.244.0.1
sudo ./minik8s doctor network
```

在 node-b：

```bash
unset MINIK8S_CNI_DISABLED
sudo env MINIK8S_PLAIN=1 NO_COLOR=1 ./minik8s cni init \
  --pod-cidr ${POD_CIDR_B} \
  --gateway 10.244.1.1
sudo ./minik8s doctor network
```

期望：

- `doctor network` 显示 `config: present`。
- `bridge: mk8s0`。
- node-a 的 `podCIDR` 为 `10.244.0.0/24`，node-b 为 `10.244.1.0/24`。

## 启动控制面和网络注册表

在 node-a 终端 1：

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
./minik8s kubebridge --listen :18080 --service-sync-interval 5s
```

在 node-a 终端 2：

```bash
./minik8s net-registry --listen :8088
```

期望：

- kubebridge 输出 `kubebridge listening on :18080`。
- net-registry 输出 `net-registry listening on :8088`。
- node-b 可访问 `curl -fsS ${KUBEHARBOR}/version`。

## 启动 netd

在 node-a 终端 3：

```bash
sudo env MINIK8S_PLAIN=1 NO_COLOR=1 ./minik8s netd \
  --node-name node-a \
  --node-ip ${NODE_A_IP} \
  --pod-cidr ${POD_CIDR_A} \
  --registry ${REGISTRY}
```

在 node-b 终端 1：

```bash
sudo env MINIK8S_PLAIN=1 NO_COLOR=1 ./minik8s netd \
  --node-name node-b \
  --node-ip ${NODE_B_IP} \
  --pod-cidr ${POD_CIDR_B} \
  --registry ${REGISTRY}
```

期望：

- 两边 `ip route` 能看到对端 PodCIDR：

```bash
ip route | grep 10.244
```

失败排查：

- 如果没有对端 route，先确认 `REGISTRY` 指向 node-a 局域网 IP。
- 可先执行一次 `netd --once`，便于快速暴露错误。

## 启动两个 kubesailer

在 node-a 终端 4：

```bash
sudo env MINIK8S_PLAIN=1 NO_COLOR=1 ./minik8s kubesailer \
  --node-name node-a \
  --kubeharbor ${KUBEHARBOR}
```

在 node-b 终端 2：

```bash
sudo env MINIK8S_PLAIN=1 NO_COLOR=1 ./minik8s kubesailer \
  --node-name node-b \
  --kubeharbor ${KUBEHARBOR}
```

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

如果不运行 `net-registry/netd`，也可以在 CNI 初始化时写静态 route。

node-a：

```bash
sudo ./minik8s cni init \
  --pod-cidr ${POD_CIDR_A} \
  --gateway 10.244.0.1 \
  --route ${POD_CIDR_B}=${NODE_B_IP}
```

node-b：

```bash
sudo ./minik8s cni init \
  --pod-cidr ${POD_CIDR_B} \
  --gateway 10.244.1.1 \
  --route ${POD_CIDR_A}=${NODE_A_IP}
```

v0.1.0 推荐使用 `netd`，因为 route 会周期性恢复。
