# Service / kube-proxy 测试用例

本文档覆盖 Minik8s Service 的 Kubernetes 基本语义：selector 生成 endpoints，
ClusterIP 提供稳定虚拟 IP，NodePort 暴露宿主机端口，多 endpoint 生成负载均衡规则，
Pod 变化后 endpoints 动态刷新，删除 Service 后清理 iptables。

双节点公共启动流程见 [`README.md`](README.md) 和 [`two-node.md`](two-node.md)。

注意：当前 kube-proxy 由运行 `sailer` 的节点同步 iptables。默认环境下 node-a 是必测
数据面入口；node-b NodePort 可作为观察项，只有 node-b 也有等价 proxy 规则时才要求成功。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| SVC-00 | Service 环境基线 | node-a + node-b | 保持默认环境 |
| SVC-01 | selector 生成 endpoints | node-a | 删除 Service/Pod |
| SVC-02 | ClusterIP 规则与 Pod 内访问 | node-a | 删除 Service/client/backend |
| SVC-03 | NodePort 规则与宿主机访问 | node-a，node-b 观察 | 删除 NodePort Service |
| SVC-04 | 双节点多 endpoint 与负载均衡规则 | node-a + node-b | 删除两个 backend |
| SVC-05 | endpoint 动态更新 | node-a + node-b | 删除 Service/Pod |
| SVC-06 | 删除 Service 清理 iptables | node-a | 无 MK8S-SVC 残留 |
| SVC-07 | kubeproxy 单元测试 | 任意开发机 | 不改变集群 |

## SVC-00：环境基线

目标：确认默认双节点环境、CNI 和 kube-proxy 前置可用。

```fish
./kubectl get nodes
./kubectl get services; or true
iptables-save -t nat | grep MK8S-SVC; or true
```

期望：

- `node-a` 和 `node-b` 均为 `Ready`。
- node-a 当前 shell 有 root/iptables 权限。
- 旧 `MK8S-SVC` 规则不影响本轮判断；如有残留，先执行 SVC-06 或通用清理。

## SVC-01：Service endpoints

目标：验证 ServiceController 根据 selector 选中 Running Pod，并写入 endpoints。

```fish
./kubectl delete service nginx-service; or true
./kubectl delete pod nginx-node-a; or true
sleep 6

./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
sleep 8
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 6
./kubectl get services
./kubectl describe service nginx-service
./kubectl get pod nginx-node-a -o yaml
```

期望：

- `nginx-service` 类型为 `ClusterIP`，namespace 为 `default`。
- ClusterIP 为 `10.96.0.1` 或当前 allocator 的首个可用 IP。
- endpoints 包含 `nginx-node-a` 的 PodIP 和 targetPort `80`。
- endpoints 只来自 Running 且 label 匹配 `app=nginx` 的 Pod。

失败排查：

- endpoints 为空：确认 Pod 已 Running、PodIP 非空、label `app=nginx` 存在。
- Service 不存在：确认 `MINIK8S_HARBOR` 指向当前 bridge。

## SVC-02：ClusterIP 规则与数据面

目标：验证 node-a kube-proxy 写入 iptables NAT 规则，并能从 Pod 内访问 ClusterIP。

```fish
./kubectl delete pod busybox-client; or true
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_busybox_client.yaml
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 10

iptables-save -t nat | grep -E 'MK8S-SVC|10\.96\.0\.1'
set CLIENT_CID (docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "$CLIENT_CID" wget -qO- http://10.96.0.1:80 >/tmp/minik8s-service-clusterip.html
head -n 1 /tmp/minik8s-service-clusterip.html
```

期望：

- iptables 中存在 `MK8S-SVC-*` chain。
- `PREROUTING` 和 `OUTPUT` 有指向 `10.96.0.1:80` 的入口规则。
- Service chain 内存在 `DNAT --to-destination <nginx-node-a-pod-ip>:80`。
- Pod 内访问 `http://10.96.0.1:80` 返回 nginx HTML。

失败排查：

- `CLIENT_CID` 为空：先 `describe pod busybox-client`，到实际调度节点执行 Docker 命令。
- 有规则但访问失败：检查 CNI PodIP、route、container namespace 和 node-a proxy 日志。

## SVC-03：NodePort 规则与宿主机访问

目标：验证 NodePort Service 在 node-a 暴露 `30080`。

```fish
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/service/service_nodeport_nginx.yaml
sleep 8

./kubectl get services
iptables-save -t nat | grep -E 'MK8S-SVC|30080|10\.96\.0\.1'
curl --noproxy '*' -fsS "http://$NODE_A_IP:30080" >/tmp/minik8s-service-nodeport-a.html
head -n 1 /tmp/minik8s-service-nodeport-a.html
```

node-b 可选观察：

```fish
curl --noproxy '*' -fsS "http://$NODE_B_IP:30080" >/tmp/minik8s-service-nodeport-b.html; or true
```

期望：

- `nginx-nodeport` 类型为 `NodePort`，展示 `80->80/TCP:30080` 或等价字段。
- node-a iptables 中存在 `--dport 30080 -j MK8S-SVC-*`。
- node-a 访问 `${NODE_A_IP}:30080` 返回 nginx HTML。
- node-b 只有在本机也运行等价 proxy 规则时才要求成功。

失败排查：

- node-a curl 失败：检查宿主机防火墙、iptables 规则、backend endpoint。
- node-a 直连 PodIP 或 NodePort 失败且 `ip route get <pod-ip>` 没有走 `mk8s0`：检查是否有
  旧 `cni0` 或其他同 PodCIDR route 抢路由；重启当前 `sailer run` 后 netagent 应刷新本地
  PodCIDR route 到 `mk8s0`。
- node-b curl 失败：先记录为观察项，不影响 node-a 必测结论。

## SVC-04：双节点多 endpoint 与负载均衡

目标：验证 Service 可以选中分布在 node-a/node-b 的后端，并生成多条 DNAT 规则。

```fish
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 10

./kubectl get pods
./kubectl describe service nginx-service
iptables-save -t nat | grep MK8S-SVC
```

期望：

- endpoints 包含两个 PodIP：一个属于 `10.244.0.0/24`，一个属于 `10.244.1.0/24`。
- Service chain 内至少两条 DNAT。
- 除最后一个 endpoint 外，前置 DNAT 规则包含 `-m statistic --mode random --probability ...`。

失败排查：

- 只有一个 endpoint：检查另一个节点 sailer 是否运行，Pod 是否 Running。
- node-b endpoint 有但访问失败：检查 node-a 到 `10.244.1.0/24` 的 route 和 VXLAN。

## SVC-05：endpoint 动态更新

目标：验证新增/删除匹配 Pod 后，Service endpoints 和 iptables 规则周期性刷新。

```fish
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod nginx-node-b; or true
./kubectl delete service nginx-service; or true
sleep 8

./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 8
./kubectl describe service nginx-service

./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./kubectl describe service nginx-service
iptables-save -t nat | grep MK8S-SVC

set NODE_A_POD_IP (./kubectl get pod nginx-node-a -o yaml | awk '/podIP:/ {print $2; exit}')
./kubectl delete pod nginx-node-a
sleep 8
./kubectl describe service nginx-service
iptables-save -t nat | grep "$NODE_A_POD_IP"; or true
```

期望：

- 初始只有 node-a endpoint。
- 新增 node-b 后 endpoints 变为两个。
- 删除 node-a 后 endpoints 只剩 node-b，iptables 不再包含 node-a 的 PodIP。

失败排查：

- endpoints 延迟不变：等待一个 service sync 周期，通常约 5s。
- 旧 DNAT 仍在：检查 bridge 日志中的 `service-periodic-sync`。

## SVC-06：删除 Service 清理规则

目标：验证删除 Service 后入口规则、Service chain 和 DNAT 规则被清理。

```fish
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 8
iptables-save -t nat | grep MK8S-SVC

./kubectl delete service nginx-service
sleep 3
./kubectl get services
iptables-save -t nat | grep MK8S-SVC; or true
```

期望：

- 删除前能看到 `MK8S-SVC-*`。
- 删除后 `get services` 不再显示 `nginx-service`。
- 删除后不再有该 Service 对应的 `MK8S-SVC-*` chain。

失败排查：

- chain 残留：确认删除的是对应 Service，等待一个 kube-proxy sync 周期后复查；如果仍残留，
  检查是否存在重复入口规则或旧版本 `sailer run` 进程。

## SVC-07：kubeproxy 单元测试

目标：在无需 root 和真实 iptables 的环境中验证规则生成，避免只靠人工网络 case 发现回归。

```fish
go test ./internal/kubeproxy ./internal/bridge/captain ./internal/cli ./cmd/minik8s -count=1
```

期望：

- kubeproxy 测试覆盖 ClusterIP、NodePort、多 endpoint、delete 规则生成。
- ServiceController 测试覆盖 endpoints 选择、状态写回和删除刷新。
- CLI 测试覆盖 `--proxy-disabled`、Service apply/get/describe/delete。

## 全量恢复

node-a：

```fish
./kubectl delete service nginx-service; or true
./kubectl delete service nginx-nodeport; or true
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod nginx-node-b; or true
./kubectl delete pod busybox-client; or true
rm -f /tmp/minik8s-service-clusterip.html
rm -f /tmp/minik8s-service-nodeport-a.html
sleep 8
./kubectl get services
./kubectl get pods
iptables-save -t nat | grep MK8S-SVC; or true
```

node-b：

```fish
rm -f /tmp/minik8s-service-nodeport-b.html
docker ps -a --filter label=minik8s.pod.namespace=default --format '{{.Names}} {{.Status}}'
```
