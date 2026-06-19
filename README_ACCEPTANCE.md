# Minik8s Acceptance README

本文是最终验收入口说明。课程功能规格以 `docs/Handout.md` 为准，最终提交与脚本要求以`docs/FINAL.md` 为准；本文只记录助教运行脚本前需要知道的环境假设、入口和人工确认项。

## Submission

- Repository: https://github.com/Popc0rn7/minik8s
- Final tag: v0.1.0
- Final commit: TODO
- Install root on target machines: `/opt/minik8s`。

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

## 00 Environment Requirements

### 提供环境

本项目配置好了三台符合要求的云主机，可以凭借专用ssh key在交大校园网下访问，详情见 `secrets/node-ssh`。

```bash
# node-a
ssh root@10.119.16.213 -i secrets/node-ssh/id_ed25519_minik8s
# node-b
ssh root@10.119.5.94 -i secrets/node-ssh/id_ed25519_minik8s
# node-c
ssh root@10.119.6.252 -i secrets/node-ssh/id_ed25519_minik8s
```

固定验收内网 IP：

| Node | 内网 IP |
| --- | --- |
| node-a | `192.168.1.4` |
| node-b | `192.168.1.10` |
| node-c | `192.168.1.15` |

直接检查环境：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/00_env_check.sh
```

### 个人环境

`scripts/acceptance/00_env_check.sh` 会检查 OS、kernel、Go、Docker、必要命令、目录、端口和基础连通性。以下条件仍需在运行前确认：

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

脚本日志遵循 `docs/FINAL.md` 的验收格式：每条检查输出 `[RUN]`、`[EXIT]`、`[OUTPUT]` 和对应结论 `[PASS]`/`[FAIL]`/`[LIMITED]`，最后输出 `[CLEANUP]` 和 `[END]`。`00_env_check.sh` 只做环境预检，不执行 `minik8s init`、不 `kubectl apply` CNI、不运行 `go test`，也不创建或删除集群资源。

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

单独清理 02 资源可运行：

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/02_pod_lifecycle.sh cleanup
```

## 03 Service

`scripts/acceptance/03_service.sh` 是 Service 抽象、endpoint controller 和 kube-proxy 数据面验收脚本。运行前需保证已经执行好 01 的多机部署，脚本只需要在 node-a 上执行

只有在意外中断下需要手动清理资源：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/03_service.sh
```

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

该脚本使用 `manifests/hpa/`下的 ReplicaSet、Service 和一个 HPA YAML 执行，脚本小节号对应 `docs/FINAL.md` 的 7.5 小节。

**05.1 HPA 配置和创建（对应 7.5.1）**
- 只展示 `hpa_05_acceptance.yaml` 的 HPA 摘要，包含 `kind`、`metadata.name`、target workload、`minReplicas`、`maxReplicas`、`behavior` 扩缩容速度策略和 CPU/Memory metrics。
- 创建 ReplicaSet 和固定 NodePort `30082` Service，并复用这组资源进入 05.2。
- 等待 `pods.metrics.k8s.io` 发现 ReplicaSet 的 Pod metrics，metrics server 监控 CPU/Memory，输出 Pod、CPU、memory 和 timestamp 摘要。
- 通过 `kubectl apply` 创建 HPA，再运行 `kubectl get hpa` 和 `kubectl describe hpa`，副本上下限为 1 到 3，且包含 CPU/Memory utilization 指标。

**05.2 扩缩容时机（对应 7.5.2）**
- 不重新创建 RS/Service/HPA，直接复用 05.1 的资源。
- ReplicaSet Pod 中的 `polinux/stress:1.0.4` sidecar 执行真实 `stress --cpu 1 --timeout 60s`，制造 CPU load。
- 脚本固定输出五个 observation 时间点：`before-load`、`scale-up-trigger`、`after-scale-up`、`after-load`、`scale-down-trigger`。
- 每个 observation 同时展示 metrics 采集值、HPA 判断、RS Desired/Current、Running Pods 和 Service endpoints，最后说明观测到的最大副本数 3 和最小副本数 1。
- 当前 metrics 链路是教学版实现：`sailer` 采集 Pod CPU/memory metrics 并上报给 bridge，bridge 暴露最小 `metrics.k8s.io` API，HPA controller 读取 CPU/Memory utilization 计算目标副本数。

**05.3 扩缩容速度（对应 7.5.3）**
- `hpa_05_acceptance.yaml` 显式配置教学版 `spec.behavior`：`syncIntervalSeconds: 15`、扩容每次最多 `1` 个副本、缩容每次最多 `1` 个副本、缩容 `cooldownSeconds: 30`。
- `05_hpa.sh` 在 05.1 的 manifest summary 和 `kubectl describe hpa` 中展示该策略；05.2 的 replica path 继续展示实际副本数按配置逐步变化。
- 05.3 会额外应用 `hpa_05_fast.yaml`，把 `syncIntervalSeconds` 改为 `5`，把扩容/缩容单轮最大步长改为 `2`，并把缩容 cooldown 改为 `0`。脚本会展示 fast YAML 摘要、`kubectl describe hpa` 中的 fast behavior，以及 1 到 3、3 到 1 的快速副本变化路径。
- 05.3 结束时会重新 apply 普通 `hpa_05_acceptance.yaml`，让 05.4 继续使用常规策略做扩缩容后访问验证。
- 当前实现参考 Kubernetes HPA behavior 的结构，但只支持固定副本步长和缩容冷却，不支持 `policies[]`、Percent、`periodSeconds`、`selectPolicy` 和 stabilization window。

**05.4 扩缩容后访问目标 Pod（对应 7.5.4）**
- 在 05.2 缩回 1 个副本后，删除当前 RS-owned Pod，让 ReplicaSet 用同一模板重建 fresh Pod，重新触发 stress sidecar 并再次扩容到 3。
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

该脚本使用 `manifests/dns/` 下的固定 manifest：

- `manifests/dns/replicaset_06_alpha.yaml`
- `manifests/dns/replicaset_06_beta.yaml`
- `manifests/dns/service_06_alpha.yaml`
- `manifests/dns/service_06_beta.yaml`
- `manifests/dns/dns_06_routes.yaml`
- `manifests/dns/pod_06_client.yaml`

脚本小节号对应 `docs/FINAL.md` 的 7.6 小节：

**06.1 配置域名和子路径（对应 7.6.1）**
- 创建两个 1 副本后端 ReplicaSet，分别返回 `route=alpha` 和 `route=beta`。
- 创建两个 ClusterIP Service：`service-06-alpha` 和 `service-06-beta`，并等待各自 endpoint ready。
- 创建 DNS 对象 `dns-06-routes`，host 为 `acceptance06.minik8s.local`，`/alpha` 指向 `service-06-alpha:80`，`/beta` 指向 `service-06-beta:80`。
- 运行 `kubectl get/describe dns dns-06-routes`，并检查 `/opt/minik8s/dns/hosts`、`/opt/minik8s/dns/routes.json` 出现该 host 和两个 Service route target。
- 检查 DNS addon 自动创建的 `minik8s-system/minik8s-dns` ClusterIP Service，该 Service 对 Pod 暴露 `53/TCP` 和 `53/UDP`，并转发到 node-a 的 DNS addon host port。

**06.2 通过域名和子路径访问 Service（对应 7.6.2 宿主机访问）**
- 从 node-a 宿主机用 `Host: acceptance06.minik8s.local` 访问 `http://127.0.0.1/alpha` 和 `/beta`。
- `/alpha` 必须返回 `route=alpha`，`/beta` 必须返回 `route=beta`，证明同一域名下不同路径转发到不同 Service。
- 输出 `routes.json` 作为 gateway route snapshot 证据。

**06.3 Pod 内访问和删除行为（对应 7.6.2 Pod 内访问，并补充删除状态清理）**
- 在 DNS addon 已启用后创建 `pod-06-client`，检查 `/etc/resolv.conf` 中的 nameserver 是 `minik8s-dns` Service 的 ClusterIP。
- 从 Pod 内访问 `http://acceptance06.minik8s.local/alpha` 和 `/beta`，要求分别返回 `route=alpha` 和 `route=beta`。
- 删除 `dns-06-routes` 后，确认 sync 文件移除该 host，且 host ingress 不再服务该域名。

脚本按 `06.1`、`06.2`、`06.3` 分别清理资源并输出 `[END] status=<PASS|FAIL|LIMITED>`；最后输出总结果，例如 `[END] status=3/3PASS`。单独清理 06 资源：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/06_dns_forwarding.sh cleanup
```

## 07 Fault Tolerance

`scripts/acceptance/07_fault_tolerance.sh` 是 Pod/Service 控制面重启容错、Node 故障检测、ReplicaSet 恢复、Service endpoint 移除和节点恢复验收脚本。运行前需保证 01 多机部署已完成，脚本只需要在 node-a 上执行。

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/07_fault_tolerance.sh
```

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
- 网络流量验证：脚本先确保同一个 ReplicaSet 至少有一个 Pod 运行在 node-a；停止 node-a 的 `sailer` 后，检查 Node 变为 `Unknown`，ReplicaSet replacement Pods 全部迁出 node-a，Service endpoints 不再包含失效节点旧 Pod，同时 NodePort 仍能访问剩余健康后端。
- 节点恢复验证：重新启动 node-a 的 `sailer` 后，检查 Node 恢复为 `Ready`；脚本不重新创建 ReplicaSet，而是在同一个 ReplicaSet 上删除一个非 node-a 的 Pod 触发补副本，观察 replacement Pod 可再次调度到 node-a，证明故障节点恢复后重新进入调度池。

只有在意外中断下需要手动清理资源：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/07_fault_tolerance.sh cleanup
```

## 08 GPU / Personal GPU Job

`scripts/acceptance/20_personal_gpu.sh` 是个人作业 GPU 验收脚本，对应 `docs/FINAL.md` 9.3。运行前需保证 01 多机部署已完成，脚本只需要在 node-a 上执行。

当前实现不是原生 GPU device plugin，也不会把交我算 Slurm 节点加入 Minik8s 集群；它实现的是一个接近 Kubernetes Job 的一次性任务抽象：每个 `Job` 创建独立 submitter Pod/Service，由 submitter 通过 SSH/SCP 把 CUDA 程序上传到交我算平台，使用 Slurm 编译运行并回收结果。

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/20_personal_gpu.sh
```

GPU 验收依赖交我算 SSH 凭据。仓库不提交私钥；验收前需要按照交付布局把 `secrets/gpu-ssh` 上传到远程机器的 `/opt/minik8s/secrets/gpu-ssh`：

```text
/opt/minik8s/secrets/gpu-ssh/
├── <private-key>，例如 id_ed25519_minik8s
├── <private-key>-cert.pub，例如 id_ed25519_minik8s-cert.pub
├── config
└── known_hosts
```

Job controller 会把该目录作为 hostPath 挂载到 submitter Pod 的 `/root/.ssh`。私钥文件名不要求固定为 `id_ed25519` 或 `id_rsa`，最终只以远程机器 `/opt/minik8s/secrets/gpu-ssh/config` 为准；其中 `IdentityFile` 和 `CertificateFile` 必须指向挂载后的 `/root/.ssh/<private-key>` 和 `/root/.ssh/<private-key>-cert.pub`。脚本不会 fallback 或猜测文件名；缺少 config 字段或对应文件会直接失败。脚本会读取该 config，并用同一目录在 node-a 上做非交互 SSH 预检，确认能够访问 `stu1718@sylogin.hpc.sjtu.edu.cn`，并找到 `sbatch`、`squeue` 和 `sacct`。

该脚本使用 `manifests/job/` 下的三个 YAML 和两个 CUDA 程序执行：

**08.1 CUDA 程序、Job YAML 和 Slurm 凭据**
- 展示 `vector_add.cu`、`matmul_tiled.cu`、`Makefile`、`Makefile.matmul` 路径。
- 展示 Job YAML 中的 `apiVersion: batch/v1`、`kind: Job`、`metadata.name`、`selector.matchLabels.accelerator: gpu`、`source.files`、`source.command`、`spec.slurm` 和 `spec.remote`。
- 检查 `ghcr.io/popc0rn7/gpu-submitter:v0.1.0` 已在本机 Docker 中可用。
- 检查 Harbor API 可访问，检查 `/opt/minik8s/secrets/gpu-ssh` 已上传，并验证 SSH/Slurm 命令可用。

**08.2 提交 CUDA vector add Job**
- 通过 `kubectl apply -f manifests/job/cuda-add.yaml` 创建 `cuda-add`。
- 等待控制面创建独立 submitter Pod/Service：`job-cuda-add-submitter`。
- 运行 `kubectl get jobs`、`kubectl describe job cuda-add`、`kubectl get pod job-cuda-add-submitter -o yaml` 和 `kubectl get svc job-cuda-add-submitter -o yaml`，展示 Job 状态、remote host、remote dir、Slurm Job ID、submitter Pod 和 submitter Service。

**08.3 观察 Slurm 状态和 vector add 结果**
- 等待 Job 从 `PodCreating/Preparing/Uploading/Submitted/Running/Collecting` 进入终态。
- 如果进入 `Succeeded`，运行 `kubectl logs job cuda-add`，要求输出包含 `N = 1048576`、`threadsPerBlock = 256`、`blocksPerGrid = 4096` 和 `Result: PASS`。
- 如果交我算队列导致任务未在等待窗口内结束，脚本输出当前 phase、Slurm Job ID、remote dir、startTime，以及可复制的 `ssh ... squeue/sacct ...` 查询命令，并以 `[LIMITED]` 标记该小节。

**08.4 Job 隔离性**
- 通过 `kubectl apply -f manifests/job/cuda-add-2.yaml` 再提交一个同类 GPU Job。
- 检查两个 Job 各自拥有独立 submitter Pod 和 Service：
  - `job-cuda-add-submitter`
  - `job-cuda-add-2-submitter`
- 检查两个 Job 的 `Remote Dir` 不同；在两个任务都提交成功时检查 `Slurm Job ID` 不同。

**08.5 复杂 CUDA tiled matrix multiplication**
- 通过 `kubectl apply -f manifests/job/cuda-matmul.yaml` 提交 `cuda-matmul-tiled`。
- 该程序展示二维 grid/block、shared memory tile 和 block 内同步。
- 如果进入 `Succeeded`，运行 `kubectl logs job cuda-matmul-tiled`，要求输出包含 `Matrix N = 1024`、`Tile size = 16`、`Block = 16 x 16`、`Grid = 64 x 64`、`Kernel: tiled shared-memory matrix multiplication` 和 `Result: PASS`。
- 如果交我算队列未及时完成，脚本同样输出 pending/running 状态、Slurm Job ID、remote dir、startTime 和查询命令。

只有在意外中断下需要手动清理资源：
```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/20_personal_gpu.sh cleanup
```

清理会删除 `cuda-add`、`cuda-add-2` 和 `cuda-matmul-tiled` 三个 Job；如果 Job 已经提交 Slurm，控制面会 best-effort 执行 `scancel <jobid>`。

## CICD

TODO

## Software Testing

TODO

## AI Usage

TODO

## Develop Process

TODO
