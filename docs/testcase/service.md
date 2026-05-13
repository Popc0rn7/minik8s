# kubeproxy 能力测试用例与实现映射

本文档覆盖 Minik8s Service 数据面能力，重点验证 `internal/kubeproxy` 抽象和默认 `IPTablesProxy` backend 是否能把 Service 期望状态同步为可用的 ClusterIP、NodePort、endpoint 负载均衡和清理规则。所有 YAML case 使用 `manifest/testdata/` 中的 manifest。

## 0. 前置准备

kubeproxy 默认 backend 使用 Linux iptables NAT 规则，完整数据面 case 需要 root 权限或等价网络能力。在仓库根目录执行：

```bash
go build -o minik8s ./cmd/minik8s
export MINIK8S_PLAIN=1
export NO_COLOR=1
unset MINIK8S_CNI_DISABLED
./minik8s cni init
go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
./minik8s doctor network
./minik8s kubebridge --listen :8080
sudo ./minik8s kubesailer --node-name node-a --kubeharbor http://127.0.0.1:8080
```

如需隔离本次 case 的状态：

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
rm -rf .minik8s/testcase-state .minik8s/state/cni-ipam.json
```

每个网络 case 运行前建议先确认没有旧规则残留：

```bash
iptables-save -t nat | grep MK8S-SVC || true
```

## 1. 能力追踪矩阵

| kubeproxy 能力 | 验证 case | 层级 | 主要命令 | 通过标准 |
| --- | --- | --- | --- | --- |
| ServiceKubecaptain 生成期望状态并调用 kubeproxy | KP-01 | 控制面 | `go test`、`get services` | endpoints 与 matching Running Pod 一致 |
| ClusterIP 入口规则 | KP-02 | iptables 规则面 | `iptables-save -t nat` | PREROUTING/OUTPUT 指向 `MK8S-SVC-*` |
| ClusterIP 数据面转发 | KP-03 | 数据面 | client Pod 内 `wget 10.96.0.1:80` | 返回 nginx 页面 |
| NodePort 入口规则和宿主机访问 | KP-04 | 规则面 + 数据面 | `curl 127.0.0.1:30080` | 返回 nginx 页面 |
| 多 endpoint 负载均衡规则 | KP-05 | iptables 规则面 | `iptables-save -t nat` | DNAT 规则包含 `statistic --mode random` |
| endpoint 动态 reconcile | KP-06 | 控制面 + 规则面 | 新增/删除 Pod 后 `get services` | endpoint 集合和规则随 Pod 变化 |
| 删除 Service 清理规则 | KP-07 | 规则清理 | `delete service`、`iptables-save` | 对应 `MK8S-SVC-*` chain 消失 |
| kubeproxy backend 可单测、可替换 | KP-08 | 单元测试 | `go test ./internal/kubeproxy` | fake runner 记录预期命令 |

## 2. Case KP-01：控制器生成 Service 期望状态

目标：验证 kubesailer 将分配给本节点的 Pod 运行起来后，ServiceKubecaptain 能从 Service selector 和 Running Pod 生成 endpoints，并把更新后的 Service 交给 kubeproxy 抽象。

自动化测试：

```bash
go test ./internal/kubebridge/kubecaptain -run 'TestServiceKubecaptain(BuildsEndpointsAndAppliesProxy|UpdatesEndpointsWhenPodChanges|DeleteCleansProxyAndStore)' -count=1
```

CLI 验证：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s get services
```

期望：

- `get services` 输出包含 `nginx-service`、`ClusterIP`、`10.96.0.1`、`80->80/TCP`。
- `ENDPOINTS` 列包含 `nginx-pod` 对应的 Pod IP 和 `:80`。
- 不匹配 selector 的 Pod 不会进入 endpoints。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-pod || true
```

## 3. Case KP-02：ClusterIP 规则面

目标：验证 `IPTablesProxy.SyncService` 为 ClusterIP Service 创建稳定 chain，并从 `PREROUTING` 和 `OUTPUT` 挂入口规则。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
iptables-save -t nat | grep -E 'MK8S-SVC|10\.96\.0\.1|10\.244\.0'
```

期望：

- 存在名为 `MK8S-SVC-*` 的 Service chain。
- 存在 `-A PREROUTING -p tcp -d 10.96.0.1/32 --dport 80 -j MK8S-SVC-*` 或等价规则。
- 存在 `-A OUTPUT -p tcp -d 10.96.0.1/32 --dport 80 -j MK8S-SVC-*` 或等价规则。
- Service chain 内存在 `DNAT --to-destination <nginx-pod-ip>:80`。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-pod || true
```

## 4. Case KP-03：ClusterIP 数据面转发

目标：验证 Pod 内访问 Service ClusterIP 时，流量经 kubeproxy 规则转发到后端 nginx Pod。

Manifest：

- `manifest/testdata/pod_nginx.yaml`
- `manifest/testdata/pod_busybox_client.yaml`
- `manifest/testdata/service_clusterip_nginx.yaml`

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/pod_busybox_client.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s get services
CLIENT_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "$CLIENT_CID" wget -qO- http://10.96.0.1:80 >/tmp/minik8s-kubeproxy-clusterip.html
head -n 1 /tmp/minik8s-kubeproxy-clusterip.html
```

期望：

- `get services` 显示 `nginx-service` 的 endpoint 为 nginx Pod IP。
- client Pod 内访问 `http://10.96.0.1:80` 成功。
- 响应内容为 nginx 默认 HTML。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod busybox-client || true
./minik8s delete pod nginx-pod || true
```

## 5. Case KP-04：NodePort 规则面和宿主机访问

目标：验证 NodePort Service 会在宿主机端口暴露 Service，并复用同一个 Service chain 转发到后端 Pod。

Manifest：

- `manifest/testdata/pod_nginx.yaml`
- `manifest/testdata/service_nodeport_nginx.yaml`

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_nodeport_nginx.yaml
./minik8s get services
iptables-save -t nat | grep -E 'MK8S-SVC|30080|10\.96\.0\.1|10\.244\.0'
curl -fsS http://127.0.0.1:30080 >/tmp/minik8s-kubeproxy-nodeport.html
head -n 1 /tmp/minik8s-kubeproxy-nodeport.html
```

期望：

- `get services` 输出包含 `nginx-nodeport`、`NodePort`、`80->80/TCP:30080`。
- iptables 中存在 `--dport 30080 -j MK8S-SVC-*` 的 `PREROUTING` 和 `OUTPUT` 入口规则。
- Service chain 内存在针对 nodePort 流量的 `DNAT --to-destination <nginx-pod-ip>:80`。
- 宿主机访问 `127.0.0.1:30080` 成功返回 nginx 页面。

清理：

```bash
./minik8s delete service nginx-nodeport || true
./minik8s delete pod nginx-pod || true
```

## 6. Case KP-05：多 endpoint 负载均衡规则

目标：验证当 Service 选中多个 Running Pod 时，kubeproxy 为同一 Service chain 写入多条 DNAT 规则，并用 iptables statistic 模块做随机分摊。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/pod_nginx_service_peer.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s get services
iptables-save -t nat | grep MK8S-SVC
```

期望：

- `get services` 的 `ENDPOINTS` 列包含两个 `10.244.0.0/24` 内 Pod IP，端口均为 `:80`。
- Service chain 内至少有两条 `DNAT --to-destination <pod-ip>:80` 规则。
- 除最后一个 endpoint 外，前置 endpoint 规则包含 `-m statistic --mode random --probability ...`。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-pod-2 || true
./minik8s delete pod nginx-pod || true
```

## 7. Case KP-06：endpoint 动态 reconcile

目标：验证 kubeproxy 以 Service 当前 endpoints 为期望状态，每次 sync 都先清理旧规则再重建新规则。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s get services
iptables-save -t nat | grep MK8S-SVC

./minik8s apply -f manifest/testdata/pod_nginx_service_peer.yaml
./minik8s get services
iptables-save -t nat | grep MK8S-SVC

./minik8s delete pod nginx-pod
./minik8s get services
iptables-save -t nat | grep MK8S-SVC
```

期望：

- 初始状态只有 `nginx-pod` 一个 endpoint。
- 新增 `nginx-pod-2` 后，`get services` 和 DNAT 规则都包含两个 endpoints。
- 删除 `nginx-pod` 后，`get services` 和 DNAT 规则只保留 `nginx-pod-2` 的 Pod IP。
- 规则中不再出现已删除 Pod 的旧 IP。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-pod-2 || true
./minik8s delete pod nginx-pod || true
```

## 8. Case KP-07：删除 Service 清理 kubeproxy 状态

目标：验证删除 Service 时，kubeproxy 会移除入口规则、flush chain 并删除该 Service chain。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
iptables-save -t nat | grep MK8S-SVC
./minik8s delete service nginx-service
./minik8s get services
iptables-save -t nat | grep MK8S-SVC || true
```

期望：

- 删除前能看到 `MK8S-SVC-*` chain、ClusterIP 入口规则和 DNAT 规则。
- `delete service` 输出包含 `service/nginx-service deleted`。
- 删除后 `get services` 不再显示 `nginx-service`。
- 删除后不再存在该 Service 对应的 `MK8S-SVC-*` chain。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-pod || true
```

## 9. Case KP-08：kubeproxy 抽象和 iptables backend 单测

目标：在无需 root 权限、无需真实 iptables 的环境中验证 kubeproxy backend 的规则生成能力，避免只能靠手工网络 case 才能发现回归。

流程：

```bash
go test ./internal/kubeproxy -count=1
go test ./internal/service ./internal/kubebridge/kubecaptain ./internal/cli -count=1
```

期望：

- `TestIPTablesProxySyncServiceProgramsClusterIPAndNodePort` 验证 ClusterIP、NodePort 和多 endpoint DNAT 命令。
- `TestIPTablesProxySyncAllReconcilesEveryService` 验证 kubeproxy 抽象支持全量同步入口。
- `TestIPTablesProxyDeleteServiceIgnoresMissingRules` 验证删除时缺失旧规则不阻塞清理。
- ServiceKubecaptain/CLI 测试仍通过，说明控制面只依赖 `kubeproxy.Proxy`，不依赖具体 iptables 实现。

## 10. 实现设计映射

类型定义：`internal/service/types.go` 定义 Service、ServiceSpec、ServicePort、Endpoint 和 ServiceStatus。

ClusterIP 分配：`internal/service/clusterip.go` 定义默认 ClusterIP 和分配逻辑；CLI 在创建或更新 Service 时保留既有 ClusterIP，并为新 Service 分配可用地址。

YAML 解析：`pkg/yaml/service.go` 和 `pkg/yaml/defaults.go` 读取并校验 Service manifest，默认 `namespace=default`、`type=ClusterIP`、`protocol=TCP`。

状态持久化：`internal/kubebridge/etcd/service_store.go` 提供文件和内存两种 ServiceStore。默认文件为 `.minik8s/state/services.json`，也可通过 `MINIK8S_STATE_DIR` 隔离。

期望状态生成：`internal/kubebridge/kubecaptain/service_kubecaptain.go` 读取 Service 和 Running Pod，按 selector 匹配同 namespace Pod，并将 `PodIP:targetPort` 写入 endpoints。

kubeproxy 抽象：`internal/kubeproxy/proxy.go` 定义 `SyncService`、`SyncAll`、`DeleteService`。ServiceKubecaptain 和 CLI 面向该接口，backend 可替换。

iptables backend：`internal/kubeproxy/iptables.go` 的 `IPTablesProxy` 使用 iptables `nat` 表维护 `MK8S-SVC-*` chain。ClusterIP 和 NodePort 入口规则挂到 `PREROUTING` 和 `OUTPUT`；endpoint 规则用 DNAT 转发到 Pod IP；多个 endpoint 通过 `statistic --mode random` 分摊。

CLI 接入：`internal/cli/cli.go` 的 `apply -f` 会读取 `kind` 并分发到 Pod 或 Service；`get services` 触发 Service sync；`delete service` 清理 kubeproxy 规则和持久化状态。
