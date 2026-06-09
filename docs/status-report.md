# Minik8s 当前实现状态报告

日期：2026-05-13

## 结论摘要

当前 Minik8s 已经不是纯玩具脚手架，已经形成了一个可测试的最小控制面和节点执行闭环：

- Harbor 提供 HTTP API，支持 Pod、Service、Node 的基本 CRUD/查询。
- Navigator 能把未分配 Pod 调度到 Ready Node，策略是很轻量的轮询。
- Sailer 能按节点轮询 assigned Pods，创建 Docker sandbox/workload 容器，并回写 Pod status。
- Pod 生命周期、hostPort、volume、CPU/memory limit、restartPolicy、CNI bridge、Service endpoint、iptables proxy 都有代码和单元测试覆盖。
- `go test ./...` 当前通过。

但是，与 Kubernetes 相比，当前项目仍然是“单机/小规模多进程 lab 版”。它的稳定边界应该定义为：能演示 Pod 创建/删除/重启、同节点 CNI、Service endpoint 生成、Node 心跳调度、状态持久化；不要承诺完整 Kubernetes 的控制器生态、多机自动网络、ReplicaSet/HPA/DNS/Serverless。

如果目标是尽快达到“有缺陷但稳定版本”，建议冻结范围为 **Pod + Node + Scheduler + CNI 单节点 + Service 状态 + 可选 iptables 数据面 + etcd/file 持久化**。

## 当前模块状态总览

| 模块 | 对标 Kubernetes | 当前状态 | 稳定度 | 说明 |
| --- | --- | --- | --- | --- |
| CLI | `kubectl` 子集 + minik8s 管理入口 | 部分实现 | 中 | `kubectl` 支持 `apply/get/describe/delete/api-resources/version`；`minik8s` 支持 `init/doctor/cni/bridge/sailer` 等运行和诊断命令。 |
| Harbor | kube-apiserver 子集 | 部分实现 | 中 | HTTP API、默认值、状态存储、status 更新可用，但没有 auth、watch、admission、resourceVersion。 |
| Store | Logbook/local store | 部分实现 | 中 | Pod/Service/Node 支持 file store 与 etcd-backed Logbook store；没有 revision/lease 语义。 |
| Node | Node API / kubelet heartbeat | 部分实现 | 中低 | Sailer 通过 `/nodes/{name}/pods` 心跳注册 Ready；没有 capacity、allocatable、conditions、taints。 |
| Navigator | scheduler | 部分实现 | 中低 | 只按 Ready Node 轮询；不使用 requests、NodeSelector、亲和性、资源过滤。 |
| Sailer | kubelet 子集 | 部分实现 | 中 | 能拉 assigned Pod、创建/删除容器、回写 status；没有完整 pod worker、probe、日志、exec、资源上报。 |
| Pod Controller | kubelet pod lifecycle | 部分实现 | 中 | Docker sandbox、workload、volume、resource limit、restartPolicy 可用；probe 字段有类型但未执行。 |
| Docker runtime | CRI runtime 子集 | 部分实现 | 中 | 使用 Docker SDK，不是 CRI；pause 默认用 `alpine:3.20` 模拟。 |
| CNI runner/plugin | CNI + bridge | 部分实现 | 中低 | 单节点 bridge、veth、IPAM、NAT 可用；跨节点可通过 sailer 内置 host-gw 同步或手工 route。 |
| harbor 网络注册表 + sailer 网络同步 | flannel host-gw 类似组件 | 部分实现 | 中低 | Harbor 暴露网络节点注册表，sailer 通过同一控制面端口注册 node 与同步 host-gw route。 |
| Service Controller | endpoint controller | 部分实现 | 中 | 根据 selector + Running PodIP 生成 endpoints；没有独立 EndpointSlice 对象。 |
| kube-proxy | kube-proxy iptables mode | 部分实现 | 中低 | iptables 规则生成有单测；真实运行需要 root/network 权限，主入口注入 proxy 存在风险。 |
| ReplicaSet | replicaset-controller | 未实现 | 无 | 没有类型、API、controller。 |
| HPA | hpa-controller + metrics | 未实现 | 无 | 没有 metrics pipeline、ReplicaSet 对接。 |
| DNS/Ingress | CoreDNS/Ingress | 未实现 | 无 | 没有 DNS 对象、DNS server、HTTP gateway。 |
| Serverless | Knative/OpenFaaS 子集 | 未实现 | 无 | 没有 Function、Activator、Workflow、scale-to-0。 |
| Security Context/GPU | kubelet/security/resource 扩展 | 未实现 | 无 | Pod spec 中没有 securityContext/GPU 语义。 |
| TUI | dashboard 类工具 | 部分实现 | 低 | 有 `internal/tui/podtui`，但不是主流程核心能力。 |

## 已部分实现的模块

### 1. Harbor API

当前支持：

- discovery：`/version`、`/api`、`/api/v1`
- Pods：create/list/get/update/delete/status
- Services：create/list/get/update/delete
- Nodes：list/get，以及 `/api/v1/nodes/{name}/pods` 作为 worker 心跳和 assigned Pod 拉取接口

与 Kubernetes 的差距：

- 没有 API group/version 多版本机制。
- 没有 watch/list-watch，控制器依赖轮询。
- 没有 resourceVersion、generation、ownerReferences、finalizers。
- 没有认证鉴权和 admission。

稳定版建议：

- 保持当前 API 范围，不扩新资源。
- 明确文档：这是 Harbor API，不兼容 Kubernetes API。
- 给所有 CLI demo 固定 `MINIK8S_HARBOR=http://127.0.0.1:18080`。

### 2. Pod 生命周期

当前支持：

- YAML 解析、默认 namespace/restartPolicy。
- Docker sandbox + workload 容器。
- command/args/env/ports/hostPort。
- hostPath/emptyDir 映射。
- CPU/memory limit 映射到 Docker HostConfig。
- `restartPolicy: Always/OnFailure/Never` 的基础重启逻辑。
- Pod status 回写，包括 phase、PodIP、StartTime、ContainerStatus。

主要缺口：

- readiness/liveness probe 字段存在，但没有执行。
- Pod 删除是“删除 desired state 后由 sailer 下一轮清理”，不是同步删除。
- terminal Pod 清理逻辑比较粗糙，`Succeeded/Failed` 后直接清理 runtime，但 status 与对象保留语义不完全像 Kubernetes。
- 没有 init containers、multi-container readiness 聚合、日志/exec/cp。

稳定版建议：

- 把 probe 标为未实现。
- demo 只使用长期运行容器、hostPath/emptyDir、restartPolicy。
- 增加一个“删除后等待 sailer 同步”的说明，避免看起来像 delete 不生效。

### 3. Node 与调度

当前支持：

- Sailer 请求 `/nodes/{node}/pods` 时自动 heartbeat。
- NodeStore 保存 Ready Node。
- Harbor 会在 Pod 创建或心跳时尝试调度未分配 Pod。
- Scheduler 按 Ready Node 轮询。

主要缺口：

- Node 没有 IP、PodCIDR、capacity、allocatable。
- Pod 的 `nodeSelector` 字段存在，但调度器不使用。
- 没有资源请求过滤，CPU/memory request 只在 Pod spec 中存在。
- Node TTL 只是过滤 Ready Node，不会迁移已分配 Pod。

稳定版建议：

- 稳定版只承诺“多 worker 轮询分配”，不承诺资源感知调度。
- 如果要演示多节点，手动启动多个 sailer，并手工配置每个节点 CNI/route。
- 最好补一个很小的 bugfix：调度时尊重 `nodeSelector` 或在文档中明确不支持。

### 4. CNI 与网络

当前支持：

- `minik8s cni init` 生成 CNI 配置。
- CNI runner 查找配置并调用插件。
- `mooring` 插件创建 bridge、veth、Pod IP、默认路由、NAT。
- IPAM 用 JSON 文件持久化 allocation。
- 支持静态 host-gw routes。
- Harbor 内置网络注册表，`sailer` 使用 Node YAML 注册节点，控制面分配 PodCIDR 后能动态注册并同步 VXLAN route。

主要缺口：

- CNI 配置路径是项目内 `.minik8s/cni/...` 风格，不是系统级 `/etc/cni/net.d`。
- 跨节点网络和 Harbor Node API 未打通：Node 没有 NodeIP/PodCIDR 字段。
- PodCIDR 分配、route 下发、CNI 配置生成需要人工维护。
- 真实 CNI 依赖 root、`ip`、`iptables`、`nsenter`，环境敏感。

稳定版建议：

- 默认演示单节点 CNI。
- 跨节点只作为“可手工配置的增强能力”展示。
- 保留 `MINIK8S_CNI_DISABLED=1` 作为 Pod 基础功能演示兜底。

### 5. Service 与 kube-proxy

当前支持：

- Service 类型：ClusterIP、NodePort。
- ClusterIP 分配。
- 根据 selector 选择 Running 且有 PodIP 的 Pod，生成 endpoints。
- sailer 内置 node-local kubeproxy，为 ClusterIP/NodePort 创建 NAT chain 和 DNAT 规则。
- 多 endpoint 使用 statistic random 规则做简单负载均衡。

主要缺口：

- kubeproxy 不作为独立 daemon；它与 sailer 共生命周期，在每个 Worker Node 上同步 Service 数据面规则。
- bridge/captain 只更新 Service endpoints，不再直接操作本机 iptables。
- ClusterIP 默认/分配逻辑很简化，未完整处理冲突、回收、Service CIDR。
- 没有 sessionAffinity、externalTrafficPolicy、EndpointSlice。

稳定版建议：

- 在无 root/iptables 环境中运行 sailer 时使用 `--proxy-disabled`，此时仅验证 Service object + endpoints。
- 将稳定版 Service 目标分成两层：
  - 必须稳定：Service object + selector + endpoints 展示。
  - 可选稳定：iptables 数据面访问 ClusterIP/NodePort。

### 6. 持久化

当前支持：

- 默认本地 JSON：Pods、Services、Nodes。
- `MINIK8S_LOGBOOK_ENDPOINTS` 存在时 Pod/Service/Node 使用 etcd-backed Logbook。
- `doctor logbook` 能探测 etcd 连接。
- store 有单元测试。

主要缺口：

- 没有 watch、lease、revision，因此控制器不能像 Kubernetes 那样基于 etcd revision 工作。
- file store 靠每次 reload/save，适合 demo，不适合高并发。

稳定版建议：

- 稳定版默认使用 file store，etcd 作为“可选持久化后端”。
- 如果答辩强调 etcd，需要补齐 NodeStore etcd 或在报告里只承诺 Pod/Service etcd。

## 当前不工作的模块

这些模块在 `docs/PLAN.md` 或开题报告中出现，但当前仓库没有可运行实现：

1. ReplicaSet
   - 没有 API 类型、YAML parser、store、controller、CLI。
   - 因此 HPA/Serverless 也没有可依赖的副本控制基础。

2. HPA
   - 没有 metrics source、metrics API、HPA 对象、控制循环。
   - Docker stats/cAdvisor 没接入。

3. DNS / Ingress / Path 转发
   - 没有 DNS 对象、CoreDNS/dnsmasq 集成或自研 DNS proxy。
   - 没有 HTTP ingress gateway。

4. Serverless
   - 没有 Function、Activator、Gateway、Workflow、Event Trigger、scale-to-0。
   - RabbitMQ 没有集成。

5. Security Context / GPU
   - Pod 类型中没有 securityContext、runAsUser、fsGroup、capabilities。
   - Docker runtime 没有映射 security options。
   - GPU 没有 device/plugin/runtime 参数设计。

6. Kubernetes 兼容性
   - 只能用本仓库构建的 `kubectl` 子集操作，不能直接使用上游 Kubernetes
     `kubectl` 对接完整 Kubernetes API。
   - YAML 只是 Kubernetes-like，不是完整 Kubernetes schema。
   - 没有 kube-apiserver 的对象语义。

## 关键风险与建议优先级

### P0：真实演示必须先稳定

1. 固定启动流程
   - `make build`
   - `./minik8s bridge --listen :18080`
   - `./minik8s sailer manifest/node/node_a.yaml --harbor http://127.0.0.1:18080`
   - `./kubectl apply/get/delete`

2. Service proxy 节点权限风险
   - kubeproxy 随 sailer 在 Worker Node 上同步 iptables，bridge 不再操作数据面规则。
   - 演示 Service 数据面时，运行 sailer 的节点需要 root/iptables；无权限时使用 `--proxy-disabled` 只验证 endpoints。

3. 删除语义风险
   - `delete pod` 只删除控制面期望状态，runtime 清理由 sailer 下一轮执行。
   - 文档和 demo 脚本要加等待/重试。

4. 环境风险
   - CNI/kube-proxy 需要 root 和 Linux 网络工具。
   - 保留 `MINIK8S_CNI_DISABLED=1` 的 Pod 基础演示作为 fallback。

### P1：把“部分实现”补到可信

1. Scheduler 尊重 `nodeSelector`，或者移除/标注该字段。
2. 给 Service 数据面加一个真实 smoke test 脚本，失败时能自动清理 iptables chain。

### P2：不要现在碰的大坑

1. ReplicaSet/HPA：除非必须，不要在稳定版前新增。
2. Serverless：没有 ReplicaSet/Service/DNS 稳定前不要做。
3. DNS/Ingress：会引入额外 daemon 和端口调试，收益不如稳定 Pod/Service。
4. 完整 Kubernetes API 兼容：超出当前架构范围。

## 如何到达一个“有缺陷但稳定”的版本

### 稳定版定义

建议版本名：`v0.1-defective-stable`

必须承诺：

- Pod YAML apply 后进入 Pending。
- Sailer 同步后创建 Docker sandbox/workload，并回写 Running/Failed。
- `get/describe/delete pod` 可用。
- 容器崩溃后按 restartPolicy 重启。
- CNI 单节点 Pod IP 分配可用。
- Service 能根据 selector 生成 endpoints。
- Node heartbeat 和简单轮询调度可用。
- file store 持久化可用；Pod/Service etcd 可选。
- `go test ./...` 通过。

明确不承诺：

- Kubernetes API 兼容。
- ReplicaSet/HPA/DNS/Serverless。
- 自动跨节点 PodCIDR 管理。
- 高并发、强一致、多租户安全。
- 生产级 kube-proxy。

### 推荐收敛路线

第 1 步：冻结范围。

- README 和 `docs/PLAN.md` 中把当前版本目标改成 Pod/Service/Node/CNI/etcd-lite。
- 把 ReplicaSet/HPA/DNS/Serverless 移到 “Future Work”。

第 2 步：修最影响演示的缺口。

- 明确 stable 的 Service 数据面由 sailer 内置 kubeproxy 提供，bridge 只保证 endpoints。
- 给 delete demo 加 `wait until docker ps no residue` 的脚本。

第 3 步：补最小一致性。

- Scheduler 对 `nodeSelector` 做最小匹配。

第 4 步：建立稳定验收脚本。

建议保留 4 组验收：

- `go test ./...`
- Pod 无 CNI smoke：创建 nginx、curl hostPort、删除无残留。
- Pod CNI smoke：创建 nginx + busybox，busybox 访问 nginx PodIP。
- Service endpoint smoke：nginx + Service，`get services` 看到 endpoint。

iptables ClusterIP/NodePort smoke 建议作为增强验收，不作为阻塞项，除非你确认答辩机器具备 root/iptables 环境。

第 5 步：文档对齐实际能力。

- `docs/harbor-api.md` 已经比较准确，可保留。
- `docs/testcase/*.md` 可以作为测试说明，但要标出哪些需要 root、哪些只是设计映射。
- `docs/PLAN.md` 当前像最终愿景，不像当前状态；建议追加一节 “Current Stable Scope”。

## 与 Kubernetes 的核心差距

Kubernetes 的强大来自三件事：声明式 API、watch 驱动控制器、节点侧 kubelet/CRI/CNI/CSI 的长期 reconcile。当前 Minik8s 已经有声明式 API 和节点侧轮询 reconcile 的雏形，但控制器生态和对象模型还很薄。

最重要的差距不是“功能少”，而是这些系统性质还没有：

- 没有 watch，因此状态变化不是实时事件驱动。
- 没有 ownerReferences/finalizers，因此资源级联和清理语义弱。
- 没有 resourceVersion/generation，因此并发更新和控制器幂等能力弱。
- 没有统一 Node 网络模型，因此多机网络需要人工拼接。
- 没有 ReplicaSet，因此无法稳定表达“我要 N 个副本”。

因此当前最合理的定位是：**教学型 Kubernetes-like control plane，展示 Pod 生命周期、调度、网络和 Service 的最小闭环**。

## 最终建议

不要现在追求“功能完整”，应该追求“边界诚实”。把稳定版本做成：

- 少承诺；
- demo 可重复；
- 失败有 fallback；
- 文档和代码一致；
- 测试全绿；
- 关键流程可以从零跑通。

这个版本即使缺 ReplicaSet/HPA/DNS/Serverless，也会比一个功能清单很大但现场不稳定的版本更有说服力。
