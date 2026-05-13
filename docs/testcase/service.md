# Service CLI 测试用例与实现映射

本文档覆盖 Handout 中 “实现 Service 抽象” 的要求。所有 case 使用的 YAML 均放在 `manifest/testdata/`。

## 0. 前置准备

Service 依赖 CNI Pod IP 和 Linux iptables NAT 规则，通常需要 root 权限或等价网络能力。在仓库根目录执行：

```bash
go build -o minik8s ./cmd/minik8s
export MINIK8S_PLAIN=1
export NO_COLOR=1
unset MINIK8S_CNI_DISABLED
./minik8s cni init
go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
./minik8s doctor network
```

如需隔离本次 case 的本地状态：

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
rm -rf .minik8s/testcase-state .minik8s/state/cni-ipam.json
```

## 1. 需求追踪矩阵

| Handout 要求 | 验证 case | Manifest | 主要命令 | 当前状态 |
| --- | --- | --- | --- | --- |
| `kind: Service`、type、name、namespace、labels | SVC-01 | `service_clusterip_nginx.yaml` | `apply`、`get services` | 已实现，可验证 |
| selector 根据 labels 筛选 Pod | SVC-01、SVC-04 | `pod_nginx.yaml`、Service manifest | `get services` endpoints | 已实现，可验证 |
| ports: port、targetPort、nodePort | SVC-01、SVC-03 | 两个 Service manifest | `get services` | 已实现，可验证 |
| ClusterIP 稳定虚拟 IP | SVC-01、SVC-02 | `service_clusterip_nginx.yaml` | client Pod 内访问 ClusterIP | 已实现，需 root/网络环境 |
| NodePort 对外暴露端口 | SVC-03 | `service_nodeport_nginx.yaml` | `curl 127.0.0.1:30080` | 已实现，需 root/网络环境 |
| endpoints 展示 Pod IP+port | SVC-01、SVC-04、SVC-05 | 多个 manifest | `get services` | 已实现，可验证 |
| Pod 动态增删更新 endpoints | SVC-04、SVC-05 | 多个 manifest | `apply/delete pod`、`get services` | 已实现，可验证 |
| 删除 Service 并清理附带状态 | SVC-06 | Service manifest | `delete service`、`iptables-save` | 已实现，需 root/网络环境 |

## 2. Case SVC-01：创建并查看 ClusterIP Service

目标：验证 Service YAML 基础字段、selector、port/targetPort、ClusterIP、namespace、labels 和 endpoints 可视化。

Manifest：

- `manifest/testdata/pod_nginx.yaml`
- `manifest/testdata/service_clusterip_nginx.yaml`

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s get services
```

期望：

- Service apply 输出包含 `service/nginx-service created (ClusterIP)`。
- `get services` 输出包含 `nginx-service`、`ClusterIP`、`10.96.0.1`、`80->80/TCP`。
- `ENDPOINTS` 列包含 `nginx-pod` 对应的 Pod IP 和 `:80`。
- 输出包含 `default`、`app=nginx`、`tier=frontend`。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-pod || true
```

## 3. Case SVC-02：Pod 内通过 ClusterIP 访问 Service

目标：验证 client Pod 能通过 Service ClusterIP 访问被 selector 选中的 nginx Pod。

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
docker exec "$CLIENT_CID" wget -qO- http://10.96.0.1:80 >/tmp/minik8s-service-clusterip.html
head -n 1 /tmp/minik8s-service-clusterip.html
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

## 4. Case SVC-03：通过 NodePort 从宿主机访问 Service

目标：验证 NodePort Service 在宿主机端口暴露集群内服务。

Manifest：

- `manifest/testdata/pod_nginx.yaml`
- `manifest/testdata/service_nodeport_nginx.yaml`

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_nodeport_nginx.yaml
./minik8s get services
curl -fsS http://127.0.0.1:30080 >/tmp/minik8s-service-nodeport.html
head -n 1 /tmp/minik8s-service-nodeport.html
```

期望：

- `get services` 输出包含 `nginx-nodeport`、`NodePort`、`80->80/TCP:30080`。
- 宿主机访问 `127.0.0.1:30080` 成功返回 nginx 页面。

清理：

```bash
./minik8s delete service nginx-nodeport || true
./minik8s delete pod nginx-pod || true
```

## 5. Case SVC-04：动态增加 endpoints 并观察负载均衡

目标：验证新启动的 matching Pod 会被纳入 Service endpoints，iptables proxy 对多个 endpoints 配置随机均摊规则。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s apply -f manifest/testdata/pod_nginx_service_peer.yaml
./minik8s get services
iptables-save -t nat | grep MK8S-SVC
```

期望：

- `get services` 的 `ENDPOINTS` 列包含两个 `10.244.0.0/24` 内 Pod IP，端口均为 `:80`。
- `iptables-save` 中可看到 Service chain 内多条 DNAT 规则，其中前置规则包含 `statistic --mode random`。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-pod-2 || true
./minik8s delete pod nginx-pod || true
```

## 6. Case SVC-05：删除 Pod 后动态移除 endpoint

目标：验证被 selector 选中的 Pod 删除后，Service endpoints 会在下一次 sync 中移除。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
./minik8s get services
./minik8s delete pod nginx-pod
./minik8s get services
```

期望：

- 删除 Pod 前 `ENDPOINTS` 包含 nginx Pod IP。
- 删除 Pod 后再次 `get services`，`ENDPOINTS` 显示 `-`。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-pod || true
```

## 7. Case SVC-06：删除 Service 并清理 iptables 状态

目标：验证 `delete service` 会删除持久化状态并清理该 Service 的 iptables chain。

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

- 删除前能看到 `MK8S-SVC-*` chain 和跳转规则。
- `delete service` 输出包含 `service/nginx-service deleted`。
- 删除后 `get services` 不再显示 `nginx-service`。
- 删除后不再存在该 Service 对应的 iptables chain。

清理：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-pod || true
```

## 8. 实现设计映射

类型定义：`internal/service/types.go` 定义 Service、ServiceSpec、ServicePort、Endpoint 和 ServiceStatus。

YAML 解析：`pkg/yaml/service.go` 和 `pkg/yaml/defaults.go` 读取并校验 Service manifest，默认 `namespace=default`、`type=ClusterIP`、`protocol=TCP`、`clusterIP=10.96.0.1`。

状态持久化：`internal/store/service_store.go` 提供文件和内存两种 ServiceStore。默认文件为 `.minik8s/state/services.json`，也可通过 `MINIK8S_STATE_DIR` 隔离。

控制器：`internal/controller/service_controller.go` 读取 Service 和 Running Pod，按 selector 匹配同 namespace Pod，并将 `PodIP:targetPort` 写入 endpoints。

流量转发：`internal/controller/iptables_proxy.go` 使用 iptables `nat` 表维护 `MK8S-SVC-*` chain。ClusterIP 规则挂到 `PREROUTING` 和 `OUTPUT`；NodePort 规则同样挂到 `PREROUTING` 和 `OUTPUT`。多个 endpoint 通过 `statistic --mode random` 分摊。

CLI：`internal/cli/cli.go` 的 `apply -f` 会先读取 `kind`，分发到 Pod 或 Service；`get services` 会触发 Service sync 并展示 Service 表；`delete service` 会清理 proxy 规则和持久化状态。
