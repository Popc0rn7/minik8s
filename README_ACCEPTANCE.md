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

`01_node_multinode.sh bridge` 会在 Harbor ready 后自动执行 `kubectl apply -f manifests/cni/mooring.yaml`，并检查 `kube-mooring/mooring-cni-cfg` 与 `kube-mooring/mooring-cni-ds` endpoint。`env.sh` 默认 `MINIK8S_CNI_DISABLED=0`，因此后续 `sailer` 启动时会安装 `/opt/cni/bin/mooring` 并写入 `/etc/cni/net.d/10-mooring.conf`。开发调试需要禁用 CNI 时，可显式设置 `MINIK8S_CNI_DISABLED=1`。

清理当前 01 service 和 CNI 状态：

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh clean
```

只清理 mooring CNI 控制面兼容对象和本机网络残留：

```bash
source scripts/acceptance/env.sh
bash scripts/acceptance/01_node_multinode.sh cni-clean
```

Checklist:
- 启动过程，展现2, 4, 5, 6要求
- 最后的无参数验证，展现1, 3功能

Node设计：
```bash
kind: Node
apiVersion: v1
metadata:
    name: node-a
    namespace: ""
    labels:
        node: node-a
    annotations: {}
    uid: ""
    resourceVersion: "1434"
spec:
    role: Worker
    podCIDR: 10.244.0.0/24
status:
    phase: Ready
    lastHeartbeat: 2026-06-17T08:27:32.782584471Z
    addresses:
        - type: InternalIP
          address: 192.168.1.4
    conditions:
        - type: Ready
          status: "True"
          lastHeartbeatTime: 2026-06-17T08:27:32.782584471Z
          lastTransitionTime: 2026-06-17T08:27:32.782584471Z
          reason: Heartbeat
          message: Node is reporting heartbeat
```
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
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/02_pod_lifecycle.sh
```

该脚本会借助`manifests/pod/`下的 Pod YAML：

- 多容器 Pod 的创建、启动、参数查看和删除。
- Pod 配置中的 kind/name、镜像版本、命令、端口、CPU/内存 request/limit。
- 同一 Pod 内 sidecar 通过 `127.0.0.1` 访问 nginx 容器。
- 同一 Pod 内两个容器通过共享 volume 读写文件。
- 手动 `docker kill` 一个容器后，sailer reconcile 使容器重启，并在 `kubectl describe pod`
  中看到 `restartCount` 增加。
- 创建多个不指定 `nodeName` 的 Pod，展示 Harbor scheduler 基于 Ready 节点、selector/resource
  过滤和 round-robin 策略分配到不同节点。

脚本按 `02.1`、`02.2`、`02.3` 三个验收小节分别创建资源、输出证据、清理资源并输出
`[END] status=<PASS|FAIL>`；最后输出总结果，例如 `[END] status=3/3PASS`。

## 03 Service

`scripts/acceptance/03_service.sh` 是 Service 抽象、endpoint controller 和 kube-proxy 数据面验收脚本。运行前需保证已经执行好 01 的多机部署，并且 CNI/kube-proxy 处于开启状态；脚本只需要在 node-a 上执行。03 会检查 `net.bridge.bridge-nf-call-iptables=1`，否则 Pod 内访问 ClusterIP 的流量不会进入宿主机 iptables Service 规则。

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/03_service.sh
```

该脚本使用固定 manifest，而不是运行时临时生成 YAML：

- `manifests/service/pod_03_nginx_node_a.yaml`
- `manifests/service/pod_03_nginx_node_b.yaml`
- `manifests/service/pod_03_client.yaml`
- `manifests/service/service_03_clusterip.yaml`
- `manifests/service/service_03_nodeport.yaml`

四个小节分别覆盖：

- `03.1`：创建 nginx Pod 和 ClusterIP Service，检查 selector 生成两个 endpoint，删除 Service 后确认对象消失。
- `03.2`：从 node-a 主机和 client Pod 内访问 ClusterIP，证明 Service VIP 到后端 Pod 的转发可用。
- `03.3`：通过固定 NodePort `30080` 访问 Service，先验证 node-a 本机 NodePort，再尝试从 node-a 访问 node-b NodePort。若跨节点 NodePort 受云网络或防火墙限制，会输出 `[LIMITED]`。
- `03.4`：删除一个后端 Pod 后等待 Service endpoint 从 2 个变为 1 个，再恢复后端并确认 endpoint 回到 2 个。

脚本按 `03.1`、`03.2`、`03.3`、`03.4` 分别清理资源并输出 `[END] status=<PASS|FAIL|LIMITED>`；最后输出总结果，例如 `[END] status=4/4PASS`。单独清理 03 资源可运行：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/03_service.sh cleanup
```

给后续 agent 的实施步骤：

1. 本地编辑 `manifest/service/` 和 `scripts/acceptance/03_service.sh`，不要在脚本里临时创建 manifest。
2. 先运行 `bash -n scripts/acceptance/03_service.sh`，再按影响范围运行 Go 单测，例如 `go test ./internal/bridge/captain ./internal/bridge/harbor ./internal/kubeproxy ./internal/service ./internal/cli -count=1`。
3. 将脚本同步到 node-a 的 `/opt/minik8s/scripts/acceptance/`，将 manifest 同步到 node-a 的 `/opt/minik8s/manifests/service/`。
4. 在 node-a 执行 `bash scripts/acceptance/03_service.sh cleanup` 后再执行完整脚本。
5. 若失败，优先检查 `kubectl describe svc` 中的 ClusterIP、NodePort、endpoints，再看 `systemctl status minik8s-sailer` 和本机 iptables/kube-proxy 状态。

## 04 ReplicaSet

`scripts/acceptance/04_replicaset.sh` 是 ReplicaSet 抽象、Service 绑定和副本恢复能力验收脚本。运行前需保证已经完成 01 多机部署，并且 03 所需的 CNI/kube-proxy/`br_netfilter` 数据面已经可用；脚本只需要在 node-a 上执行。

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/04_replicaset.sh
```

该脚本使用固定 manifest：

- `manifests/replicaset/replicaset_04_acceptance.yaml`
- `manifests/replicaset/service_04_acceptance.yaml`

三个小节分别覆盖：

- `04.1`：创建 `rs-04-web` ReplicaSet，期望副本数为 3，检查 `kubectl get/describe rs`、生成 Pod、至少两个节点上的调度分布，并删除 ReplicaSet 后确认 owned Pods 被清理。
- `04.2`：创建绑定同一 label/selector 的 NodePort Service，检查 3 个 endpoints，并从 node-a 访问 node-a/node-b 的 `30081` NodePort。后端 Pod 使用 busybox httpd 返回自己的 hostname；脚本还会检查 kube-proxy 为 NodePort 写入了面向 3 个 endpoint 的 `statistic --mode random` DNAT 规则，用于证明本组设置的随机负载均衡策略。
- `04.3`：删除一个 ReplicaSet-owned Pod，等待 controller 补回 3 个 Running Pods，输出删除前后的 Pod 名称，证明恢复能力。

脚本按 `04.1`、`04.2`、`04.3` 分别清理资源并输出 `[END] status=<PASS|FAIL>`；最后输出总结果，例如 `[END] status=3/3PASS`。单独清理 04 资源：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/04_replicaset.sh cleanup
```

给后续 agent 的实施步骤：

1. 本地编辑 `manifest/replicaset/` 和 `scripts/acceptance/04_replicaset.sh`，不要在脚本中临时生成 manifest。
2. 先运行 `bash -n scripts/acceptance/04_replicaset.sh`，再运行 `go test ./internal/bridge/captain ./internal/bridge/harbor ./internal/replicaset ./pkg/yaml ./internal/cli -count=1`。
3. 同步脚本到 node-a 的 `/opt/minik8s/scripts/acceptance/`，同步 manifest 到 node-a 的 `/opt/minik8s/manifests/replicaset/`。
4. 在 node-a 执行 `bash scripts/acceptance/04_replicaset.sh cleanup` 后再执行完整脚本。
5. 若失败，优先检查 `kubectl describe rs rs-04-web`、`kubectl get pods`、`kubectl describe svc rs-04-web`，再看 bridge 的 ReplicaSet controller 日志和各节点 sailer 日志。

## 05 HPA

`scripts/acceptance/05_hpa.sh` 是 HorizontalPodAutoscaler、metrics API 和 ReplicaSet 动态伸缩验收脚本。运行前需保证 01 多机部署已完成，三台 sailer 正常上报 metrics，CNI/kube-proxy 可用；脚本只需要在 node-a 上执行。HPA 负载镜像 `polinux/stress:1.0.4` 需要预先拉取并加载到所有可能调度 Pod 的节点上。

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/05_hpa.sh
```

该脚本使用固定 manifest：

- `manifests/hpa/replicaset_05_acceptance.yaml`
- `manifests/hpa/service_05_acceptance.yaml`
- `manifests/hpa/hpa_05_scale_up.yaml`
- `manifests/hpa/hpa_05_scale_down.yaml`

三个小节分别覆盖：

- `05.1`：创建 HPA 目标 ReplicaSet 和 NodePort Service，等待真实 sailer metrics 出现在 `pods.metrics.k8s.io`，再创建 HPA 并输出 `kubectl get/describe hpa`。
- `05.2`：ReplicaSet Pod 中的 `polinux/stress:1.0.4` sidecar 执行真实 `stress --cpu 1 --timeout 90s`，HPA 基于 sailer 上报的 CPU metrics 将副本从 1 逐步扩到 `maxReplicas=3`，输出 Pod 和 HPA 状态。
- `05.3`：保持同一 HPA 目标值，等待 Pod 内 `stress` 命令结束、真实 CPU metrics 回落并经过 cooldown 后缩回 `minReplicas=1`，再通过固定 NodePort `30082` 访问缩容后的 Service。

脚本按 `05.1`、`05.2`、`05.3` 分别清理资源并输出 `[END] status=<PASS|FAIL>`；最后输出总结果，例如 `[END] status=3/3PASS`。单独清理 05 资源：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/05_hpa.sh cleanup
```

## 06 DNS

`scripts/acceptance/06_dns_forwarding.sh` 是 DNS 对象、DNS sync 文件和 host/path 转发验收脚本。运行前需保证 bridge 启动时包含 `dns` addon，`/opt/minik8s/dns` 由 bridge 同步，node-a 本机 80 端口由 DNS ingress 使用；脚本只需要在 node-a 上执行。

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/06_dns_forwarding.sh
```

该脚本使用固定 manifest：

- `manifests/dns/replicaset_06_alpha.yaml`
- `manifests/dns/replicaset_06_beta.yaml`
- `manifests/dns/service_06_alpha.yaml`
- `manifests/dns/service_06_beta.yaml`
- `manifests/dns/dns_06_routes.yaml`
- `manifests/dns/pod_06_client.yaml`

三个小节分别覆盖：

- `06.1`：创建两个后端 Service 和 `dns-06-routes`，检查 `kubectl get/describe dns`，并检查 `/opt/minik8s/dns/hosts`、`/opt/minik8s/dns/routes.json` 中出现 `acceptance06.minik8s.local`、`/alpha` 和 `/beta`。
- `06.2`：从 node-a 宿主机用 `Host: acceptance06.minik8s.local` 访问 `http://127.0.0.1/alpha` 和 `/beta`，验证分别转发到返回 `route=alpha`、`route=beta` 的不同 Service。
- `06.3`：在 DNS addon 已启用后创建 client Pod，尝试从 Pod 内通过 `http://acceptance06.minik8s.local/alpha` 和 `/beta` 访问；随后删除 DNS 对象，确认 sync 文件删除该 host，host ingress 不再服务该域名。若当前部署只将 DNS addon 绑定在宿主机自定义端口而容器 nameserver 无法指定端口，该子项会输出 `[LIMITED]`。

脚本按 `06.1`、`06.2`、`06.3` 分别清理资源并输出 `[END] status=<PASS|FAIL|LIMITED>`；最后输出总结果，例如 `[END] status=3/3PASS`。单独清理 06 资源：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/06_dns_forwarding.sh cleanup
```

## 07 Fault Tolerance

`scripts/acceptance/07_fault_tolerance.sh` 是 bridge 重启、数据面节点故障、ReplicaSet 恢复、bare Pod NodeLost 和节点恢复验收脚本。运行前需保证 node-a 同时运行 bridge 和 sailer；脚本只在 node-a 上执行。脚本会重启 `minik8s-bridge.service` 验证控制面恢复，也会通过 `systemctl stop minik8s-sailer.service` 模拟本机 worker 故障。脚本带 `trap`，失败时也会尝试重新启动 bridge 和 sailer。

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/07_fault_tolerance.sh
```

该脚本使用固定 manifest：

- `manifests/fault/replicaset_07_acceptance.yaml`
- `manifests/fault/service_07_acceptance.yaml`
- `manifests/fault/pod_07_bare.yaml`
- `manifests/fault/service_07_bare.yaml`

四个小节分别覆盖：

- `07.1`：创建 3 副本 ReplicaSet 和固定 NodePort `30085` Service，确认 NodePort 可访问；重启 bridge 后再次确认 Harbor API、Pod/Service/endpoints 和 NodePort 访问恢复。
- `07.2`：确认至少一个 ReplicaSet 副本在 node-a；停止 node-a sailer 后等待 node-a 变为 `Unknown`，确认 ReplicaSet-owned Pod 被驱逐并在 Ready 节点补齐，Service endpoint 不再指向故障节点。
- `07.3`：创建显式调度到 node-a 的 bare Pod 和 ClusterIP Service；停止 node-a sailer 后确认 bare Pod 被标记为 `Unknown/NodeLost`，且对应 Service endpoint 被移除，不会像 ReplicaSet Pod 一样自动重建。
- `07.4`：再次停止并启动本机 sailer，等待 node-a 从 `Unknown` 回到 `Ready`，输出恢复后的 node 列表。

脚本按 `07.1`、`07.2`、`07.3`、`07.4` 分别清理资源并输出 `[END] status=<PASS|FAIL|LIMITED>`；最后输出总结果，例如 `[END] status=4/4PASS`。如果 ReplicaSet 调度 3 次都没有副本落到 node-a，`07.2` 会输出 `[LIMITED]`，因为无法证明本机节点故障路径。单独清理 07 资源并确保 bridge/sailer 启动：

```bash
cd /opt/minik8s
source scripts/acceptance/env.sh
bash scripts/acceptance/07_fault_tolerance.sh cleanup
```

## CICD

TODO

## Software Testing

TODO

## AI Usage

TODO

## Develop Process

TODO
