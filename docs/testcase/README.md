# Testcase README

本文是 `docs/testcase/` 的人工验收总入口。测试规格以
[`docs/Handout.md`](../Handout.md) 为准；本文和各 feature testcase 只描述当前代码
真实可运行的验收路径。不要把未落地能力写成已通过能力。

## 默认拓扑

除单个 testcase 明确说明外，默认使用三台 Linux root 节点、fish shell、启用 mooring
CNI、启用三个 worker。

| 角色 | 主机 | Node 名 | 默认 IP | 运行组件 |
| --- | --- | --- | --- | --- |
| 控制面 + worker | node-a | `node-a` | `192.168.1.4` | `bridge`、`sailer` |
| worker | node-b | `node-b` | `192.168.1.10` | `sailer` |
| worker | node-c | `node-c` | `192.168.1.15` | `sailer` |

默认网络：

- Harbor API：`http://<NODE_A_IP>:18080`
- Cluster CIDR：`10.244.0.0/16`
- node-a PodCIDR：`10.244.0.0/24`
- node-b PodCIDR：`10.244.1.0/24`
- node-c PodCIDR：`10.244.2.0/24`
- Service CIDR 默认从 `10.96.0.0/12` 分配，示例 ClusterIP 通常从 `10.96.0.1` 开始
- 跨节点 CNI 需要三节点之间双向 UDP `4789`

三台机器都需要 Docker、`ip`、`bridge`、`iptables`、`nsenter`、`curl` 或 `wget`。
`sailer join` 默认按访问 Harbor 的 UDP 路由探测 node IP；多网卡环境下如果探测结果
不符合预期，显式传 `--node-ip <本机内网 IP>`。`spec.podCIDR` 由控制面按
`CLUSTER_CIDR` 自动分配。

如果 root 的 fish 配置设置了代理，确认 `NO_PROXY/no_proxy` 覆盖
`192.168.0.0/16`、`10.244.0.0/16` 和 `10.96.0.0/12`。访问 Harbor LAN 地址时优先使用
`curl --noproxy '*'`，避免代理导致 502。

## 默认启动流程

node-a、node-b 和 node-c 的每个测试终端先设置变量；如果 IP 不同，先改前三个值：

```fish
set -gx NODE_A_IP 192.168.1.4; set -gx NODE_B_IP 192.168.1.10; set -gx NODE_C_IP 192.168.1.15; set -gx CLUSTER_CIDR 10.244.0.0/16; set -gx HARBOR http://$NODE_A_IP:18080; set -gx MINIK8S_HARBOR $HARBOR; set -gx MINIK8S_STATE_DIR .minik8s/testcase-state; set -gx MINIK8S_TOKEN minik8s
set -e MINIK8S_CNI_DISABLED
```

三台机器都在仓库根目录构建和同步产物：

```fish
make prod-deploy
```

node-a 终端 1 启动控制面：

```fish
./bin/minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24
```

node-a 测试终端启用 mooring CNI 并设置 bootstrap token：

```fish
./bin/kubectl apply -f manifests/cni/mooring.yaml
./bin/minik8s bridge token set $MINIK8S_TOKEN --ttl 24h
```

node-a worker 终端：

```fish
./bin/minik8s sailer join \
  --apiserver http://$NODE_A_IP:18080 \
  --token $MINIK8S_TOKEN \
  --node-name node-a

./bin/minik8s sailer run
```

node-b worker 终端：

```fish
./bin/minik8s sailer join \
  --apiserver http://$NODE_A_IP:18080 \
  --token $MINIK8S_TOKEN \
  --node-name node-b

./bin/minik8s sailer run
```

node-c worker 终端：

```fish
./bin/minik8s sailer join \
  --apiserver http://$NODE_A_IP:18080 \
  --token $MINIK8S_TOKEN \
  --node-name node-c

./bin/minik8s sailer run
```

node-a 测试终端检查默认环境：

```fish
./bin/kubectl version
./bin/kubectl get nodes
curl --noproxy '*' -fsS $HARBOR/nodes
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

期望 `node-a`、`node-b` 和 `node-c` 均为 `Ready`，并分别显示 `10.244.0.0/24`、
`10.244.1.0/24` 和 `10.244.2.0/24`。

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
| Serverless Function/EventTrigger/Workflow/NATS | `serverless.md`、`serverless-nats.md`、`serverless-sam.md`、`serverless-image-workflow.md` | 当前教学闭环；复杂 SAM/image workflow demo 依赖预构建镜像和本地数据 |
| Job GPU/Slurm 提交、状态、日志、隔离 | `job-gpu.md` | 当前最小闭环；真机验证依赖 SSH 凭据、submitter 镜像和 Harbor endpoint 配置 |
| PV/PVC、Security Context、MicroService mesh | 无可通过 testcase | 未实现或未纳入当前验收 |

## Testcase 入口

| 文件 | 默认环境 | 说明 |
| --- | --- | --- |
| `two-node.md` | 可单独从 0 开始 | 三节点预检、启动、Ready、CNI 基线。 |
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
| `serverless.md` | 需要 serverless addon | Function/EventTrigger/Workflow、冷启动、scale-to-0、并发扩容 + NATS publish。 |
| `serverless-sam.md` | 需要 serverless addon 和预构建镜像 | SAM CPU 图像分割容器 Function demo。 |
| `serverless-image-workflow.md` | 需要 serverless addon、预构建镜像和本地数据 | 多 Function 图像处理 Workflow demo。 |
| `job-gpu.md` | 需要交我算账号和 submitter 镜像 | Job + Slurm GPU 后端，CUDA vector add、日志和隔离演示。 |
| `testing-agent-prompt.md` | 辅助文档 | 给测试代理的执行、证据和恢复要求。 |

## TODO 验证进度

按推荐执行顺序跟踪人工 testcase 验证状态。只有完成对应文档中的主路径验证、记录证据并
恢复环境后，才把条目标为已完成。

- [x] `startup.md`：`init`、static deps pod、bridge dependency startup。
- [x] `two-node.md`：三节点预检、启动、Ready、CNI 基线。
- [ ] `pod.md`：Pod lifecycle、调度、NodeLost 和删除 Node；新增多容器 localhost 与共享 volume 后需补测。
- [x] `cni.md`：mooring CNI、Pod IP、同节点和跨节点通信。
- [ ] `service.md`：Service endpoints、ClusterIP、NodePort、负载均衡、iptables 清理；新增集群外 NodePort 证据后需补测。
- [ ] `replicaset.md`：ReplicaSet 创建、补齐、缩容和级联删除；新增跨节点分布与 NodeLost 补副本后需补测。
- [x] `logbook.md`：file/etcd Logbook、对象持久化和 bridge 重启恢复；LOGBOOK-06 为可选工程增强项，尚未记录通过。
- [x] `addons.md`：addon manifest、`--addons` readiness 和 doctor 状态。
- [ ] `metrics-server.md`：metrics API 和 `kubectl top`。
- [ ] `hpa.md`：HPA metrics、扩容和缩容。
- [ ] `dns.md`：DNS 对象和 gateway host/path routing。
- [ ] `serverless-nats.md`：Function/EventTrigger/Workflow + NATS publish。
- [ ] `serverless.md`：Function/EventTrigger/Workflow、冷启动、scale-to-0、并发扩容 + NATS publish。
- [ ] `serverless-sam.md`：SAM CPU 图像分割容器 Function demo。
- [ ] `serverless-image-workflow.md`：多 Function 图像处理 Workflow demo。
- [ ] `job-gpu.md`：Job + Slurm GPU 后端、CUDA vector add、日志和隔离演示。

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
./bin/kubectl get nodes; or true
./bin/kubectl get pods; or true
./bin/kubectl get services; or true
./bin/kubectl get rs; or true
./bin/kubectl get hpa; or true
./bin/kubectl get dns; or true
./bin/kubectl get functions; or true
./bin/kubectl get eventtriggers; or true
./bin/kubectl get workflows; or true
./bin/kubectl get jobs; or true
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
  "function slow-echo" \
  "function sam-segment" \
  "eventtrigger echo-events" \
  "workflow echo-chain" \
  "job cuda-add" \
  "job cuda-add-2" \
  "pod nginx-pod" \
  "pod nginx-pod-2" \
  "pod nginx-node-a" \
  "pod nginx-node-b" \
  "pod busybox-node-a" \
  "pod busybox-node-b" \
  "pod busybox-client" \
  "pod volume-resource-pod -n demo"
    ./bin/kubectl delete (string split ' ' -- $item); or true
end

sleep 8
./bin/kubectl get nodes
./bin/kubectl get pods
```

如果要结束本轮测试或重置网络，在 node-a、node-b 和 node-c 先停止对应 `sailer run`，再分别检查
并清理本机网络状态：

```fish
./bin/minik8s doctor network; or true
./bin/minik8s doctor clean; or true
./bin/minik8s doctor network; or true
```

`doctor clean` 是本机操作。三台节点都跑一遍，才能同时清掉 mooring bridge、
VXLAN、iptables 规则、CNI 配置和 IPAM 文件。清理后如果要继续跑默认环境 testcase，
需要重新启动对应 worker，让 `sailer` 重新写入 CNI 配置并注册网络。
