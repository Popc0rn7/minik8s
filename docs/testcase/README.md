# Testcase README

本文是 `docs/testcase/` 的人工验收总入口。测试规格以
[`docs/Handout.md`](../Handout.md) 为准；本文和各 feature testcase 只描述当前代码
真实可运行的验收路径。不要把未落地能力写成已通过能力。

## 默认拓扑

除单个 testcase 明确说明外，默认使用两台 Linux root 节点、fish shell、启用 mooring
CNI、启用两个 worker。

| 角色 | 主机 | Node 名 | 默认 IP | 运行组件 |
| --- | --- | --- | --- | --- |
| 控制面 + worker | node-a | `node-a` | `192.168.1.8` | `bridge`、`sailer` |
| worker | node-b | `node-b` | `192.168.1.6` | `sailer` |

默认网络：

- Harbor API：`http://<NODE_A_IP>:18080`
- Cluster CIDR：`10.244.0.0/16`
- node-a PodCIDR：`10.244.0.0/24`
- node-b PodCIDR：`10.244.1.0/24`
- Service CIDR 默认从 `10.96.0.0/12` 分配，示例 ClusterIP 通常从 `10.96.0.1` 开始
- 跨节点 CNI 需要两节点之间双向 UDP `4789`

两台机器都需要 Docker、`ip`、`bridge`、`iptables`、`nsenter`、`curl` 或 `wget`。
确认 `manifest/node/node_a.yaml` 和 `manifest/node/node_b.yaml` 中的
`status.addresses[type=InternalIP]` 与实际主机 IP 一致。Node YAML 不需要手写
`spec.podCIDR`，控制面会按 `CLUSTER_CIDR` 自动分配。

如果 root 的 fish 配置设置了代理，确认 `NO_PROXY/no_proxy` 覆盖
`192.168.0.0/16`、`10.244.0.0/16` 和 `10.96.0.0/12`。访问 Harbor LAN 地址时优先使用
`curl --noproxy '*'`，避免代理导致 502。

## 默认启动流程

node-a 和 node-b 的每个测试终端先设置变量；如果 IP 不同，先改前两个值：

```fish
set -gx NODE_A_IP 192.168.1.8; set -gx NODE_B_IP 192.168.1.6; set -gx CLUSTER_CIDR 10.244.0.0/16; set -gx HARBOR http://$NODE_A_IP:18080; set -gx MINIK8S_HARBOR $HARBOR; set -gx MINIK8S_STATE_DIR .minik8s/testcase-state; set -gx MINIK8S_TOKEN minik8s
set -e MINIK8S_CNI_DISABLED
```

两台机器都在仓库根目录构建和同步产物：

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

node-a 测试终端启用 mooring CNI 并设置 bootstrap token：

```fish
./kubectl apply -f manifest/cni/mooring.yaml
./minik8s bridge token set $MINIK8S_TOKEN --ttl 24h
```

node-a worker 终端：

```fish
./minik8s sailer join \
  --apiserver http://$NODE_A_IP:18080 \
  --token $MINIK8S_TOKEN \
  -f manifest/node/node_a.yaml

./minik8s sailer run
```

node-b worker 终端：

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
curl --noproxy '*' -fsS $HARBOR/nodes
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

期望 `node-a` 和 `node-b` 均为 `Ready`，并分别显示 `10.244.0.0/24` 和
`10.244.1.0/24`。

## Handout 覆盖矩阵

| Handout 能力 | 当前 testcase | 状态 |
| --- | --- | --- |
| Pod lifecycle、YAML、namespace、labels、volume、resource、restart | `pod.md` | 必测 |
| CNI Pod IP、同节点和跨节点 PodIP 通信 | `cni.md` | 必测，依赖 Linux 网络权限 |
| Service ClusterIP、NodePort、endpoints、动态更新、清理 | `service.md` | 必测，node-a proxy 为主入口 |
| ReplicaSet desired/current、补齐、缩容、级联删除 | `replicaset.md` | 必测 |
| Node、Navigator、多机、NodeLost 容错 | `two-node.md`、`pod.md` | 必测 |
| 控制面状态持久化和恢复 | `logbook.md`、`startup.md` | 必测 |
| HPA 和 metrics | `hpa.md`、`metrics-server.md` | 当前已有能力，按 addon 测 |
| DNS host/path gateway | `dns.md` | 当前已有能力，按 addon 测 |
| Serverless Function/EventTrigger/Workflow/NATS | `serverless-nats.md` | 当前最小闭环；完整 scale-to-0 未实现 |
| PV/PVC、GPU、Security Context、MicroService mesh | 无可通过 testcase | 未实现或未纳入当前验收 |

## Testcase 入口

| 文件 | 默认环境 | 说明 |
| --- | --- | --- |
| `two-node.md` | 可单独从 0 开始 | 双节点预检、启动、Ready、CNI 基线。 |
| `pod.md` | 大部分使用默认环境 | Pod 生命周期、调度、NodeLost 和删除 Node；偏离默认环境的步骤见文档内 case 前置。 |
| `cni.md` | 使用默认环境 | mooring CNI 主路径、manifest 激活、route fallback。 |
| `service.md` | 使用默认环境 | Service endpoints、ClusterIP、NodePort、负载均衡、iptables 清理。 |
| `replicaset.md` | 使用默认环境 | ReplicaSet 对 Pod 的创建、收敛和级联删除。 |
| `logbook.md` | 可单独从 0 开始 | file/etcd Logbook、控制面重启恢复。 |
| `startup.md` | 可单独从 0 开始 | `init`、static deps pod、bridge dependency startup。 |
| `addons.md` | 可单独从 0 开始 | addon manifest 与 `--addons` readiness。 |
| `metrics-server.md` | 需要 metrics addon | metrics API 和 `kubectl top`。 |
| `hpa.md` | 需要 metrics 样本 | HPA 根据 Docker metrics 调整 ReplicaSet。 |
| `dns.md` | 需要 dns addon | DNS 对象和 gateway host/path routing。 |
| `serverless-nats.md` | 需要 serverless addon | Function/EventTrigger/Workflow + NATS publish。 |
| `testing-agent-prompt.md` | 辅助文档 | 给测试代理的执行、证据和恢复要求。 |

## TODO 验证进度

按推荐执行顺序跟踪人工 testcase 验证状态。只有完成对应文档中的主路径验证、记录证据并
恢复环境后，才把条目标为已完成。

- [x] `startup.md`：`init`、static deps pod、bridge dependency startup。
- [x] `two-node.md`：双节点预检、启动、Ready、CNI 基线。
- [ ] `pod.md`：Pod lifecycle、调度、NodeLost 和删除 Node；新增多容器 localhost 与共享 volume 后需补测。
- [x] `cni.md`：mooring CNI、Pod IP、同节点和跨节点通信。
- [ ] `service.md`：Service endpoints、ClusterIP、NodePort、负载均衡、iptables 清理；新增集群外 NodePort 证据后需补测。
- [ ] `replicaset.md`：ReplicaSet 创建、补齐、缩容和级联删除；新增跨节点分布与 NodeLost 补副本后需补测。
- [x] `logbook.md`：file/etcd Logbook、对象持久化和 bridge 重启恢复；LOGBOOK-06 为可选工程增强项，尚未记录通过。
- [x] `addons.md`：addon manifest、`--addons` readiness 和 doctor 状态。
- [ ] `metrics-server.md`：metrics API 和 `kubectl top`。
- [ ] `hpa.md`：HPA metrics、扩容和缩容；新增扩缩容速度时间点记录后需补测。
- [ ] `dns.md`：自动 cluster DNS 注入、Service FQDN 和 gateway host/path routing；新增 Pod 内域名与多 path 输出证据后需补测。
- [ ] `serverless-nats.md`：Function/EventTrigger/Workflow + NATS publish；Serverless 正在开发中，不纳入当前基础能力整理。

## Testcase 整理建议

本节只整理基础功能和非 Serverless/GPU 的验收覆盖。已经明确不作为当前实现目标的
PV/PVC、Security Context、MicroService mesh 不在此处展开。

### 新增后待补测的检验项

- `metrics-server.md` 仍需要补真实运行记录：addon
  启动、metrics API、Pod/Node 样本和 `kubectl top`。
- `pod.md` 新增 `POD-02B`，覆盖多容器 localhost 通信和同 Pod 共享 volume，待真实环境补测。
- `service.md` 新增 `SVC-03B`，覆盖从 node-b 或第三方机器访问 node-a NodePort，待补测。
- `replicaset.md` 新增 `RS-05B`，覆盖 owned Pods 跨节点分布和 NodeLost 后补副本，待补测。
- `hpa.md` 新增 `HPA-04B`，覆盖扩缩容速度和冷却窗口的时间点记录，待补测。
- `dns.md` 新增 `DNS-02B`，覆盖同一 host 下不同 path 转发到不同响应后端；`DNS-04`
  补充自动 cluster DNS 注入、Service FQDN 和 Pod 内 `/etc/resolv.conf` 证据，待补测。
- `logbook.md` 已覆盖主要恢复路径；如仍保留 LOGBOOK-06，需要补 watch/并发检查的通过
  记录，或把它降级为工程增强项。

### 可合并的语义重合项

- `startup.md` 与 `addons.md` 都覆盖 `minik8s init` 生成依赖 manifest 和 bridge 启动依赖；
  可保留 `startup.md` 验证核心 storage-etcd，`addons.md` 只验证可选 addon readiness。
- `two-node.md` 的 Node Ready、PodCIDR 和网络基线与 `cni.md` 的 CNI 环境基线有重合；
  可让 `two-node.md` 负责集群启动，`cni.md` 直接复用默认环境并专注 PodIP 通信。
- `pod.md` 的 NodeLost、Node 删除级联与 `service.md` 的 endpoint 动态更新有重合；
  可在 `pod.md` 验证 Node/Pod 状态级联，在 `service.md` 只验证 Service endpoints 被刷新。
- `hpa.md` 依赖 metrics 样本，和 `metrics-server.md` 的 metrics API 有重合；建议
  `metrics-server.md` 只证明样本可查，`hpa.md` 只证明这些样本驱动 ReplicaSet 扩缩容。
- `logbook.md` 的 bridge 重启恢复和各 feature 的对象 CRUD 后状态展示有重合；保留
  `logbook.md` 作为唯一控制面恢复验收，各 feature testcase 不重复做控制面重启。

### Handout 提到但当前未覆盖或仍需实测的项

- metrics-server 真实运行记录。
- LOGBOOK-06 watch/并发检查；当前作为可选工程增强项，不阻塞基础通过记录。
- 新增的 `POD-02B`、`SVC-03B`、`RS-05B`、`HPA-04B`、`DNS-02B` 需要跑完后再重新打勾。

最近人工验证记录：

- 2026-06-15 `service.md`：SVC-01 到 SVC-06 通过。此前 SVC-03 的 NodePort 失败根因是
  宿主机残留同 PodCIDR 的旧 `cni0` route，导致 `10.244.0.0/24` 流量没有走 `mk8s0`；
  当前 mooring/netagent 会刷新本地 PodCIDR route 到 `mk8s0`。此前 SVC-06 的
  `MK8S-SVC-*` 残留通过 kube-proxy 删除入口规则时循环删除重复规则修复。
- 2026-06-14 `logbook.md`：LOGBOOK-01 到 LOGBOOK-05 通过。当前 bridge 重启语义是
  私有 dependency sailer/etcd 会随 bridge 重启，etcd 数据依赖
  `.minik8s/state/bridge-deps/etcd` 持久化目录恢复；公开 `sailer run` 在 Harbor 短暂
  不可用期间应保持运行，通过 `sailer-sync`、`netagent-sync` warning 重试，并在 bridge
  恢复后自动恢复 Node Ready、Pod、ReplicaSet 和 Service endpoints。

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
./kubectl get functions; or true
./kubectl get eventtriggers; or true
./kubectl get workflows; or true
```

清理常见 API 对象：

```fish
for item in \
  "service nginx-service" \
  "service nginx-nodeport" \
  "hpa nginx-hpa" \
  "rs nginx-rs" \
  "dns example-routes" \
  "function echo" \
  "eventtrigger echo-events" \
  "workflow echo-chain" \
  "pod nginx-pod" \
  "pod nginx-pod-2" \
  "pod nginx-node-a" \
  "pod nginx-node-b" \
  "pod busybox-node-a" \
  "pod busybox-node-b" \
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

`doctor clean` 是本机操作。两台 worker 都跑一遍，才能同时清掉 mooring bridge、
VXLAN、iptables 规则、CNI 配置和 IPAM 文件。清理后如果要继续跑默认环境 testcase，
需要重新启动对应 worker，让 `sailer` 重新写入 CNI 配置并注册网络。
