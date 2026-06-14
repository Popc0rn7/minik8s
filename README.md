# Minik8s

Minik8s 是一个面向云操作系统课程 Lab 的轻量容器编排系统。项目目标参考
Kubernetes，但当前实现更适合描述为“教学版 Kubernetes 核心闭环”：一个
`bridge` 控制面加多个节点本地 `sailer` agent，用户通过单独的 `kubectl`
二进制用 Kubernetes 风格命令管理 Pod、
Service、ReplicaSet、HorizontalPodAutoscaler 和 Node，并在 Linux + Docker 环境中演示 Pod 网络、
Service 转发和控制面状态恢复。

课程规格以 [docs/Handout.md](docs/Handout.md) 为准。本 README 只描述仓库
当前已经实现或可以演示的能力，不把开题蓝图写成已完成能力。

## 当前能力

已实现或主路径可演示：

- `kubectl apply/get/describe/delete/api-resources/version` 用户资源操作。
- Docker sandbox + workload 容器，支持 command、args、ports、hostPort、
  hostPath/emptyDir volume、CPU/memory limit。
- Pod 状态回写，展示 name、phase、Pod IP、uptime、namespace、labels。
- `restartPolicy: Always/OnFailure/Never` 的基础重启逻辑。
- CNI bridge 插件、host-local IPAM、Pod IP 分配、同节点通信。
- 基于 Node YAML 的节点心跳、Ready/Unknown 状态、控制面 PodCIDR 分配。
- `sailer` 自动写入本机 CNI 配置，并同步 VXLAN/host-gw 风格的跨节点路由。
- `manifest/cni/mooring.yaml` 可用 ConfigMap + DaemonSet 兼容对象声明自研 CNI，
  `sailer` 会通过 `mooring-cni` 安装镜像落地 `/opt/cni/bin/mooring`。
- `minik8s doctor network` 可检查本机 CNI 状态；`minik8s doctor clean` 可清理
  mooring bridge、VXLAN、iptables 规则和本地 IPAM 文件。
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
- Serverless 的事件 ack/retry、Workflow 自动执行、scale-to-0。
- PV/PVC 持久化卷、GPU 应用、Security Context。
- 完整 Kubernetes API machinery，例如 watch、resourceVersion、admission、
  RBAC、EndpointSlice、probe 执行和资源感知调度。

## 架构

当前组件边界如下：

- `cmd/kubectl/`：用户侧 Kubernetes 风格资源操作入口。
- `cmd/minik8s/`：控制面、节点进程、诊断和本地初始化入口。
- `cmd/mooring/`：CNI bridge 插件入口。
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

构建后会得到两个主要二进制：

- `./kubectl`：用户侧资源操作，命令形态对齐 Kubernetes 的 `kubectl` 子集。
- `./minik8s`：运行和诊断 Minik8s 组件，例如 `init`、`bridge`、`sailer`、`doctor`。

可选：复制 `.env.example` 为 `.env`，把常用运行配置放在文件中。`kubectl` 和
`minik8s` 启动时都会读取当前目录 `.env`，但 shell 中已设置的环境变量优先。

```bash
cp .env.example .env
```

初始化本地启动文件。该命令只写入 `.minik8s/` 下的状态目录、DNS 配置和
static pod manifests，不启动进程。它会生成核心 `storage-etcd` 以及 `dns`、
`metrics`、`serverless` addon manifests；实际启动哪些 addon 由后续
`bridge --addons` 决定：

```bash
./minik8s init
```

启动控制面。默认 `bridge` 会先读取 `.minik8s/manifests/` 下的 `storage-etcd.yaml`
并通过私有本地 `sailer` 启动核心依赖 Pod，然后再启动 Harbor API。默认不启用
addon；启动后会把 Harbor 地址写入 `.minik8s/config.json`，后续 `./kubectl`
默认读取该配置。需要 DNS、metrics 或 serverless 时显式传 `--addons`：

```bash
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr 10.244.0.0/16 \
  --node-cidr-mask-size 24
```

在另一个终端启动本机 worker：

```bash
./minik8s bridge token set minik8s --ttl 24h
./minik8s sailer join \
  --apiserver http://127.0.0.1:18080 \
  --token minik8s \
  -f manifest/node/node_a.yaml
./minik8s sailer run
```

常用 CLI：

```bash
./kubectl version
./kubectl api-resources
./kubectl get nodes

./kubectl apply -f manifest/pod/pod_nginx.yaml
./kubectl get pods
./kubectl describe pod nginx-pod
./kubectl delete pod nginx-pod

./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
./kubectl get services
./kubectl describe service nginx-service
./kubectl delete service nginx-service

./kubectl apply -f manifest/replicaset/replicaset_nginx.yaml
./kubectl get rs
./kubectl describe rs nginx-rs
./kubectl delete rs nginx-rs
```

CNI 和 kube-proxy 需要 Linux network namespace、`ip`、`bridge`、`iptables`、
`nsenter` 和通常的 root 权限。只演示控制面对象和 Pod lifecycle 时，可以使用
`MINIK8S_CNI_DISABLED=1` 或 `sailer --proxy-disabled` 降低环境要求。网络实验后
可用 `./minik8s doctor clean` 清理 mooring 相关本地网络状态。

## 双机与 etcd 演示

远程服务器部署流程见 [docs/deploy.md](docs/deploy.md)。双机公共验收流程见
[docs/testcase/two-node.md](docs/testcase/two-node.md)。主路径是：

1. 在 node-a 启动 `bridge --listen :18080`。
2. 在 node-a/node-b 分别用对应 `manifest/node/*.yaml` 启动 `sailer`。
3. 控制面自动分配不同 PodCIDR，例如 `10.244.0.0/24` 和 `10.244.1.0/24`。
4. `sailer` 写入本机 CNI 配置并同步 VXLAN overlay。
5. 通过 `get nodes`、PodIP 互通、Service endpoints 和 iptables 规则验证结果。

etcd/Logbook 流程见 [docs/testcase/logbook.md](docs/testcase/logbook.md) 和
[docs/testcase/startup.md](docs/testcase/startup.md)。默认 `bridge` 会启动一个
私有本地 `sailer`，由该内部 worker 运行 static deps pod manifests 中的
`storage-etcd` 以及启用的 addon Pod，并把控制面连接到
`http://127.0.0.1:2379`。只有启用 `serverless` addon 时，bridge 才会同时设置
`nats://127.0.0.1:4222`。如果没有先运行 `init`，`bridge` 会回退到内置
`storage-etcd` 模板；启用 addon 时应先运行 `init` 生成对应 manifests。

默认 etcd 模式下，Pod、Service、ReplicaSet、Node 会写入 `/registry/...` 前缀。

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
| 自选 Serverless | 部分完成 | Function/EventTrigger/Workflow 对象、YAML/API/CLI、file/etcd store、HTTP invoke、NATS 订阅触发和 publish/doctor 辅助命令已有；事件 ack/retry、Workflow 自动执行、scale-to-0 未实现。 |
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
