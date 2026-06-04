# Minik8s

Minik8s 是一个面向云操作系统课程 Lab 的轻量容器编排系统。项目目标参考
Kubernetes，但当前实现更适合描述为“教学版 Kubernetes 核心闭环”：一个
`bridge` 控制面加多个节点本地 `sailer` agent，支持通过 YAML 管理 Pod、
Service、ReplicaSet、HorizontalPodAutoscaler 和 Node，并在 Linux + Docker 环境中演示 Pod 网络、
Service 转发和控制面状态恢复。

课程规格以 [docs/Handout.md](docs/Handout.md) 为准。本 README 只描述仓库
当前已经实现或可以演示的能力，不把开题蓝图写成已完成能力。

## 当前能力

已实现或主路径可演示：

- Pod YAML 解析、默认值填充、`apply/get/describe/delete`。
- Docker sandbox + workload 容器，支持 command、args、ports、hostPort、
  hostPath/emptyDir volume、CPU/memory limit。
- Pod 状态回写，展示 name、phase、Pod IP、uptime、namespace、labels。
- `restartPolicy: Always/OnFailure/Never` 的基础重启逻辑。
- CNI bridge 插件、host-local IPAM、Pod IP 分配、同节点通信。
- 基于 Node YAML 的节点心跳、Ready/Unknown 状态、控制面 PodCIDR 分配。
- `sailer` 自动写入本机 CNI 配置，并同步 VXLAN/host-gw 风格的跨节点路由。
- Service `ClusterIP` 和 `NodePort` 对象、selector endpoints、简单负载均衡。
- 节点本地 kube-proxy 逻辑随 `sailer` 同步 iptables 规则，可用
  `--proxy-disabled` 在无 root/iptables 环境下关闭数据面规则。
- ReplicaSet YAML、API、CLI、file/etcd store、controller，同步 desired/current
  副本，并在删除 ReplicaSet 时级联删除 owned Pods。
- HPA YAML、API、CLI、file/etcd store、控制器和 `sailer` Docker metrics 上报；
  当前只支持基于 ReplicaSet 的 CPU/Memory utilization 扩缩容。
- Logbook 状态存储：默认本地 JSON；设置 `MINIK8S_LOGBOOK_ENDPOINTS` 后，
  Pod、Service、ReplicaSet、HPA、Node 使用 etcd-backed store。
- 控制面重启后可从 file/etcd 恢复声明对象；worker 继续心跳后状态重新收敛。

尚未实现或不应作为当前版本承诺：

- DNS 对象、域名解析和同 host 多 path 转发。
- Serverless Function、Event Trigger、Workflow、scale-to-0。
- PV/PVC 持久化卷、GPU 应用、Security Context。
- 完整 Kubernetes API machinery，例如 watch、resourceVersion、admission、
  RBAC、EndpointSlice、probe 执行和资源感知调度。

## 架构

当前组件边界如下：

- `cmd/minik8s/`：主 CLI 和控制面/节点进程入口。
- `cmd/minik8s-bridge/`：CNI bridge 插件入口。
- `internal/bridge/`：控制面边界，组合 Harbor API、Logbook store、Navigator
  scheduler 和 Captain controllers。
- `internal/bridge/harbor/`：Kubernetes-like HTTP API，服务 Pod、Service、
  ReplicaSet、HPA、Node、节点心跳和节点 metrics 接口。
- `internal/bridge/logbook/`：控制面状态存储，提供 in-memory、file 和 etcd
  后端。
- `internal/bridge/navigator/`：轻量调度器，目前按 Ready Node 做简单分配。
- `internal/bridge/captain/`：控制器集合，当前包括 Service endpoint controller、
  ReplicaSet controller 和 HPA controller。
- `internal/sailer/`：节点本地 agent，轮询 assigned Pods，管理 Docker 容器、
  CNI、Pod status 和 kube-proxy 规则。
- `internal/cniplugin/`、`internal/cni/`、`internal/netagent/`：CNI 插件、CNI
  runner 和跨节点网络同步。
- `internal/kubeproxy/`：iptables Service 数据面规则生成。
- `pkg/yaml/`：Pod、Service、ReplicaSet、HPA、Node YAML loader 与校验。
- `manifest/`：可直接用于演示的示例 YAML。
- `docs/testcase/`：按功能拆分的人工验收脚本和排查说明。

一句话拓扑：

```text
bridge = apiserver 子集 + controller-manager 子集 + scheduler 子集
sailer = kubelet 子集 + node network agent + kube-proxy
```

## 快速启动

构建二进制：

```bash
make build
```

启动控制面：

```bash
export MINIK8S_HARBOR=http://127.0.0.1:18080
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr 10.244.0.0/16 \
  --node-cidr-mask-size 24
```

在另一个终端启动本机 worker：

```bash
./minik8s sailer manifest/node/node_a.yaml --harbor http://127.0.0.1:18080
```

常用 CLI：

```bash
./minik8s version
./minik8s api-resources
./minik8s get nodes

./minik8s apply -f manifest/pod/pod_nginx.yaml
./minik8s get pods
./minik8s describe pod nginx-pod
./minik8s delete pod nginx-pod

./minik8s apply -f manifest/service/service_clusterip_nginx.yaml
./minik8s get services
./minik8s describe service nginx-service
./minik8s delete service nginx-service

./minik8s apply -f manifest/replicaset/replicaset_nginx.yaml
./minik8s get rs
./minik8s describe rs nginx-rs
./minik8s delete rs nginx-rs
```

CNI 和 kube-proxy 需要 Linux network namespace、`ip`、`bridge`、`iptables`、
`nsenter` 和通常的 root 权限。只演示控制面对象和 Pod lifecycle 时，可以使用
`MINIK8S_CNI_DISABLED=1` 或 `sailer --proxy-disabled` 降低环境要求。

## 双机与 etcd 演示

双机公共流程见 [docs/testcase/two-node.md](docs/testcase/two-node.md)。主路径是：

1. 在 node-a 启动 `bridge --listen :18080`。
2. 在 node-a/node-b 分别用对应 `manifest/node/*.yaml` 启动 `sailer`。
3. 控制面自动分配不同 PodCIDR，例如 `10.244.0.0/24` 和 `10.244.1.0/24`。
4. `sailer` 写入本机 CNI 配置并同步 VXLAN overlay。
5. 通过 `get nodes`、PodIP 互通、Service endpoints 和 iptables 规则验证结果。

etcd/Logbook 流程见 [docs/testcase/logbook.md](docs/testcase/logbook.md)。设置：

```bash
export MINIK8S_LOGBOOK_ENDPOINTS=http://127.0.0.1:2379
```

之后启动 `bridge`，Pod、Service、ReplicaSet、Node 会写入 `/registry/...` 前缀。

## Handout 覆盖状态

| Handout 项 | 当前状态 | 说明 |
| --- | --- | --- |
| Pod 抽象与生命周期 | 部分完成 | Pod YAML、Docker 运行、状态展示、删除、基础 restartPolicy 已有；probe、exec/logs、完整多容器语义未完成。 |
| CNI Pod 间通信 | 部分完成 | 单节点 bridge/IPAM 可演示；跨节点 VXLAN/host-gw 可演示但依赖环境和节点配置。 |
| Service ClusterIP/NodePort | 部分完成 | Service 对象、endpoints、iptables proxy 和简单负载均衡已有；复杂 SNAT、readiness、EndpointSlice 未完成。 |
| ReplicaSet | 部分完成 | YAML/API/CLI/controller/store 已有；当前是简化控制器，没有 ownerReference、adoption/orphan 等完整 K8s 语义。 |
| 资源监控与 HPA | 部分完成 | HPA 对象/API/CLI/store、Docker CPU/Memory metrics 上报和 ReplicaSet 扩缩容已有；只支持 Resource utilization，metrics 不持久化。 |
| DNS 与转发 | 未实现 | 没有 DNS 对象、DNS server 或 HTTP path gateway。 |
| 多机部署 | 部分完成 | Node、heartbeat、PodCIDR、简单调度、跨节点网络同步已有；调度不做资源过滤，故障迁移能力有限。 |
| 容错 | 部分完成 | 控制面状态可持久化，重启后可恢复对象；Node heartbeat 可标记 Unknown；没有完整故障自愈和副本重调度。 |
| 自选 Serverless | 未实现 | Function、Workflow、Event Trigger、scale-to-0 均未实现。 |
| 个人 PV/PVC | 未实现 | 尚无 PV/PVC 抽象和多机存储实现。 |
| 个人 GPU | 未实现 | 尚无 Slurm/GPU job 抽象。 |
| 个人 Security Context | 未实现 | 尚无 runAsUser、runAsGroup、fsGroup 映射。 |

## 测试与当前验证基线

推荐先跑包级测试，再跑人工 testcase：

```bash
go test ./pkg/yaml ./internal/bridge/logbook ./internal/bridge/captain ./internal/bridge/harbor ./internal/sailer ./internal/kubeproxy ./test/integration -count=1
```

全量测试目标命令是：

```bash
go test ./...
```

当前在干净模块缓存下，全量测试会因为 `go.mod` 中
`github.com/docker/docker v27.0.0+incompatible` 被 Go 解析为不存在的
`v27.0.0` revision 而导致 Docker runtime 相关包 setup failed；这不是某个
业务单测断言失败。修复依赖版本前，不要在文档或答辩中宣称 `go test ./...`
全量通过。

## AI 使用说明

本课程 Handout 要求在 README 或附录中标注 AI 辅助使用。本仓库开发过程中使用
AI 工具辅助梳理架构、生成/修改部分文档、分析测试输出和定位实现缺口。所有最终
提交内容仍需由小组成员审阅、运行和解释；答辩时应以代码与测试结果为准。
