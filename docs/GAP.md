# GAP: Kubernetes 语义对照缺口

本文按 Kubernetes 官方语义对照当前 Minik8s 实现，记录“容易被误认为已经完整实现”的
语义差距。课程验收规格仍以 [Handout.md](Handout.md) 为准；本文用于答辩表述、后续
补强和风险排查。

## 结论

Minik8s 当前最接近 Kubernetes 的部分是控制循环形状：

```text
API object -> controller -> node agent -> runtime/network -> status update
```

主要差距集中在 Kubernetes 的中间语义层：UID / ownerReference、conditions / events、
readiness、EndpointSlice、Metrics API aggregation、HPA 保守算法、scheduler framework、
GC / finalizer。

尤其需要谨慎表述 metrics / HPA：当前是 **Docker stats 驱动的教学版 Resource HPA，
带最小 metrics.k8s.io adapter**，不是完整 Kubernetes metrics-server / HPA 实现。

## Metrics / HPA

| 维度 | Kubernetes | Minik8s 当前 | 差距/空缺 |
| --- | --- | --- | --- |
| 指标链路 | container runtime / cAdvisor -> kubelet `/metrics/resource` 或 `/stats` -> metrics-server -> API aggregation -> `metrics.k8s.io` -> HPA / `kubectl top` | `sailer` 直接读 Docker stats，并 PUT 到 Harbor；Harbor 直接提供 `metrics.k8s.io` | 没有 kubelet metrics endpoint、cAdvisor、真实 metrics-server、API aggregation、TLS / RBAC / APIService |
| metrics-server | 独立 addon，拉取每个 kubelet 指标并聚合 | `metrics-server` Pod 是 busybox 占位；真实 API 在 bridge 里 | 不能说实现了真实 metrics-server，只能说 minimal metrics adapter |
| CPU 指标 | 对 cumulative CPU counter 做 rate，Metrics API 有实际采样 window | 也用相邻两轮 Docker stats delta 算 CPU | 首轮 CPU 不可用；`window` 展示与实际采样间隔不强绑定 |
| Memory 指标 | Kubernetes resource metrics 通常报 working set | 当前使用 Docker `MemoryStats.Usage` | 语义不等价，可能包含 cache，和 working set 口径不同 |
| Metrics 存储 | metrics-server 有缓存，但不是业务状态持久化；HPA按新鲜样本决策 | bridge 内存 map，只保存最新样本 | bridge 重启丢 metrics；旧 Pod 样本可能残留；metrics API 可能返回 stale 样本 |
| NodeMetrics | kubelet / metrics-server 提供节点级资源指标 | 由 PodMetrics 按 node 汇总 | 不含宿主机系统进程、daemon、未托管容器、节点真实 working set |
| HPA target | 支持 scalable target，常见 Deployment / ReplicaSet / StatefulSet 等 scale subresource | 只允许 `ReplicaSet` | 没有 scale subresource 抽象，也不支持 Deployment / Pod target |
| HPA metric 类型 | Resource、ContainerResource、Pods、Object、External 等 | 只支持 Resource CPU / Memory utilization | 没有 custom / external metrics，也没有 per-container resource metric |
| HPA 算法 | 基础公式相同；还处理 missing metrics、not-yet-ready pods、tolerance、downscale stabilization window | 使用基础公式，多指标取最大，每轮最多 +/-1，缩容 30s cooldown | 缺 Kubernetes 的保守重算、Ready 延迟、默认 5min 缩容稳定窗口、tolerance、behavior policies |
| readiness 影响 | HPA 会考虑 Pod Ready 状态和 CPU 初始化窗口 | 只看 Pod phase Running 和 metrics TTL | readiness 未实现，所以启动期 CPU、未就绪 Pod 对 HPA 都偏简化 |
| scale-to-zero | 普通 HPA 基于资源指标不能无样本从 0 自动拉起 | `minReplicas` 可为 0，但无 Running Pod 时只报 `NoRunningPods` | 不应宣称资源 HPA 支持 0 -> N 冷启动 |

优先补强建议：

- P0：某 node 新一轮上报 metrics 时，清理这个 node 上本轮未再上报的旧 Pod metrics。
- P0：`PodUtilization` 遇到任一目标容器缺 metrics / request 时，把整个 Pod 标为缺失，
  避免 partial container metrics 误算。
- P1：metrics API 对外展示增加 freshness 过滤或标识，避免 `kubectl top` 和 HPA 决策
  看到的样本新鲜度不一致。
- P1：文档和答辩统一称为 minimal metrics adapter，不称真实 metrics-server。
- P2：再考虑真实 scraper / cAdvisor / kubelet metrics endpoint 或历史窗口。

## Pod / Kubelet 语义

| 维度 | Kubernetes | Minik8s 当前 | 差距/空缺 |
| --- | --- | --- | --- |
| Pod UID 生命周期 | Pod 一旦绑定 node，不会原地迁移；替代 Pod 有新 UID | 基本按 name / namespace 管理，没有 UID 语义 | 没有 UID、resourceVersion、deletionTimestamp / finalizer 语义 |
| Restart backoff | 容器反复崩溃进入 CrashLoopBackOff，指数退避 | 容器退出后按 restartPolicy 直接重启 | 没有 backoff、CrashLoopBackOff 状态、事件记录 |
| Probe | kubelet 执行 startup / liveness / readiness；liveness 可杀容器，readiness 控制 Service 流量 | 已支持最小 exec / HTTP / TCP liveness 和 readiness，readiness 可影响 Service endpoints | 未实现 startupProbe、successThreshold、named port、gRPC probe、完整 probe 周期和事件语义 |
| Pod phase / status | phase 只是粗粒度；还有 conditions、container waiting / running / terminated reasons | phase + 简化 container status | conditions、reason、events、waiting 状态不完整 |
| Graceful termination | SIGTERM、preStop、terminationGracePeriodSeconds、强删例外 | 删除后由 sailer reconcile 清理 | 没有完整优雅终止、preStop、Terminating 状态 |

## Service / 网络

| 维度 | Kubernetes | Minik8s 当前 | 差距/空缺 |
| --- | --- | --- | --- |
| EndpointSlice | Service 后端由 EndpointSlice 表达，支持 scale、conditions、dual-stack、hints | Service status 内嵌 endpoints | 没有 EndpointSlice 对象、ready / serving / terminating condition |
| readiness endpoint | 未 Ready Pod 不接 Service 流量 | 只过滤 Running + PodIP | readiness 未接入，可能把未就绪 Pod 加入 endpoints |
| kube-proxy | iptables / ipvs / nftables 等，处理 ClusterIP / NodePort / sessionAffinity / traffic policy 等 | 简化 iptables DNAT + random | NodePort SNAT / 回包、多协议、session affinity、traffic policy 不完整 |
| DNS | CoreDNS + Service / Pod DNS 规范 | hosts + HTTP host/path gateway | 可演示，但非完整 Kubernetes DNS / Ingress / Gateway 语义 |

## ReplicaSet / 控制器

| 维度 | Kubernetes | Minik8s 当前 | 差距/空缺 |
| --- | --- | --- | --- |
| ownerReferences | ReplicaSet 通过 ownerReferences 认领 / 管理 Pod | 用 label `minik8s.io/replicaset` | 没有 ownerReference、UID、blockOwnerDeletion、GC |
| adoption / orphan | 可收养 selector 匹配且无 controller owner 的 Pod | 主要管理自己 label 的 Pod | adoption / orphan 语义不完整 |
| selector / template | RS selector 和 template 有 Kubernetes API 校验约束 | 简化校验 | API 校验语义不完整 |
| Deployment rollout | 通常用户通过 Deployment 管 RS 版本滚动 | 没有 Deployment | 无 rollout / history / rollback / revision |

## Scheduler / Node

| 维度 | Kubernetes | Minik8s 当前 | 差距/空缺 |
| --- | --- | --- | --- |
| 调度框架 | Filter + Score + Bind，插件化，考虑资源、affinity、taints、topology、volume 等 | Ready node + nodeSelector + requests / capacity + round-robin | 没有 scoring 插件、taints / tolerations、affinity、topology spread、volume binding |
| Node 状态 | kubelet heartbeat / Lease，Node conditions，taints 驱逐 | heartbeat TTL 标 Unknown | 没有 Lease、Node pressure、taint-based eviction |
| 失联 Pod | Node lost 后 Pod 最终 Failed / 删除，由控制器创建替代 Pod | RS Pod 删除触发补副本，普通 Pod Unknown | 普通 Pod 清理 / 失败语义不完全；旧 runtime / network 残留校正有限 |

## API Machinery / 通用语义

| 维度 | Kubernetes | Minik8s 当前 | 差距/空缺 |
| --- | --- | --- | --- |
| 对象元数据 | UID、resourceVersion、generation、managedFields、ownerReferences、finalizers | name / namespace / labels / annotations 为主 | 缺少并发控制、GC、server-side apply 等基础元语义 |
| Watch | watch stream 驱动 controller 和客户端 | 多数为周期 list / sync | 没有 watch、bookmark、resourceVersion 增量语义 |
| Admission / authz | admission chain、RBAC、ServiceAccount、准入校验 | node token + 简化 API | 缺少 RBAC、admission webhook、准入默认化体系 |
| Events | kubelet / controller 记录事件，供 describe 排障 | 少量日志和状态 reason | 缺少 Kubernetes Events 对象 |

## 答辩表述建议

- 可以说：实现了 Kubernetes-like 的 API、controller、scheduler、kubelet/kube-proxy 子集。
- 可以说：HPA 使用 sailer 上报的 Docker CPU / Memory metrics 调整 ReplicaSet 副本数。
- 应避免说：实现了完整 metrics-server、完整 Kubernetes HPA、完整 kube-proxy、完整 Pod lifecycle。
- 对 metrics/HPA 推荐表述：
  “当前实现了教学版资源指标链路：sailer 采集 Docker stats，bridge 暴露最小
  `metrics.k8s.io` adapter，HPA 控制器读取 CPU / Memory utilization 并按简化策略调整
  ReplicaSet。它覆盖课程要求的 CPU + Memory 动态伸缩主路径，但不等同于 Kubernetes
  metrics-server / cAdvisor / API aggregation / HPA 完整语义。”
