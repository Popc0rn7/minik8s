# Testcase README

本文是 `docs/testcase/` 的人工验收总入口。这里只声明通用基础环境。需要临时关闭
CNI、启用 addon、设置额外环境变量或停止 worker 的 case，在对应 testcase 文档里单独说明。
除单个 testcase 明确说明外，默认使用两台 Linux 节点、fish shell、启用 CNI、启用两个
worker。

## 默认拓扑

| 角色 | 主机 | Node 名 | 默认 IP | 运行组件 |
| --- | --- | --- | --- | --- |
| 控制面 + worker | node-a | `node-a` | `192.168.1.8` | `bridge`、`sailer` |
| worker | node-b | `node-b` | `192.168.1.6` | `sailer` |

默认网络：

- Harbor API：`http://<NODE_A_IP>:18080`
- Cluster CIDR：`10.244.0.0/16`
- node-a PodCIDR：`10.244.0.0/24`
- node-b PodCIDR：`10.244.1.0/24`
- Service CIDR 默认由代码分配，示例 ClusterIP 通常从 `10.96.0.1` 开始
- 跨节点 VXLAN 需要两节点之间双向 UDP `4789`

两台机器都需要：

- Linux root shell
- Docker
- `ip`、`bridge`、`iptables`、`nsenter`
- `curl` 或 `wget`
- 当前仓库和 `manifest/` 文件

确认 `manifest/node/node_a.yaml` 和 `manifest/node/node_b.yaml` 中的
`status.addresses[type=InternalIP]` 与实际主机 IP 一致。Node YAML 不需要手写
`spec.podCIDR`，控制面会按 `CLUSTER_CIDR` 自动分配。

## fish 一键环境变量

在 node-a 和 node-b 的每个测试终端先执行这一行；如果 IP 不同，先改掉前两个值：

```fish
set -gx NODE_A_IP 192.168.1.8; set -gx NODE_B_IP 192.168.1.6; set -gx CLUSTER_CIDR 10.244.0.0/16; set -gx HARBOR http://$NODE_A_IP:18080; set -gx MINIK8S_HARBOR $HARBOR; set -gx MINIK8S_STATE_DIR .minik8s/testcase-state; set -gx MINIK8S_TOKEN minik8s
```

## 默认启动流程

两台机器都在仓库根目录构建：

```fish
make prod-deploy
```

node-a 终端 1 启动控制面：

```fish
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24
```

node-a 终端 2 启用 CNI:

```fish
./kubectl apply -f manifest/cni/mooring.yaml
```

node-a 终端 2 设置 token:

```fish
./minik8s bridge token set $MINIK8S_TOKEN --ttl 24h
```

node-a 终端 2 启动 worker：

```fish
./minik8s sailer join \
  --apiserver http://$NODE_A_IP:18080 \
  --token $MINIK8S_TOKEN \
  -f manifest/node/node_a.yaml

./minik8s sailer run
```

node-b 终端 1 启动 worker：

```fish
./minik8s sailer join \
  --apiserver http://$NODE_A_IP:18080 \
  --token $MINIK8S_TOKEN \
  -f manifest/node/node_b.yaml

./minik8s sailer run
```

node-a 测试终端检查默认环境：

```fish
./kubectl version
./kubectl get nodes
curl -fsS $HARBOR/nodes
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

期望 `node-a` 和 `node-b` 都是 `Ready`，并分别显示 `10.244.0.0/24` 和
`10.244.1.0/24`。

## testcase 入口

| 文件 | 默认环境 | 说明 |
| --- | --- | --- |
| `pod.md` | 大部分使用默认环境 | Pod 生命周期、调度、NodeLost 和删除 Node；偏离默认环境的步骤见文档内 case 前置。 |
| `service.md` | 使用默认环境 | node-a 是必测 kube-proxy/iptables 数据面入口。 |
| `replicaset.md` | 使用默认环境 | 两个 worker 建议保持运行，便于验证调度和副本恢复。 |
| `cni.md` | 使用默认环境 | CNI 主路径、manifest 激活和 route fallback；特殊步骤见文档内 case 前置。 |
| `dns.md` | 需要 addon | DNS gateway 验收；启动参数和 worker DNS 设置见该文档。 |
| `hpa.md` | 需要 metrics | HPA 和 metrics 上报验收；同步周期和等待策略见该文档。 |
| `metrics-server.md` | 需要 addon | metrics API 和 `kubectl top` 验收；addon 启动方式见该文档。 |
| `serverless-nats.md` | 需要 addon | Function/EventTrigger/Workflow + NATS 验收；NATS 环境见该文档。 |
| `logbook.md` | 可单独从 0 开始 | 验证 file/etcd Logbook 持久化，按该文档覆盖默认启动方式。 |
| `startup.md` | 可单独从 0 开始 | 验证 `init` 和 static deps pod。 |
| `addons.md` | 可单独从 0 开始 | 验证 addon manifest 与启动组合。 |
| `two-node.md` | 默认环境说明 | 更详细的双节点启动和排障步骤。 |

## 通用清理

清理分两层：API 对象由控制面删除；本机 CNI/iptables/IPAM 残留由每个 worker 的
`doctor clean` 删除。开始前先看状态，避免把 testcase 失败和环境残留混在一起。

node-a 测试终端：

```fish
./kubectl get nodes; or true
./kubectl get pods; or true
./kubectl get services; or true
./kubectl get rs; or true
./kubectl get hpa; or true
./kubectl get dns; or true
```

node-a 测试终端清理常见 API 对象：

```fish
for item in \
  "service nginx-service" \
  "service nginx-nodeport" \
  "hpa nginx-hpa" \
  "rs nginx-rs" \
  "dns example-routes" \
  "pod nginx-pod" \
  "pod nginx-pod-2" \
  "pod nginx-node-a" \
  "pod nginx-node-b" \
  "pod busybox-client" \
  "pod volume-resource-pod -n demo"
    ./kubectl delete (string split ' ' -- $item); or true
end

sleep 8
./kubectl get nodes
./kubectl get pods
```

如果要结束本轮测试或重置网络，在 node-a 和 node-b 先停止对应 `sailer run`，再分别检查
并清理本机网络状态：

```fish
./minik8s doctor network; or true
./minik8s doctor clean; or true
./minik8s doctor network; or true
```

`doctor clean` 是本机操作。两台 worker 都跑一遍，才能同时清掉 node-a 和 node-b
上的 mooring bridge、VXLAN、iptables 规则、CNI 配置和 IPAM 文件。清理后如果要继续跑
默认环境 testcase，需要重新启动对应 worker，让 `sailer` 重新写入 CNI 配置并注册网络。

注意：当前工作区已删除 `manifest/pod/pod_busybox_node_b.yaml`，但
`cni.md` 的双向跨节点 PodIP case 仍引用它。运行该 case 前需要恢复一个 node-b
busybox client manifest，或先把该 case 改写成不依赖这个文件。
