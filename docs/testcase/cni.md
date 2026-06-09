# CNI 测试用例

本文档只记录具体 CNI 人工验收步骤。CNI 架构、运行模式和成熟化目标见
[docs/cni.md](../cni.md)。

本文档从 0 开始验证 CNI bridge、IPAM、同节点 PodIP、VXLAN 跨节点 PodIP、删除清理、
manifest 激活，以及静态 route fallback。主路径需要 node-a 的 Harbor `18080`，以及节点间
UDP `4789`。

## 测试模型

| 节点 | 宿主机 IP | PodCIDR | 运行组件 |
| --- | --- | --- | --- |
| node-a | `192.168.1.8` | `10.244.0.0/24` | `bridge`、`sailer` |
| node-b | `192.168.1.6` | `10.244.1.0/24` | `sailer` |

`mooring` CNI 负责本机 Pod 网络；`sailer` 使用 Node YAML 注册节点，控制面自动分配
`spec.podCIDR`，随后 `sailer` 写入本机 CNI 配置，并向 Harbor `/nodes` 注册节点网络信息，
周期性同步跨节点 VXLAN overlay。

## 从 0 启动

两台机器都需要 Linux、Docker、`ip`、`bridge`、`iptables`、`nsenter`、`curl` 或 `wget`，并以 root 用户执行命令。安全组或防火墙至少放通 node-a 入站 TCP `18080`，以及两台节点之间双向 UDP `4789`。

在 node-a 和 node-b 都设置变量：

```bash
export NODE_A_IP=192.168.1.8
export NODE_B_IP=192.168.1.6
export CLUSTER_CIDR=10.244.0.0/16
export HARBOR=http://${NODE_A_IP}:18080
export MINIK8S_HARBOR=${HARBOR}
unset MINIK8S_CNI_DISABLED
```

两台机器都构建二进制和 CNI plugin，并确认 `manifest/node/node_a.yaml`、`manifest/node/node_b.yaml` 中的 `InternalIP` 与实际机器一致：

```bash
make build
install -d -m 0755 /opt/cni/bin /etc/cni/net.d
install -m 0755 .minik8s/cni/bin/mooring /opt/cni/bin/mooring
```

在 node-a 终端 1 启动控制面：

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
export MINIK8S_HARBOR=${HARBOR}
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr ${CLUSTER_CIDR} \
  --node-cidr-mask-size 24
```

在 node-b 先确认能访问控制面：

```bash
curl -fsS ${HARBOR}/version
curl -fsS ${HARBOR}/nodes
```

在 node-a 终端 2 启动 worker：

```bash
./minik8s sailer \
  manifest/node/node_a.yaml \
  --harbor ${HARBOR}
```

在 node-b 终端 1 启动 worker：

```bash
./minik8s sailer \
  manifest/node/node_b.yaml \
  --harbor ${HARBOR}
```

在 node-a 的测试终端确认节点和 VXLAN/路由：

```bash
./kubectl get nodes
curl -fsS ${HARBOR}/nodes
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

在 node-b 确认 VXLAN/路由：

```bash
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

期望：

- `get nodes` 包含 `node-a` 和 `node-b`，状态为 `Ready`。
- `get nodes` 显示 node-a/node-b 的 PodCIDR 分别为 `10.244.0.0/24` 和 `10.244.1.0/24`。
- node-a 有到 `10.244.1.0/24` 的 route，且 `mk8s-vxlan` FDB 指向 node-b IP。
- node-b 有到 `10.244.0.0/24` 的 route，且 `mk8s-vxlan` FDB 指向 node-a IP。

## 通用清理

每个 case 都可以单独运行。运行前后建议在 node-a 执行一次清理，避免残留 Pod 影响判断：

```bash
./kubectl delete pod busybox-node-b || true
./kubectl delete pod busybox-client || true
./kubectl delete pod nginx-node-a || true
./kubectl delete pod nginx-node-b || true
./kubectl delete pod nginx-pod || true
sleep 8
```

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| CNI-00 | manifest 激活自研 CNI | node-a + node-b | 可选 |
| CNI-01 | CNI 配置与 Pod IP 分配 | node-a + node-b | 是 |
| CNI-02 | 同节点 PodIP 通信 | node-a | 是 |
| CNI-03 | 跨节点 PodIP 通信 | node-a + node-b | 是 |
| CNI-04 | CNI DEL 与 IPAM 清理 | node-a + node-b | 是 |
| CNI-05 | 静态 route fallback | node-a + node-b | 可选 |

## CNI-00：manifest 激活自研 CNI

目标：验证自研 `mooring` 可以通过 ConfigMap 激活，并由 `sailer` 在节点启动时完成本地引导。
该模式仍使用 `sailer` 写本机 CNI 配置，并保留内置 `netagent` 做跨节点同步。

在 node-a apply manifest：

```bash
./kubectl apply -f manifest/cni/mooring.yaml
```

然后按“从 0 启动”中的方式启动 node-a/node-b 的 `sailer`。启动后分别在两台机器检查：

```bash
cat /etc/cni/net.d/10-mooring.conf
./minik8s doctor network
```

期望：

- `kubectl apply` 输出 `namespace/kube-mooring accepted`、
  `configmap/mooring-cni-cfg created`。
- node-a 的 `10-mooring.conf` 中 `type` 为 `mooring`，`podCIDR` 为
  `10.244.0.0/24`，`gateway` 为 `10.244.0.1`。
- node-b 的 `10-mooring.conf` 中 `type` 为 `mooring`，`podCIDR` 为
  `10.244.1.0/24`，`gateway` 为 `10.244.1.1`。
- 两台机器仍能看到 `mk8s-vxlan` 和对端 PodCIDR route，说明内置 `netagent` 未被禁用。

失败排查：

- 没有生成 `10-mooring.conf`：确认 ConfigMap 名称是 `kube-mooring/mooring-cni-cfg`。
- `type` 不是 `mooring`：检查 ConfigMap 的 `data.cni-conf.json`。
- PodCIDR 为空或错误：确认 `kubectl get nodes` 已显示节点 PodCIDR，且 `sailer` 使用的是正确
  Node YAML。

## CNI-01：配置与 Pod IP 分配

目标：确认两台机器使用不同 PodCIDR，Pod 启动后获得各自网段内 IP。

在 node-a：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./kubectl get pods
./kubectl get pod nginx-node-a -o yaml
./kubectl get pod nginx-node-b -o yaml
```

在 node-a：

```bash
cat .minik8s/state/cni-ipam.json
```

在 node-b：

```bash
cat .minik8s/state/cni-ipam.json
```

期望：

- `nginx-node-a` 为 `Running`，IP 属于 `10.244.0.0/24`。
- `nginx-node-b` 为 `Running`，IP 属于 `10.244.1.0/24`。
- node-a IPAM 文件包含 `default/nginx-node-a`。
- node-b IPAM 文件包含 `default/nginx-node-b`。

失败排查：

- PodIP 为空：检查对应节点 `./minik8s doctor network`，确认 `MINIK8S_CNI_DISABLED` 未设置为 `1`。
- 两个 Pod 拿到同一网段：检查 `./kubectl get nodes` 中两个节点的 PodCIDR 是否不同，并确认两个 sailer 使用了不同 Node YAML。

## CNI-02：同节点 PodIP 通信

目标：验证 node-a 上的 client Pod 可以通过 PodIP 访问 node-a 上的 nginx Pod。

在 node-a：

```bash
./kubectl apply -f manifest/pod/pod_nginx.yaml
./kubectl apply -f manifest/pod/pod_busybox_client.yaml
sleep 8
SERVER_IP=$(./kubectl get pod nginx-pod -o yaml | awk '/podIP:/ {print $2; exit}')
CLIENT_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "${CLIENT_CID}" wget -qO- "http://${SERVER_IP}:80" >/tmp/mooring-cni-same-node.html
head -n 1 /tmp/mooring-cni-same-node.html
```

期望：

- `SERVER_IP` 是 `10.244.0.0/24` 内地址。
- `wget` 返回 nginx HTML。

失败排查：

- client 容器不存在：确认 node-a 的 sailer 正在运行。
- `wget` 超时：检查 `mk8s0`、veth、iptables MASQUERADE。

## CNI-03：跨节点 PodIP 通信

目标：验证 node-b 上的 client 可以访问 node-a 的 nginx PodIP，并验证 node-a 上的 client 可以访问 node-b 的 nginx PodIP。

在 node-a 创建测试 Pod 并记录 IP：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
./kubectl apply -f manifest/pod/pod_busybox_node_b.yaml
./kubectl apply -f manifest/pod/pod_busybox_client.yaml
sleep 10
./kubectl get pods
NGINX_A_IP=$(./kubectl get pod nginx-node-a -o yaml | awk '/podIP:/ {print $2; exit}')
NGINX_B_IP=$(./kubectl get pod nginx-node-b -o yaml | awk '/podIP:/ {print $2; exit}')
echo "${NGINX_A_IP} ${NGINX_B_IP}"
```

在 node-b，验证访问 node-a：

```bash
export NGINX_A_IP=<node-a 输出的 NGINX_A_IP>
CLIENT_B_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-node-b --filter label=minik8s.container.name=client)
docker exec "${CLIENT_B_CID}" wget -qO- "http://${NGINX_A_IP}:80" >/tmp/mooring-cni-cross-a.html
head -n 1 /tmp/mooring-cni-cross-a.html
```

在 node-a，验证访问 node-b：

```bash
CLIENT_A_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "${CLIENT_A_CID}" wget -qO- "http://${NGINX_B_IP}:80" >/tmp/mooring-cni-cross-b.html
head -n 1 /tmp/mooring-cni-cross-b.html
```

期望：

- `NGINX_A_IP` 属于 `10.244.0.0/24`。
- `NGINX_B_IP` 属于 `10.244.1.0/24`。
- 两个跨节点 `wget` 都返回 nginx HTML。
- 两台宿主机 `ip route` 中都有对端 PodCIDR，且 `mk8s-vxlan` 存在。

失败排查：

- node-b shell 没有 `NGINX_A_IP`：从 node-a 输出复制该变量，或在 node-b 手动 `export NGINX_A_IP=<value>`。
- 跨节点不通但同节点通：检查 `./kubectl get nodes` 是否已有 PodCIDR，`curl -fsS ${HARBOR}/nodes` 是否有 `nodeIP + podCIDR`，再检查 `ip link show mk8s-vxlan`、`bridge fdb show dev mk8s-vxlan`、`ip route`、宿主机防火墙、Linux `ip_forward`。
- 云主机跨节点不通：确认安全组双向放通 UDP `4789`，并用 `tcpdump -ni ens3 udp port 4789` 确认 VXLAN 包是否到达对端。
- node-b Pod 没启动：看 node-b 的 sailer 日志和 Docker 状态。

## CNI-04：删除释放 IPAM

目标：验证删除 Pod 后 sailer 调用 CNI `DEL`，释放 IPAM allocation 并删除 veth/runtime 资源。

在 node-a：

```bash
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./kubectl delete pod nginx-node-a
./kubectl delete pod nginx-node-b
sleep 8
```

在 node-a：

```bash
cat .minik8s/state/cni-ipam.json
docker ps -a --filter label=minik8s.pod.name=nginx-node-a
```

在 node-b：

```bash
cat .minik8s/state/cni-ipam.json
docker ps -a --filter label=minik8s.pod.name=nginx-node-b
```

期望：

- IPAM 文件不再包含对应 Pod key。
- Docker 不再有对应 sandbox/workload 容器。

失败排查：

- key 仍存在：确认对应节点 sailer 还在运行并已同步。
- 容器残留：检查 sailer 删除日志，必要时手动清理测试容器。

## CNI-05：静态 route fallback

目标：验证在 sailer 不负责网络路由同步时，也可以用 CNI 配置中的 `routes` 字段手动完成跨节点路由。

此 case 可选，只在排查动态路由同步时使用。执行前先停止 node-a/node-b 的 sailer，避免动态 VXLAN 路由恢复影响观察。

在 node-a 重新初始化 CNI：

```bash
./minik8s cni init \
  --pod-cidr 10.244.0.0/24 \
  --gateway 10.244.0.1 \
  --route 10.244.1.0/24=${NODE_B_IP}
./minik8s doctor network
```

在 node-b 重新初始化 CNI：

```bash
./minik8s cni init \
  --pod-cidr 10.244.1.0/24 \
  --gateway 10.244.1.1 \
  --route 10.244.0.0/24=${NODE_A_IP}
./minik8s doctor network
```

两台机器分别启动对应 Node YAML 的 sailer：

```bash
./minik8s sailer \
  manifest/node/node_a.yaml \
  --harbor ${HARBOR}
```

node-b 使用 `manifest/node/node_b.yaml`。

然后按 CNI-03 创建 Pod 并验证跨节点访问。

期望：

- `doctor network` 显示 `route: <remote PodCIDR> via <remote NodeIP>`。
- 两方向跨节点访问均成功。

## 收尾清理

在 node-a 清理 API 对象：

```bash
./kubectl delete pod busybox-node-b || true
./kubectl delete pod busybox-client || true
./kubectl delete pod nginx-node-a || true
./kubectl delete pod nginx-node-b || true
./kubectl delete pod nginx-pod || true
sleep 8
```

在两台机器检查本地残留：

```bash
docker ps -a --filter label=minik8s.pod.namespace=default
cat .minik8s/state/cni-ipam.json 2>/dev/null || true
```

## 快速排查索引

- `curl ${HARBOR}/version` 失败：先查 node-a `bridge` 是否运行、node-a 入站 `18080` 是否放通。
- `get nodes` 缺 node-b：检查 node-b sailer 的 `--harbor` 是否指向 node-a 局域网 IP。
- `/nodes` 为空：检查 Node YAML 是否包含 `InternalIP`，并确认 sailer 已从控制面拿到 `spec.podCIDR`。
- 跨节点 PodIP 不通：检查两台机器的 `ip link show mk8s-vxlan`、`bridge fdb show dev mk8s-vxlan`、`ip route | grep 10.244`，再查 UDP `4789`、防火墙、`ip_forward` 和 sailer 日志。
- Pod 一直 `Pending`：检查对应节点 sailer 是否在运行。
