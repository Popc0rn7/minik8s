# Minik8s Acceptance README

本文是最终验收入口说明。课程功能规格以 `docs/Handout.md` 为准，最终提交与脚本要求以`docs/FINAL.md` 为准；本文只记录助教运行脚本前需要知道的环境假设、入口和人工确认项。

## Submission

- Repository: https://github.com/Popc0rn7/minik8s
- Final branch: `main`
- Final tag: `v0.1.0`（最终提交后同步更新 tag）
- Final commit: 以TAG为主
- Install root on target machines: `/opt/minik8s`。
- Developer Contribution: 100% by 王启源522021910372

现有交付布局固定为：

```text
/opt/minik8s/
├── bin/
│   ├── minik8s
│   └── kubectl
├── scripts/
│   └── acceptance/
├── manifests/
├── demo/
│   └── serverless/
│       └── harbor-incident-triage/
├── state/ # 运行时状态数据
├── static-pods/ # 启动时的静态Pod
├── dns/
└── secrets/
    └── gpu-ssh/

/etc/cni/ # CNI安装
└── net.d/

/opt/cni/ # CNI安装
└── bin/
```

## Project Overview

Minik8s 是一个教学版 Kubernetes 核心闭环实现，控制面由 `bridge` 组合 API server 子集、controller-manager 子集和 scheduler 子集，数据面由 `sailer` 组合 kubelet 子集、node network agent 和 kube-proxy。CLI 入口在 `cmd/minik8s/`，主要业务实现位于 `internal/`，YAML 解析和默认值/校验位于 `pkg/yaml/`。

主要组件和代码路径：

| 组件 | 职责 | 代码路径 |
| --- | --- | --- |
| CLI | `apply/get/describe/delete`、bridge/sailer 启动和 serverless invoke/request | `cmd/minik8s/` |
| Harbor API | 资源 HTTP API、node join、metrics API、DNS API | `internal/bridge/harbor/` |
| Logbook | in-memory/file/etcd-backed 状态存储 | `internal/bridge/logbook/` |
| Navigator | Pod 调度，当前为 Ready 节点过滤加 Round-Robin | `internal/bridge/navigator/` |
| Controllers | Service、ReplicaSet、HPA、Job、Node lifecycle 等控制循环 | `internal/bridge/captain/` |
| Sailer | 节点本地 Pod 生命周期、CNI、proxy reconcile | `internal/sailer/` |
| CNI / Network | mooring CNI、跨节点 VXLAN/route 同步、iptables Service proxy | `internal/cniplugin/`, `internal/netagent/`, `internal/kubeproxy/` |
| Runtime | Docker/containerd 运行时适配 | `internal/runtime/` |
| Serverless | Function/EventTrigger/Workflow、activator、invocation worker | `internal/bridge/serverless/`, `internal/functionrunner/`, `internal/function/`, `internal/workflow/` |
| GPU Job | Job 类型、Slurm 脚本生成和 submitter controller | `internal/job/`, `internal/slurm/`, `internal/bridge/captain/job_controller.go` |

主要开源组件及用途：

| 组件 | 用途 |
| --- | --- |
| Go | 主实现语言 |
| Docker / containerd | 本地容器运行时 |
| Linux bridge / VXLAN / route | Pod 同节点和跨节点通信 |
| iptables | ClusterIP/NodePort Service 转发和负载均衡 |
| etcd | 设置 `MINIK8S_LOGBOOK_ENDPOINTS` 后作为 Logbook 后端 |
| NATS消息队列 | Serverless invoke、event trigger 和 workflow 调用链 |
| Slurm | GPU Job 远程提交到交我算平台 |

## TA Quick Start

所有脚本默认从 `/opt/minik8s` 运行，并先执行：

```bash
source scripts/acceptance/env.sh
```

| 顺序 | 验收项 | 入口 | 运行机器 | 预计时间 | 主要依赖 | 清理方式 |
| --- | --- | --- | --- | --- | --- | --- |
| 00 | 环境检查 | `bash scripts/acceptance/00_env_check.sh` | 三台机器均可，建议先在 node-a | 1-3 分钟 | Linux root、Docker、iptables、CNI 目录、端口和节点连通性 | 无资源创建 |
| 01 | 三节点部署与 Node 抽象 | `bash scripts/acceptance/01_node_multinode.sh bridge`、`bash scripts/acceptance/01_node_multinode.sh sailer <node>`、`bash scripts/acceptance/01_node_multinode.sh` | node-a 启动 bridge；node-a/node-b/node-c 启动各自 sailer；node-a 验证 | 5-10 分钟 | 三台机器互通、`TCP 18080`、`UDP 4789`、root 网络权限 | `bash scripts/acceptance/00_cleanup.sh` 或停止 minik8s service；CNI 残留用 `bash scripts/acceptance/01_node_multinode.sh cni-clean` |
| 02 | Pod 生命周期、多容器、调度、volume | `bash scripts/acceptance/02_pod_lifecycle.sh` | node-a | 5-8 分钟 | 01 已完成，多节点 Ready，Docker 镜像已就绪 | `bash scripts/acceptance/02_pod_lifecycle.sh cleanup` |
| 03 | Service ClusterIP/NodePort/endpoints | `bash scripts/acceptance/03_service.sh` | node-a | 5-8 分钟 | 01 已完成，kube-proxy/iptables 可用，NodePort 端口可访问 | `bash scripts/acceptance/03_service.sh cleanup` |
| 04 | ReplicaSet、Service 绑定、恢复 | `bash scripts/acceptance/04_replicaset.sh` | node-a | 5-8 分钟 | 01 已完成，Service 数据面可用 | `bash scripts/acceptance/04_replicaset.sh cleanup` |
| 05 | HPA、metrics、扩缩容 | `bash scripts/acceptance/05_hpa.sh` | node-a | 10-20 分钟 | 01 已完成，metrics addon、`polinux/stress:1.0.4` 镜像 | `bash scripts/acceptance/05_hpa.sh cleanup` |
| 06 | DNS host/path 转发 | `bash scripts/acceptance/06_dns_forwarding.sh` | node-a | 5-10 分钟 | 01 已完成，DNS addon，占用 host `80` 端口 | `bash scripts/acceptance/06_dns_forwarding.sh cleanup` |
| 07 | 控制面和 Node 容错 | `bash scripts/acceptance/07_fault_tolerance.sh` | node-a | 10-20 分钟 | 01 已完成，systemd/minik8s service 可被脚本重启 | `bash scripts/acceptance/07_fault_tolerance.sh cleanup` |
| 20 | 个人作业 GPU Job | `bash scripts/acceptance/20_personal_gpu.sh` | node-a | 10-30 分钟，取决于 Slurm 排队 | 01 已完成，交我算 SSH 凭据、submitter 镜像、Slurm 可访问 | `bash scripts/acceptance/20_personal_gpu.sh cleanup`，已提交 Slurm 任务会 best-effort `scancel` |
| 自选 | Serverless 日志检查应用 | 按本文 `Serverless - Minik8s 日志检查应用` 小节逐条运行；并发压测用 `wrk -t2 -c20 -d45s ...` | node-a | 20-40 分钟，建议结合录屏展示 | serverless addon、NATS、预构建函数镜像、`wrk` | 删除对应 Function/EventTrigger/Workflow/ReplicaSet，或执行 `bash scripts/acceptance/00_cleanup.sh` |

## 00 Environment Requirements

### 提供环境

**强烈建议使用作者提供的 3 台云主机进行测试，避免环境差异带来的问题。**

本项目配置好了三台符合要求的云主机，可以凭借专用ssh key在交大校园网下访问，详情见 `secrets/node-ssh`。

```bash
# node-a
ssh root@10.119.16.213 -i secrets/node-ssh/id_ed25519_minik8s
# node-b
ssh root@10.119.5.94 -i secrets/node-ssh/id_ed25519_minik8s
# node-c
ssh root@10.119.6.252 -i secrets/node-ssh/id_ed25519_minik8s
```

后续验收统一用内网地址访问，可以直接在三台机器上检查环境：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/00_env_check.sh
```

### 个人环境

`scripts/acceptance/00_env_check.sh` 会检查 OS、kernel、Docker、必要运行命令、目录、端口和基础连通性。远程验收机器只运行已部署的二进制，不检查 Go；Go 1.25.9 仅是编译/打包侧要求。以下条件仍需在运行前确认：

- 要求至少有三台环境一致的机器来构建多节点环境，而且三台机器通过能够通过内网或其他网络通信。
- 除了互联之外，也要保证安全组开放端口
  - `TCP 18080`
  - `UDP 4789`
- 保持网络环境干净，确保没有其他 CNI、iptables 管理程序，或端口占用: `153,80,2379,2380,4222,8080,8088,18080,30080,30082,30085`。如需清理 Minik8s 自研 mooring CNI 残留，先运行 `bash scripts/acceptance/01_node_multinode.sh cni-clean`。
- Docker 镜像依赖DockerHub/GHCR，建议提前load在三台机器本地，避免运行时网络问题，所用镜像见：`docs/acceptance/images.md`。
- CNI 和 kube-proxy 数据面需要 root 权限，要求三台机器上都用root用户操作，或手动sudo操作。Service 从 Pod 内访问 ClusterIP 还要求内核启用 `br_netfilter`，并设置 `net.bridge.bridge-nf-call-iptables=1`；当前 `sailer` 的 node network agent 会在启动时自动配置。
- Slurm平台需要身份验证，需要提前准备好密钥和证书，这里提供`secrets/gpu-ssh`。

然后请在`scripts/acceptance/env.sh`中修改为对应的网络环境并运行：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/00_env_check.sh
```

## 01 Deploy

`scripts/acceptance/01_node_multinode.sh` 是多机启动脚本，支持同一个入口显式建立 minik8s service 启动 bridge 或 sailer 节点。

推荐运行顺序如下。三台机器均从 `/opt/minik8s` 执行：

```bash
# node-a：启动 bridge，并 apply mooring CNI 兼容对象
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh bridge

# node-a：独立启动本机 worker sailer
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh sailer node-a

# node-b：仅作为 worker 启动 sailer
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh sailer node-b

# node-c：仅作为 worker 启动 sailer
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh sailer node-c

# node-a上验证集群状态
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh
```

Checklist:
- 启动过程，展现2, 4, 5, 6要求
- 最后的无参数验证，展现1, 3功能

Node设计：
- Node注册靠机器通过token加入Bridge，这时 Bridge 记录 node 对象，Node 记录 Harbor 地址。
- 关键字段说明：
  - `spec.role`：Worker/Master
  - `spec.podCIDR`：Bridge分配给该 Node 的 Pod CIDR
  - `status.phase`：Ready/NotReady/Unknown
  - `status.addresses`：Node IP 地址列表，至少包含 InternalIP
  - `status.conditions`：Node状态条件列表，至少包含 Ready 条件

## 02 Pod

`scripts/acceptance/02_pod_lifecycle.sh` 是 Pod 抽象和容器生命周期验收脚本。运行前需保证已经执行好01的多机部署，该脚本只需要在 node-a 上执行。

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/02_pod_lifecycle.sh
```

本次验收参考耗时：约 35s。

该脚本使用 `manifests/pod/`下的四个YAML执行：

**02.1 生命周期、配置参数和容错**
- 通过 `kubectl apply pod yaml` 创建 Pod 包含 `nginx:1.27-alpine` 的 web 容器和 `busybox:1.36` 的 sidecar 容器。
- YAML 中展示 Pod 基本字段和关键配置：`kind`、`metadata.name`、镜像名称和版本、`command/args`、`containerPort: 80`、CPU/Memory request 和 limit、`restartPolicy: Always`、`nodeSelector`、以及共享 volume 挂载。
- 支持 `kubectl describe`（符合 `docker ps` / `docker inspect`），展示本地容器镜像、命令、资源限制和网络命名空间。
- 脚本用 `docker kill` 终止 sidecar 容器，随后检查该容器重启，且 `restartCount` 增加。
- 最后通过 `kubectl delete` 删除 Pod，并确认对象已经不存在。

**02.2 同一 Pod 内 localhost 通信**
- 创建多容器 Pod，由其中 web 容器提供 nginx 服务。
- sidecar 容器通过 `docker exec` 执行 `wget -qO- http://127.0.0.1/`访问web 容器里的 nginx HTTP 服务，验证同一 Pod 内多容器共享网络命名空间，可以用 localhost 访问彼此服务。

**02.3 多机调度**
- 通过 `kubectl apply` 创建3个不指定 `nodeSelector` 的 Pod，交给 bridge 内的 navigator 分配到节点。
- 通过 `nodeName`、状态和调度结果，检查这些 Pod 至少分布到两个不同节点，展示多机调度真实生效。
- 当前调度策略是 Round-Robin： 先选择 Ready 节点，按节点名排序，再对 Pod 的 CPU/Memory requests 进行审查，过滤不满足节点。

**02.4 Volume 文件共享**
- 复用 02.2 的 Pod，声明 `hostPath` volume，并分别挂载到 web 容器的 `/usr/share/nginx/html/shared` 和 sidecar 容器的 `/shared`。
- 在 web 容器中写入 `from-web.txt`，再在 sidecar 容器中读取同一文件内容，验证同一 Pod 内多个容器共享 volume。同时进行了双向验证。

只有在意外中断下需要手动清理资源：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/02_pod_lifecycle.sh cleanup
```

## 03 Service

`scripts/acceptance/03_service.sh` 是 Service 抽象、endpoint controller 和 kube-proxy 数据面验收脚本。运行前需保证已经执行好 01 的多机部署，脚本只需要在 node-a 上执行

本次验收参考耗时：约 60s。

该脚本使用 `manifests/service/`下的五个YAML执行：

**03.1&2 Service 创建删除、基本信息和 selector**
- 创建两个分别固定到 node-a 和 node-b 的 Nginx Pod，label 均包含 `app=svc-03-nginx`。
- 通过 `kubectl apply` 创建 `svc-03-clusterip`。YAML 中展示 `kind`、`metadata.name`、`type: ClusterIP`、`selector.matchLabels.app`、`port: 80`、`targetPort: 80`。
- 脚本会展示 Service 创建前不存在，创建后 endpointCount 从无到 2，说明 Service 创建过程触发 selector/endpoints 建立。
- 运行 `kubectl describe svc svc-03-clusterip`，展示 Selector、虚拟 IP、Port/TargetPort 和 Endpoints，2个 Endpoint 对比 Pod IP 证明 Service 动态绑定后端。
- 通过 `kubectl delete` 删除 Service 并确认对象不存在；随后重新创建该 Service 并再次恢复 endpointCount=2。

**03.3 ClusterIP 集群内访问**
- 复用 03.1&2 的 Pod 和 Service，并创建新 Pod 作为集群内访问客户端。
- node-a 宿主机通过 `curl http://<clusterIP>:80/` 访问 Service，证明宿主机可以访问 Service VIP。
- client Pod 通过 `docker exec` 执行 `wget -qO- http://<clusterIP>:80/`，证明集群内 Pod 可以通过虚拟 IP 访问 Service 后端。

**03.4 NodePort 集群外访问**
- 创建新 Service `svc-03-nodeport`，使用固定 NodePort `30080`，selector 同样指向两个 nginx 后端。
- node-a 宿主机通过 `curl http://<node-a-ip>:30080/` 访问本机 NodePort。
- 脚本还会尝试从 node-a 访问 node-b 的 `30080`。如果云网络或防火墙限制跨节点 NodePort，会输出 `[LIMITED]`，但本机 NodePort 访问必须有成功证据。

**03.5 Endpoints 动态更新**
- 初始状态下 `svc-03-clusterip` 应有两个 endpoints。
- 删除 Pod `svc-03-nginx-b` 后，脚本等待 Service endpoint 从 2 个变为 1 个，并检查剩余 endpoint 对应 `svc-03-nginx-a`。
- 重新 apply `svc-03-nginx-b` 后，脚本等待 endpoint 恢复为 2 个，展示 selector 和 endpoint controller 的动态更新能力。

只有在意外中断下需要手动清理资源：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/03_service.sh cleanup
```

## 04 ReplicaSet

`scripts/acceptance/04_replicaset.sh` 是 ReplicaSet 抽象、Service 绑定、负载均衡和副本恢复能力验收脚本。运行前需保证已经完成 01 多机部署，脚本只需要在 node-a 上执行。

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/04_replicaset.sh
```

本次验收参考耗时：约 91s。

该脚本使用 `manifests/replicaset/`下的两个YAML执行：

**04.1 ReplicaSet 创建删除、基本信息和多机调度**
- 通过 `kubectl apply` 创建 ReplicaSet，YAML 中展示 `kind`、`metadata.name`、`replicas: 3`、selector、Pod template、`busybox:1.36`、`containerPort: 8080` 和 CPU/Memory request/limit。
- 运行 `kubectl describe rs`，展示 ReplicaSet 名称、Desired/Current、Selector 和 owned Pods。
- 脚本等待 3 个 Pod 变为 Running，并输出每个 Pod 的 `nodeName`、`podIP` 和 label。
- 检查 3 个 Pod 至少分布到 2 个节点，展示 ReplicaSet 创建的 Pods 进入多机调度。
- 通过 `kubectl delete rs rs-04-web` 删除 ReplicaSet，并确认对应 owned Pods 和 ReplicaSet 对象已经不存在。

**04.2 ReplicaSet 绑定 Service 和负载均衡**
- 重新创建 ReplicaSet，并创建同 selector 的 NodePort Service `rs-04-web`，固定 `nodePort: 30081`。
- 运行 `kubectl describe svc`，展示 Selector、ClusterIP、Port/TargetPort、NodePort 和 3 个 Endpoints。
- node-a 宿主机通过 `curl http://<node-a-ip>:30081/` 访问 Service，后端 busybox httpd 会返回当前 Pod 的 hostname 和 Pod IP。
- 脚本会连续访问本机 NodePort 并汇总返回的后端 Pod IP，要求至少命中 2 个不同后端，证明 Service 流量进入 ReplicaSet 的多个 Pod。
- 负载均衡策略采取 iptables random DNAT，用随机化方式简单保证流量分布均匀。展示 kube-proxy 写入的 `--mode random` 和 3 条 DNAT 后端规则，说明本组负载均衡策略是 iptables random DNAT。

**04.3 ReplicaSet 恢复能力**
- 复用 04.2 保留的 ReplicaSet，初始状态下应有 3 个 Running Pods。
- docker 删除其中一个 ReplicaSet-owned Pod 后，等待 controller 自动补回 3 个 Running Pods，并再次展示 Desired/Current/Running 数量。
- 输出删除前 Pod 集合、被删除的 Pod 名称和恢复后的 Pod 集合，证明 ReplicaSet 创建了 replacement Pod。

只有在意外中断下需要手动清理资源：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/04_replicaset.sh cleanup
```

## 05 HPA

`scripts/acceptance/05_hpa.sh` 是 HorizontalPodAutoscaler、metrics API 和 ReplicaSet 动态伸缩验收脚本。运行前需保证 01 多机部署已完成，metrics addon 已启用，脚本只需要在 node-a 上执行。

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/05_hpa.sh
```

本次验收参考耗时：约 5 minutes。

该脚本使用 `manifests/hpa/`下的 ReplicaSet、Service 和一个 HPA YAML 执行，脚本小节号对应 `docs/FINAL.md` 的 7.5 小节。

**05.1 HPA 配置和创建（对应 7.5.1）**
- 展示 `hpa_05_acceptance.yaml` 的 HPA 摘要。
- 创建 ReplicaSet 和固定 NodePort `30082` Service。
- 等待 `pods.metrics.k8s.io` 发现 ReplicaSet 的 Pod metrics，metrics server 监控 CPU/Memory，输出 Pod、CPU、memory 和 timestamp 摘要。
- 通过 `kubectl apply` 创建 HPA，再运行 `kubectl get hpa` 和 `kubectl describe hpa`，副本上下限为 1 到 3，且包含 CPU/Memory utilization 指标。

**05.2 扩缩容时机**
- 复用上一节的的资源RS/SRV。
- ReplicaSet Pod 中的 `polinux/stress:1.0.4` sidecar 执行真实 `stress --cpu 1 --timeout 60s`，制造 CPU load。
- 脚本固定输出五个 observation 时间点：`before-load`、`scale-up-trigger`、`after-scale-up`、`after-load`、`scale-down-trigger`。
- 每个 observation 同时展示 metrics 采集值、HPA 判断、RS Desired/Current、Running Pods 和 Service endpoints，最后说明观测到的最大副本数 3 和最小副本数 1。
- 当前 metrics 实现如下：`sailer` 采集 Pod CPU/memory metrics 并上报给 bridge，bridge 暴露最小 `metrics.k8s.io` API 给 metrics server，HPA controller 读取 metrics server 的 CPU/Memory utilization 信息计算目标副本数。

**05.3 扩缩容速度**
- `hpa_05_acceptance.yaml` 显式配置简化版 `spec.behavior`：`syncIntervalSeconds: 15`、扩容每次最多 `1` 个副本、缩容每次最多 `1` 个副本、缩容 `cooldownSeconds: 30`。
- 对比05.2，05.3 会额外应用 `hpa_05_fast.yaml`，把 `syncIntervalSeconds` 改为 `5`，把扩容/缩容单轮最大步长改为 `2`，并把缩容 cooldown 改为 `0`。展示快速扩缩容路径。
- 当前实现是 Kubernetes HPA 的简化版，只支持比较静态的 policy 改动

**05.4 扩缩容后访问目标 Pod（对应 7.5.4）**
- 重新 apply 常规 HPA YAML
- 重新触发 stress sidecar 并再次扩容到 3。
- 检查扩容后的 Pod 分布在多个节点，Service endpoints 为 3。
- 通过 `curl http://<node-a-ip>:30082/` 多次访问 NodePort，统计返回中的 `pod=` 或 `ip=`，证明扩容后的多个后端 Pod 可以接收流量。

只有在意外中断下需要手动清理资源：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/05_hpa.sh cleanup
```

## 06 DNS

`scripts/acceptance/06_dns_forwarding.sh` 是 DNS 对象、DNS sync 文件、host/path 转发和 Pod 内域名访问验收脚本。运行前需保证 01 多机部署已完成，bridge 已启用 DNS addon，端口 80 由 DNS ingress 使用，脚本只需要在 node-a 上执行。

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/06_dns_forwarding.sh
```

本次验收参考耗时：约 60s。

该脚本使用 `manifests/dns/` 下的六个固定 manifest：

**06.1 配置域名和子路径**
- 创建两个 1 副本后端 ReplicaSet，分别返回 `route=alpha` 和 `route=beta`。
- 创建两个 ClusterIP Service：`service-06-alpha` 和 `service-06-beta` 分别对应后端。
- 用 `kubectl apply` 创建 DNS 对象 `dns-06-routes`，host 为 `acceptance06.minik8s.local`，`/alpha` 指向 `service-06-alpha:80`，`/beta` 指向 `service-06-beta:80`。
- 运行 `kubectl get/describe dns dns-06-routes`，并检查配置文件里出现该 host 和两个 Service route target。
- 检查 DNS addon 自动创建的 `minik8s-system/minik8s-dns` ClusterIP Service，该 Service 对 Pod 暴露 `53/TCP` 和 `53/UDP`，并转发到 node-a 的 DNS addon host port。

**06.2 通过域名和子路径访问 Service**
- 从 node-a 宿主机用 `Host: acceptance06.minik8s.local` 访问 `http://127.0.0.1/alpha` 和 `/beta`。
- `/alpha` 必须返回 `route=alpha`，`/beta` 必须返回 `route=beta`，证明同一域名下不同路径转发到不同 Service。
- 输出 `routes.json` 作为 gateway route snapshot 证据。
- 在 DNS addon 已启用后创建 `pod-06-client`，检查 `/etc/resolv.conf` 中的 nameserver 是 `minik8s-dns` Service 的 ClusterIP。
- 从 Pod 内访问 `http://acceptance06.minik8s.local/alpha` 和 `/beta`，要求分别返回 `route=alpha` 和 `route=beta`。
- 删除 `dns-06-routes` 后，确认 sync 文件移除该 host，且 host ingress 不再服务该域名。

只有在意外中断下需要手动清理资源：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/06_dns_forwarding.sh cleanup
```

## 07 Fault Tolerance

`scripts/acceptance/07_fault_tolerance.sh` 是 Pod/Service 控制面重启容错、Node 故障检测、ReplicaSet 恢复、Service endpoint 移除和节点恢复验收脚本。运行前需保证 01 多机部署已完成，脚本只需要在 node-a 上执行。

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/07_fault_tolerance.sh
```

本次验收参考耗时：约 4 minutes。

该脚本使用 `manifests/fault/` 下的固定 manifest：

**07.1 Pod 和 Service 容错**
- 启动 Pod 和 Service
- 手动 stop bridge 服务，包括 api-server, controller scheduler, 本设计中 `etcd` 由 bridge 内置的 private-kubelet 管理，暂时会同步清理`etcd`容器，但保证数据持久化且不影响语义。
- 在重启中检查 Pod 对应的 Docker 对象存活.
- 在重启中检查 Service 对应的 iptables 规则和 CNI 配置仍然存在。
- 手动 start bridge 服务，用 `kubectl get` 检查 Pod 和 Service 状态
- Service 访问验证：脚本在 bridge 重启前后分别访问同一个 NodePort Service，记录返回的后端 `pod=`/`ip=`；重启后仍能访问且 Service endpoints 恢复为 3，证明控制面重启没有破坏已有数据面和恢复后的对象状态。

**07.2 Node 容错**
- 手动 stop node-a 上的 sailer 服务，模拟失活。
- 用 `kubectl get nodes` 发现 node-a 失活
- 网络流量验证：ReplicaSet replacement Pods 全部迁出 node-a，Service endpoints 不再包含失效节点旧 Pod，同时 NodePort 仍能访问剩余健康后端。
- Pod 调度验证：手动 apply 一个新的 nodeSelector 为 node-a 的 Pod，Pending。
- 节点恢复验证：重新启动 node-a 的 `sailer` 后，检查 Node 恢复为 `Ready`；在原本的 ReplicaSet 上删除一个非 node-a 的 Pod 触发补副本，观察 replacement Pod 可再次调度到 node-a，先前nodeSelector 为 node-a 的 Pod 也 由Pending 变为 Running，且 Service endpoints 恢复包含 node-a 的 Pod。

只有在意外中断下需要手动清理资源：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/07_fault_tolerance.sh cleanup
```

## 20 Personal GPU Job

`scripts/acceptance/20_personal_gpu.sh` 是个人作业 GPU 验收脚本。运行前需保证 01 多机部署已完成，脚本只需要在 node-a 上执行。

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/20_personal_gpu.sh
```

共三个GPU Job，每个 GPU Job 最多等待约 2 分钟，具体时间依排队/网络问题定。

GPU 验收依赖交我算 SSH 凭据，这里提供我配置好的私钥 `secrets/gpu-ssh`，验收时需保证上传到远程机器的 `/opt/minik8s/secrets/gpu-ssh`，如果需要用自己的私钥可以参考[交我算文档](https://docs.hpc.sjtu.edu.cn/login/sshlogin.html#label-no-password-login)：

```text
/opt/minik8s/secrets/gpu-ssh/
├── id_ed25519_minik8s
├── id_ed25519_minik8s-cert.pub
├── config
└── known_hosts
```

本次验收参考耗时：约 72s（本次为外部 DNS 解析失败后退出并完成清理；排队场景单个 GPU Job 默认等待窗口约 2 分钟）。
该脚本使用 `manifests/job/` 下的三个 YAML 和两个 CUDA 程序执行：

**08.1 CUDA 程序、Job YAML 和 Slurm 凭据**
- 展示 CUDA 程序和编辑脚本。
- 展示 Job YAML 中的 `apiVersion: batch/v1`、`kind: Job`、`metadata.name`、`selector.matchLabels.accelerator: gpu`、`source.files`、`source.command`、`spec.slurm` 和 `spec.remote`。
- 检查密钥和Slurm连接成功。

**08.2 Vector Add 实验**
- 通过 `kubectl apply` 创建 Job `cuda-add`，通过一维 grid/block 把 N = 1048576 个元素并行分配到 GPU 线程执行。
- 等待控制面创建独立 submitter Pod/Service：`job-cuda-add-submitter`。
- 运行 `kubectl get / describe` 等操作，展示 Job 状态、remote host、remote dir、Slurm Job ID、submitter Pod 和 submitter Service。
- 等待 Job 从 `PodCreating/Preparing/Uploading/Submitted/Running/Collecting` 进入终态，默认等待窗口约 2 分钟。
- 如果进入 `Succeeded`，运行 `kubectl logs job cuda-add`，得到CUDA程序的期望输出。
- 如果脚本等待窗口内仍未进入 `Succeeded`，但 Job 已经提交到 Slurm，脚本输出当前 `Phase`、`Message`、`Slurm Job ID`、`Remote Dir`、`StartTime` 和可复制的 `squeue/sacct` 查询命令，作为等待交我算队列/运行中的状态证据并判定本小节通过。若超时且没有 Slurm Job ID，则判定失败。

**08.3 Job 隔离性**
- 通过 `kubectl apply -f manifests/job/cuda-add-2.yaml` 再提交一个同类 GPU Job。
- 检查两个 Job 各自拥有独立 submitter Pod 和 Service：
  - `job-cuda-add-submitter`
  - `job-cuda-add-2-submitter`
- 检查两个 Job 的 `Remote Dir` 不同；在两个任务都提交成功时检查 `Slurm Job ID` 不同。

**08.4 复杂 CUDA tiled matrix multiplication**
- 通过 `kubectl apply` 提交 Job `cuda-matmul-tiled`，使用二维 16 x 16 block 和 64 x 64 grid 并行计算，用 shared memory 缓存 A/B 的 tile，并通过 __syncthreads() 协调线程，减少 global memory 访问，体现 GPU 并发和内存层次优化。
- 如果进入 `Succeeded`，运行 `kubectl logs job cuda-matmul-tiled`，要求输出包含 `Matrix N = 1024`、`Tile size = 16`、`Block = 16 x 16`、`Grid = 64 x 64`、`Kernel: tiled shared-memory matrix multiplication` 和 `Result: PASS`。
- 如果脚本等待窗口内仍未进入 `Succeeded`，但 Job 已经提交到 Slurm，脚本同样输出当前阶段、Slurm Job ID、远端目录和 `squeue/sacct` 查询命令，以该 pending/running 状态替代 CUDA 程序 stdout 作为通过证据。若超时且没有 Slurm Job ID，则判定失败。

脚本默认自动清理资源；需要额外确认或补救时可以手动执行：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/20_personal_gpu.sh cleanup
```

清理会删除 `cuda-add`、`cuda-add-2` 和 `cuda-matmul-tiled` 三个 Job；如果 Job 已经提交 Slurm，控制面会 best-effort 执行 `scancel <jobid>`。

## Serverless - Minik8s 日志检查应用

本段介绍展示 `harbor-incident-triage` demo，覆盖 Final 中 Serverless 的 Function、Workflow、EventTrigger、按需启动、并发伸缩和 scale-to-zero 要求。

**!!!重要!!!** 以下流程耗时较长，所以文档仅作为说明，具体演示请参考加速过的演示录屏：[录屏](https://pan.sjtu.edu.cn/web/share/b6ee991d6d7fa93a2739b677c99fa93b)

**前置设置**
```bash
cd /opt/minik8s
source scripts/acceptance/env.sh

kubectl apply -f manifests/serverless/harbor-incident-triage/functions/normalize-input.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/tiny-log-classifier.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/network-diagnose.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/runtime-diagnose.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/build-diagnose.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/app-diagnose.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/quick-reply.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/notify-captain.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/compose-report.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/workflow.yaml
kubectl apply -f manifests/serverless/harbor-incident-triage/eventtrigger.yaml
```

这里不要使用 `functions/*.yaml`，因为 `revision-probe-v1.yaml` 和
`revision-probe-v2.yaml` 是同名函数的更新演示材料。如果通配符一次性 apply，
会提前覆盖版本，导致后面的更新前后对比不清楚。

**01. 查看声明式配置**
```bash
kubectl get functions
kubectl describe workflow harbor-incident-triage
kubectl describe eventtrigger harbor-incident-created
```

启动 watch 脚本观察集群工作状态：
```bash
cd /opt/minik8s/demo/serverless/harbor-incident-triage
./scripts/watch-scale.sh tiny-log-classifier
```

**02. Function 更新与删除**

本节单独覆盖 Final Serverless 第 21、22 点，使用不参与主 Workflow 的
`revision-probe`，避免影响后续 `harbor-incident-triage` 展示。

上传 v1 并调用：
```bash
cd /opt/minik8s
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/revision-probe-v1.yaml
kubectl get functions
kubectl describe function revision-probe

minik8s invoke function revision-probe \
  --data "$(cat manifests/serverless/harbor-incident-triage/inputs/revision-probe.json)"
```

展示点：输出中应包含 `revision":"v1"` 和 `message":"first uploaded version"`。

更新为 v2 并再次调用：
```bash
kubectl apply -f manifests/serverless/harbor-incident-triage/functions/revision-probe-v2.yaml
kubectl describe function revision-probe

minik8s invoke function revision-probe \
  --data "$(cat manifests/serverless/harbor-incident-triage/inputs/revision-probe.json)"
```

展示点：同一个 Function 名称 `revision-probe`，再次调用返回
`revision":"v2"` 和 `message":"updated function version"`，证明函数更新前后结果可区分。

删除函数并展示删除后调用结果：
```bash
kubectl delete function revision-probe
kubectl get functions

minik8s invoke function revision-probe \
  --data "$(cat manifests/serverless/harbor-incident-triage/inputs/revision-probe.json)"
```

展示点：删除后 `kubectl get functions` 不再包含 `revision-probe`，再次 invoke 应返回
not found 或调用失败结果，证明删除生效。

**03. Workflow 普通分支**
手动 Invoke 简单的 Workflow 对象，输入日志 json 处理
```bash
minik8s invoke workflow harbor-incident-triage \
  --data "$(cat manifests/serverless/harbor-incident-triage/inputs/network-incident.json)"
```

系统按需创建函数 Pod，并返回分类、风险增强和报告生成结果。展示时检查输出中的分类结果、诊断分支、风险字段和最终报告文本，证明函数输出随输入日志变化，并且上游函数结果会传递给下游函数。

**04. Workflow critical 分支**
手动 Invoke critical 条件分支的 Workflow 对象，输入日志 json 处理
```bash
minik8s invoke workflow harbor-incident-triage \
  --data "$(cat manifests/serverless/harbor-incident-triage/inputs/critical-incident.json)"
```

critical 输入会经过 `captain-notifier` 分支，输出中应能看到
`severity=critical`、`notified=true` 或等价字段，说明 Workflow 支持条件分支。

**05. EventTrigger 主动事件触发**
通过触发 EventTrigger 的 subject 发送事件，触发 Workflow 执行，注意当前实践没有设计 audit 来绑定 EventTrigger，request是实验的一个模拟接口，模拟 audit 发现事件后激活消息队列对应事件：
```bash
minik8s request minik8s.incident.created \
  --data "$(cat manifests/serverless/harbor-incident-triage/inputs/low-risk-incident.json)" \
  --timeout 90s
```

用户向 EventTrigger 绑定的 subject 发送事件。

**06. 并发压测和自动扩缩容**

发起 wrk 压测：
```bash
cd /opt/minik8s
wrk -t2 -c20 -d45s --timeout 120s \
  -s manifests/serverless/harbor-incident-triage/wrk/tiny-log-classifier.lua \
  http://127.0.0.1:18080/
```

- wrk 使用 manifest 中的 Lua 请求体反复调用 `tiny-log-classifier`。
- 监控窗口中 `fn-tiny-log-classifier` ReplicaSet/Pod 数量会从 0 或 1 增长到5个副本。
- 压测停止并超过 idle timeout 后，函数副本会自动收缩，Pod 最终消失或回到 0
副本，体现 Serverless scale-to-zero。

## CICD

GitHub Actions 配置位于 `.github/workflows/`：

- `ci.yml`：在 PR 以及 push 到 `main`、`dev` 时触发，执行 `golangci-lint fmt --diff`、`golangci-lint`、`go vet ./...`、`go test -race -covermode=atomic -coverprofile=coverage.out ./...` 和 `go build ./...`，并构建 `dist/minik8s`、`dist/kubectl`。
- `release.yml`：在 `v*` tag push 时触发，先复用格式、lint、vet、test、build 检查，再构建 Linux amd64/arm64 二进制包并发布 GitHub Release。
- `docker-image.yml`：在 push 到 `main` 或手动触发时构建并推送 `ghcr.io/popc0rn7/minik8s`、`ghcr.io/popc0rn7/mooring-cni`、`ghcr.io/popc0rn7/gpu-submitter`。
- `ai-summary.yml`：在非 `main/dev/dependabot` 分支 push 或手动触发时生成分支变更摘要；没有配置 API key 时会跳过外部调用。

## Software Testing

测试分为三类：

- 单元测试：每个GO源文件都会配备一个对应单元测试，常用命令为 `go test ./pkg/yaml ./internal/bridge/logbook ./internal/bridge/captain ./internal/bridge/harbor ./internal/sailer ./internal/kubeproxy ./test/integration -count=1`。
- 构建验证：运行 `make build` 生成 `bin/minik8s`、`bin/kubectl`、`bin/mooring`，或运行 CI 中的 `go build ./...`、`go build -o dist/minik8s ./cmd/minik8s`、`go build -o dist/kubectl ./cmd/kubectl`。
- 端到端/真机验收：主要依赖人工/AI登陆到远程机器集群，以 `scripts/acceptance/00_env_check.sh` 到 `scripts/acceptance/07_fault_tolerance.sh` 覆盖基础功能，以 `scripts/acceptance/20_personal_gpu.sh` 覆盖 GPU 个人作业，以 Serverless demo 命令覆盖自选功能展示。

## AI Usage

该项目共计约6万行代码和文档，90%的代码由AI落实，交叉使用Claude Code和Codex，具体合作方法如下：
- 确定参考 Handout.md 和 Acceptance.md，以及Kubernetes原生实现，对代码规则和项目风格在AGENTS.md中进行明确说明。
- 实际开发并行，用git worktree来一起开发不同模块，保持代码风格和设计一致。
- 分支推到Github上后由AI Summary和AI Review协助验收，然后合并进dev分支。
- 具体开发借助设计testcase文档作为实现目标来引导AI开发，开发过程中遵循，先明确语义，设计计划再开发，测试，真机验证的流程，保持和设计文档的对齐。

## Develop Process

开发流程按开源协作方式组织：

1. 从 `dev` 拉出功能分支，只有可用版本更新才合并 `main`，功能分支按 Pod、Service、ReplicaSet、HPA、DNS、Serverless、GPU Job 等模块独立开发。
2. 功能完成后本地运行相关单元测试、format/lint检查后push。
3. 通过 PR 合并到集成分支，CI 检查格式、lint、vet、测试和构建。
4. 验收前将稳定版本合入 `main`，把所用镜像上传GHCR，打 `v0.1.0` tag。
