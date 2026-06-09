# TODO：Handout 对齐缺口清单

本文档以 [Handout.md](Handout.md) 为验收基准，记录当前仓库距离课程要求的主要
缺口。状态口径采用“当前事实优先”：已经有代码和测试用例支撑的能力写为已完成或
部分完成；还停留在规划文档中的能力写为未实现。

## P0：验收稳定与真实演示

- [ ] 固化 v0.1.0 演示路径：`make build`、`bridge`、两个 `sailer`、Pod、
  Service、ReplicaSet、Node、Logbook 的完整命令需要在 `docs/testcase/` 中保持
  可复现。
- [ ] 修复 Go 依赖验证风险：干净缓存下 `go test ./...` 会在 Docker runtime
  相关包 setup 阶段失败，错误为 `github.com/docker/docker v27.0.0+incompatible`
  解析到不存在的 `v27.0.0` revision。修复前只能报告部分包测试通过。
- [ ] 明确 root/network 权限要求：CNI、VXLAN、iptables、NodePort 数据面必须在
  Linux 且具备 `ip`、`bridge`、`iptables`、`nsenter` 权限的环境中演示。
- [ ] 给无权限环境保留降级演示：Pod lifecycle 可用 `MINIK8S_CNI_DISABLED=1`，
  Service 对象/endpoints 可用 `sailer --proxy-disabled`。
- [ ] 清理/确认示例 Node IP：`manifest/node/node_a.yaml` 和 `node_b.yaml` 中的
  `InternalIP` 应在演示前改成真实机器地址。

## P1：基础功能补齐

### 启动与控制面依赖

- [x] 增加 `minik8s init` 初始化入口：生成本地状态目录、DNS 配置和
  `.minik8s/manifests/` 下的控制面依赖 Pod manifest。
- [x] 将 `bridge --deps internal` 的私有依赖 Pod 整理成更接近 Kubernetes
  static pod 的机制：`storage-etcd` 是核心依赖，`dns`、`metrics`、`serverless`
  是可选 addon；启用 addon 但 manifest 缺失时提示重新运行 `minik8s init --addons ...`。
- [ ] 后续继续简化启动路径：提供 `minik8s up` 或清晰的一键演示命令，同时用
  `doctor startup` 检查 Docker、CNI、iptables、Harbor、etcd/NATS 等依赖。

### Pod

- [x] YAML 支持 `kind/name/image/imageTag/command/args/volume/port/resources/
  namespace/labels` 主路径。
- [x] Docker sandbox + workload 容器、Pod 状态展示、删除后由 sailer 清理。
- [x] `restartPolicy` 基础重启。
- [ ] liveness/readiness probe 字段执行、日志/exec/cp、完整多容器 readiness。
- [ ] Pod 删除语义仍是“控制面删 desired state，sailer 下一轮清理”，需要在演示
  脚本中保留等待或重试。

### CNI

- [x] 自研 bridge CNI、host-local IPAM、同节点 Pod IP 通信。
- [x] 控制面分配 PodCIDR，sailer 写入 CNI 配置并同步 VXLAN/host-gw route。
- [x] 自研 `mooring` 可通过 `manifest/cni/mooring.yaml` 的
  ConfigMap + DaemonSet 兼容对象激活，`sailer` 仍负责写入节点本地 CNI 配置。
- [ ] 增加外部 CNI 模式：允许 `sailer` 只使用用户指定的 CNI conf/bin 目录调用
  标准 CNI 插件，而不覆盖为自研 `mooring` 配置。
- [ ] 提供常见 CNI 的安装/配置辅助，例如 `minik8s cni install flannel` 或
  `minik8s addon enable flannel`。第一阶段目标是兼容 flannel CNI 配置和二进制；
  完整支持原生 flannel DaemonSet/RBAC/ConfigMap YAML 需等 Minik8s 具备对应对象能力。
- [ ] 跨节点网络仍依赖真实网络、防火墙和 VXLAN 环境；需要继续补 smoke test 和
  自动清理脚本。
- [ ] IPAM 并发、异常恢复、CNI 状态可视化仍较弱。

### Service

- [x] ClusterIP、NodePort YAML/API/CLI。
- [x] selector endpoints、周期同步、删除 Service 清理规则。
- [x] kube-proxy iptables 规则生成和多 endpoint 简单随机负载均衡。
- [ ] NodePort 多节点 SNAT/回包语义不完整，node-b 访问 NodePort 仍应作为观察项。
- [ ] readiness、UDP、session affinity、EndpointSlice、Service CIDR 回收未实现。

### ReplicaSet

- [x] ReplicaSet 类型、YAML loader、file/etcd store、Harbor API、CLI。
- [x] ReplicaSet controller 能补齐副本、删除多余 owned Pod、级联删除。
- [ ] 当前控制器是简化实现：没有 ownerReference、adoption/orphan、revision、
  rollout、资源感知调度。
- [ ] NodeLost 后 ReplicaSet 是否能可靠跨节点补副本需要补真实双机 case。

### HPA / 资源监控

- [x] `HorizontalPodAutoscaler` YAML/API/CLI、file/etcd store 已实现。
- [x] `sailer` 可通过 Docker stats 上报 CPU + Memory metrics，控制面内存保存
  最新样本。
- [x] HPA controller 可按 CPU/Memory utilization 调整 ReplicaSet replicas，并
  支持每轮最多扩缩 1 个副本、缩容冷却。
- [ ] 当前是简化实现：只支持 target `ReplicaSet`、Resource utilization；
  metrics 不持久化，`metrics.k8s.io/v1beta1` 只是复用 sailer 样本的最小 adapter，
  缺少 cAdvisor、custom/external metrics 和 Kubernetes 完整 stabilization policy。
- [ ] 真实压力扩缩容需要补 Linux + Docker 人工验收记录。

### Runtime

- [x] Docker runtime 已接入 `sailer` 主路径，支持 sandbox、业务容器、hostPath
  mount、资源限制、stats 和基于 Docker netns 的 CNI 调用。
- [ ] containerd 目前仍是独立雏形，尚未完整实现 `pkg/runtime.ContainerRuntime`
  接口，也没有 CLI/环境变量切换入口。
- [ ] 增加运行时选择入口，例如 `sailer --runtime docker|containerd` 或
  `MINIK8S_RUNTIME=docker|containerd`，并补齐 containerd 的 sandbox、container、
  image、stats、netns path 和 cleanup 语义。

### DNS 与转发

- [ ] 未实现 DNS 配置对象。
- [ ] 未实现集群内域名解析。
- [ ] 未实现同一 host 下多个 path 转发到不同 Service 的 HTTP gateway。

### 多机部署

- [x] Node 抽象、Node YAML 注册、heartbeat、Ready/Unknown 状态。
- [x] Navigator 对未分配 Pod 做简单 Ready Node 调度。
- [x] PodCIDR 分配和跨节点 VXLAN/host-gw route 同步主路径。
- [ ] 调度器不使用 CPU/memory requests、Node capacity、taints、affinity。
- [ ] 数据面 Node crash 后只标记 Node/Pod 状态，副本重调度和网络清理能力仍有限。

### 容错

- [x] 控制面 crash 不会直接杀死已有 Docker 容器。
- [x] file store 和 etcd-backed Logbook 可恢复 Pod/Service/ReplicaSet/Node 对象。
- [x] heartbeat TTL 可将 Node 标为 Unknown，并从 Service endpoints 中移除失联
  Node 上的 Running Pod。
- [ ] 控制面重启后所有控制循环完全恢复的真实验收脚本仍需持续维护。
- [ ] Node 恢复后的 Pod 状态校正、ReplicaSet 补偿和旧网络状态清理仍需加强。

## P2：自选功能与个人作业

### Serverless 自选功能

- [x] Function 抽象、YAML/API/CLI、file/etcd store 已有最小实现。
- [x] HTTP invoke 已有最小实现：`invoke function <name> --data ...` 调用内联
  Python handler。
- [x] EventTrigger 对象、NATS 订阅触发、publish/doctor 辅助命令已有最小实现；
  NATS 可由 `serverless` addon 启动，也可通过 `MINIK8S_NATS_URL` 指定外部实例。
- [x] Workflow 对象、YAML/API/CLI、file/etcd store 已有最小实现。
- [ ] EventTrigger ack/retry、dead-letter、订阅状态可视化尚未实现。
- [ ] Workflow DAG 自动执行、顺序调用、分支控制尚未实现。
- [ ] 函数 zip/代码文件上传、update 的完整语义尚未实现；当前以 YAML 内联代码为主。
- [ ] scale-to-0、冷启动、并发扩容未实现。
- [ ] 结合模型类 workload 的复杂应用未实现。

### 持久化存储

- [ ] PV/PVC 抽象未实现。
- [ ] 静态/动态 PV provision 未实现。
- [ ] hostPath PV 与多机共享 PV 未实现。
- [ ] Pod 删除后重新绑定持久化数据的验收 case 未实现。

### GPU 应用

- [ ] Slurm/交我算提交任务抽象未实现。
- [ ] GPU job YAML、上传、编译运行、结果回传未实现。
- [ ] CUDA 示例程序和隔离演示未实现。

### Security Context

- [ ] Pod-level `runAsUser`、`runAsGroup`、`fsGroup` 未实现。
- [ ] Container-level security context 覆盖 Pod-level 配置未实现。
- [ ] `supplementalGroupsPolicy` 类似增强未实现。

## P3：工程质量与文档

- [ ] `README.md`、本 TODO、`AGENTS.md` 需要在每次能力变化后同步更新。
- [ ] `docs/status-report.md` 中关于 ReplicaSet 未实现的旧判断已经过时，需要单独
  更新或标注历史日期。
- [ ] `docs/PLAN.md` 仍是目标蓝图，不能作为当前实现状态引用。
- [ ] CI/CD 需要固定依赖缓存和 `go test` 命令，避免本地缓存掩盖依赖版本问题。
- [ ] 文档中的 AI 使用说明需要在最终提交前由小组确认。
