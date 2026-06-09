# Service / kube-proxy 测试用例

本文档覆盖 v0.1.0 的 Service endpoints、ClusterIP、NodePort、多 endpoint 负载均衡和 iptables 清理。双机公共启动流程见 `docs/testcase/two-node.md`。

注意：v0.1.0 的 iptables proxy 由 `bridge` 进程所在节点同步，因此必测数据面入口是 node-a。node-b 访问 NodePort 可以作为观察项；若 node-b 没有运行 proxy 规则，node-b 的 NodePort 不作为失败。

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| SVC-01 | selector 生成 endpoints | node-a | 是 |
| SVC-02 | ClusterIP iptables 规则与数据面 | node-a | 是 |
| SVC-03 | NodePort iptables 规则与宿主机访问 | node-a，node-b 辅助 | 是 |
| SVC-04 | 双机多 endpoint 与负载均衡规则 | node-a + node-b | 是 |
| SVC-05 | endpoint 动态更新 | node-a + node-b | 是 |
| SVC-06 | 删除 Service 清理 iptables | node-a | 是 |
| SVC-07 | kubeproxy 单元测试 | 任意开发机 | 是 |

## SVC-01：Service endpoints

目标：验证 ServiceController 根据 selector 选中 Running Pod，并写入 endpoints。

机器：node-a 执行 CLI。

流程：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
sleep 8
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 6
./kubectl get services
./kubectl describe service nginx-service
```

期望：

- `nginx-service` 类型为 `ClusterIP`。
- ClusterIP 为 `10.96.0.1`。
- endpoints 包含 `nginx-node-a:<targetPort 80>` 对应的 PodIP。

失败排查：

- endpoints 为空：确认 Pod 已 Running 且 label `app=nginx` 存在。
- Service 不存在：确认 `MINIK8S_HARBOR=${HARBOR}`。

## SVC-02：ClusterIP 规则与数据面

目标：验证 node-a 上随 sailer 运行的 kubeproxy 写入 iptables NAT 规则，并能从 node-a Pod 内访问 ClusterIP。

机器：node-a。

流程：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_busybox_client.yaml
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 8
iptables-save -t nat | grep -E 'MK8S-SVC|10\.96\.0\.1'
CLIENT_A_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "${CLIENT_A_CID}" wget -qO- http://10.96.0.1:80 >/tmp/minik8s-service-clusterip.html
head -n 1 /tmp/minik8s-service-clusterip.html
```

期望：

- iptables 中存在 `MK8S-SVC-*` chain。
- `PREROUTING` 和 `OUTPUT` 有指向 `10.96.0.1:80` 的入口规则。
- Service chain 内存在 `DNAT --to-destination <nginx-node-a-pod-ip>:80`。
- Pod 内访问 `http://10.96.0.1:80` 返回 nginx HTML。

失败排查：

- 没有 `MK8S-SVC`：确认 node-a 的 sailer 未使用 `--proxy-disabled`，并且运行用户有 root/iptables 权限。
- 有规则但访问失败：检查 CNI PodIP、`ip route` 和 Docker container 网络命名空间。

## SVC-03：NodePort 规则与宿主机访问

目标：验证 NodePort Service 在运行 sailer/kubeproxy 的节点上暴露 `30080`。

机器：node-a 必测；node-b 辅助观察。

流程：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/service/service_nodeport_nginx.yaml
sleep 8
./kubectl get services
iptables-save -t nat | grep -E 'MK8S-SVC|30080|10\.96\.0\.1'
curl -fsS "http://${NODE_A_IP}:30080" >/tmp/minik8s-service-nodeport-a.html
head -n 1 /tmp/minik8s-service-nodeport-a.html
```

在 node-b 可选观察：

```bash
curl -fsS "http://${NODE_B_IP}:30080" >/tmp/minik8s-service-nodeport-b.html || true
```

期望：

- `get services` 包含 `nginx-nodeport`、`NodePort`、`80->80/TCP:30080`。
- node-a iptables 中存在 `--dport 30080 -j MK8S-SVC-*`。
- node-a 访问 `${NODE_A_IP}:30080` 返回 nginx HTML。
- node-b 只有在本机也运行了等价 proxy 规则时才要求成功。

失败排查：

- node-a curl 失败：检查宿主机防火墙是否允许 `30080`，以及 iptables 规则是否存在。
- node-b curl 失败：这是 v0.1.0 允许的观察结果，不影响必测结论。

## SVC-04：双机多 endpoint 与负载均衡规则

目标：验证同一个 Service 可以选中分布在 node-a/node-b 的后端，并生成多条 DNAT 规则。

机器：node-a 执行 CLI 和 iptables 检查；node-b 提供后端 Pod。

流程：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 10
./kubectl get services
./kubectl describe service nginx-service
iptables-save -t nat | grep MK8S-SVC
```

期望：

- `nginx-service` endpoints 包含两个 PodIP：一个 `10.244.0.0/24`，一个 `10.244.1.0/24`。
- Service chain 内至少两条 DNAT。
- 除最后一个 endpoint 外，前置 DNAT 规则包含 `-m statistic --mode random --probability ...`。

失败排查：

- 只有一个 endpoint：检查另一个节点的 sailer 是否运行，Pod 是否 Running。
- 有 node-b endpoint 但访问失败：检查 node-a 到 `10.244.1.0/24` 的 route。

## SVC-05：endpoint 动态更新

目标：验证新增/删除匹配 Pod 后，Service endpoints 和 iptables 规则能被周期性刷新。

机器：node-a。

流程：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 8
./kubectl describe service nginx-service

./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./kubectl describe service nginx-service
iptables-save -t nat | grep MK8S-SVC

./kubectl delete pod nginx-node-a
sleep 8
./kubectl describe service nginx-service
iptables-save -t nat | grep MK8S-SVC
```

期望：

- 初始只有 node-a endpoint。
- 新增 node-b 后 endpoints 变为两个。
- 删除 node-a 后 endpoints 只剩 node-b，iptables 不再包含 node-a 的 PodIP。

失败排查：

- endpoints 延迟不变：等待 service sync 默认周期，通常约 5s。
- 删除后旧 DNAT 仍在：检查 bridge 日志是否有 `service-periodic-sync` 错误。

## SVC-06：删除 Service 清理规则

目标：验证删除 Service 后入口规则、Service chain 和 DNAT 规则被清理。

机器：node-a。

流程：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 8
iptables-save -t nat | grep MK8S-SVC
./kubectl delete service nginx-service
sleep 2
./kubectl get services
iptables-save -t nat | grep MK8S-SVC || true
```

期望：

- 删除前能看到 `MK8S-SVC-*`。
- 删除后 `get services` 不再显示 `nginx-service`。
- 删除后不再有该 Service 对应的 `MK8S-SVC-*` chain。

失败排查：

- chain 残留：确认删除的是对应 Service，必要时重跑 `delete service`。

清理：

```bash
./kubectl delete service nginx-service || true
./kubectl delete service nginx-nodeport || true
./kubectl delete pod nginx-node-a || true
./kubectl delete pod nginx-node-b || true
./kubectl delete pod busybox-client || true
```

## SVC-07：kubeproxy 单元测试

目标：在无需 root 和真实 iptables 的环境中验证规则生成，避免手工网络 case 才发现回归。

机器：任意开发机。

流程：

```bash
go test ./cmd/minik8s ./internal/kubeproxy ./internal/bridge/captain ./internal/cli -count=1
```

期望：

- `internal/cli` 和 `internal/sailer` 测试确认 sailer 默认启用 iptables kubeproxy，并可通过 `--proxy-disabled` 关闭。
- `internal/kubeproxy` 测试确认 ClusterIP、NodePort、多 endpoint、delete 规则生成。
- ServiceController 和 CLI 测试通过。

失败排查：

- sailer proxy 测试失败：检查 CLI 是否把 `--proxy-disabled` 传入 sailer options，以及 sailer 是否调用 Service list API。
