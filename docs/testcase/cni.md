# CNI 测试用例

本文档从 0 开始验证 v0.1.0 的 CNI bridge、IPAM、同节点 PodIP、跨节点 PodIP、删除清理，以及静态 route fallback。主路径只需要一个控制面端口：node-a 的 Kubeharbor `18080`。

## 测试模型

| 节点 | 宿主机 IP | PodCIDR | 运行组件 |
| --- | --- | --- | --- |
| node-a | `192.168.1.8` | `10.244.0.0/24` | `kubebridge`、`kubesailer` |
| node-b | `192.168.1.6` | `10.244.1.0/24` | `kubesailer` |

`minik8s-bridge` CNI 负责本机 Pod 网络；带 `--node-ip` 和 `--pod-cidr` 的 `kubesailer` 会向 Kubeharbor `/nodes` 注册节点网络信息，并周期性同步跨节点 host-gw route。

## 从 0 启动

两台机器都需要 Linux、Docker、`ip`、`iptables`、`nsenter`、`curl` 或 `wget`，并以 root 用户执行命令。安全组或防火墙至少放通 node-a 入站 TCP `18080`，来源为 node-b 的 IP 或当前局域网网段。

在 node-a 和 node-b 都设置变量：

```bash
export NODE_A_IP=192.168.1.8
export NODE_B_IP=192.168.1.6
export POD_CIDR_A=10.244.0.0/24
export POD_CIDR_B=10.244.1.0/24
export KUBEHARBOR=http://${NODE_A_IP}:18080
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
unset MINIK8S_CNI_DISABLED
```

两台机器都构建二进制和 CNI plugin：

```bash
make build
```

在 node-a 初始化 CNI：

```bash
./minik8s cni init \
  --pod-cidr ${POD_CIDR_A} \
  --gateway 10.244.0.1
./minik8s doctor network
```

在 node-b 初始化 CNI：

```bash
./minik8s cni init \
  --pod-cidr ${POD_CIDR_B} \
  --gateway 10.244.1.1
./minik8s doctor network
```

在 node-a 终端 1 启动控制面：

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
./minik8s kubebridge --listen :18080 --service-sync-interval 5s
```

在 node-b 先确认能访问控制面：

```bash
curl -fsS ${KUBEHARBOR}/version
curl -fsS ${KUBEHARBOR}/nodes
```

在 node-a 终端 2 启动 worker：

```bash
./minik8s kubesailer \
  --node-name node-a \
  --kubeharbor ${KUBEHARBOR} \
  --node-ip ${NODE_A_IP} \
  --pod-cidr ${POD_CIDR_A}
```

在 node-b 终端 1 启动 worker：

```bash
./minik8s kubesailer \
  --node-name node-b \
  --kubeharbor ${KUBEHARBOR} \
  --node-ip ${NODE_B_IP} \
  --pod-cidr ${POD_CIDR_B}
```

在 node-a 的测试终端确认节点和路由：

```bash
./minik8s get nodes
curl -fsS ${KUBEHARBOR}/nodes
ip route | grep 10.244
```

在 node-b 确认路由：

```bash
ip route | grep 10.244
```

期望：

- `get nodes` 包含 `node-a` 和 `node-b`，状态为 `Ready`。
- node-a 有到 `10.244.1.0/24` 的 route。
- node-b 有到 `10.244.0.0/24` 的 route。

## 通用清理

每个 case 都可以单独运行。运行前后建议在 node-a 执行一次清理，避免残留 Pod 影响判断：

```bash
./minik8s delete pod busybox-node-b || true
./minik8s delete pod busybox-client || true
./minik8s delete pod nginx-node-a || true
./minik8s delete pod nginx-node-b || true
./minik8s delete pod nginx-pod || true
sleep 8
```

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| CNI-01 | CNI 配置与 Pod IP 分配 | node-a + node-b | 是 |
| CNI-02 | 同节点 PodIP 通信 | node-a | 是 |
| CNI-03 | 跨节点 PodIP 通信 | node-a + node-b | 是 |
| CNI-04 | CNI DEL 与 IPAM 清理 | node-a + node-b | 是 |
| CNI-05 | 静态 route fallback | node-a + node-b | 可选 |

## CNI-01：配置与 Pod IP 分配

目标：确认两台机器使用不同 PodCIDR，Pod 启动后获得各自网段内 IP。

在 node-a：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s apply -f manifest/testdata/pod_nginx_node_b.yaml
sleep 8
./minik8s get pods
./minik8s get pod nginx-node-a -o yaml
./minik8s get pod nginx-node-b -o yaml
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
- 两个 Pod 拿到同一网段：检查两台机器的 `cni init --pod-cidr` 参数。

## CNI-02：同节点 PodIP 通信

目标：验证 node-a 上的 client Pod 可以通过 PodIP 访问 node-a 上的 nginx Pod。

在 node-a：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/pod_busybox_client.yaml
sleep 8
SERVER_IP=$(./minik8s get pod nginx-pod -o yaml | awk '/podIP:/ {print $2; exit}')
CLIENT_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "${CLIENT_CID}" wget -qO- "http://${SERVER_IP}:80" >/tmp/minik8s-cni-same-node.html
head -n 1 /tmp/minik8s-cni-same-node.html
```

期望：

- `SERVER_IP` 是 `10.244.0.0/24` 内地址。
- `wget` 返回 nginx HTML。

失败排查：

- client 容器不存在：确认 node-a 的 kubesailer 正在运行。
- `wget` 超时：检查 `mk8s0`、veth、iptables MASQUERADE。

## CNI-03：跨节点 PodIP 通信

目标：验证 node-b 上的 client 可以访问 node-a 的 nginx PodIP，并验证 node-a 上的 client 可以访问 node-b 的 nginx PodIP。

在 node-a 创建测试 Pod 并记录 IP：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s apply -f manifest/testdata/pod_nginx_node_b.yaml
./minik8s apply -f manifest/testdata/pod_busybox_node_b.yaml
./minik8s apply -f manifest/testdata/pod_busybox_client.yaml
sleep 10
./minik8s get pods
NGINX_A_IP=$(./minik8s get pod nginx-node-a -o yaml | awk '/podIP:/ {print $2; exit}')
NGINX_B_IP=$(./minik8s get pod nginx-node-b -o yaml | awk '/podIP:/ {print $2; exit}')
echo "${NGINX_A_IP} ${NGINX_B_IP}"
```

在 node-b，验证访问 node-a：

```bash
export NGINX_A_IP=<node-a 输出的 NGINX_A_IP>
CLIENT_B_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-node-b --filter label=minik8s.container.name=client)
docker exec "${CLIENT_B_CID}" wget -qO- "http://${NGINX_A_IP}:80" >/tmp/minik8s-cni-cross-a.html
head -n 1 /tmp/minik8s-cni-cross-a.html
```

在 node-a，验证访问 node-b：

```bash
CLIENT_A_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "${CLIENT_A_CID}" wget -qO- "http://${NGINX_B_IP}:80" >/tmp/minik8s-cni-cross-b.html
head -n 1 /tmp/minik8s-cni-cross-b.html
```

期望：

- `NGINX_A_IP` 属于 `10.244.0.0/24`。
- `NGINX_B_IP` 属于 `10.244.1.0/24`。
- 两个跨节点 `wget` 都返回 nginx HTML。
- 两台宿主机 `ip route` 中都有对端 PodCIDR。

失败排查：

- node-b shell 没有 `NGINX_A_IP`：从 node-a 输出复制该变量，或在 node-b 手动 `export NGINX_A_IP=<value>`。
- 跨节点不通但同节点通：检查 kubesailer 是否带了 `--node-ip` 和 `--pod-cidr`，再检查 `ip route`、宿主机防火墙、Linux `ip_forward`。
- node-b Pod 没启动：看 node-b 的 kubesailer 日志和 Docker 状态。

## CNI-04：删除释放 IPAM

目标：验证删除 Pod 后 kubesailer 调用 CNI `DEL`，释放 IPAM allocation 并删除 veth/runtime 资源。

在 node-a：

```bash
./minik8s apply -f manifest/testdata/pod_nginx_node_a.yaml
./minik8s apply -f manifest/testdata/pod_nginx_node_b.yaml
sleep 8
./minik8s delete pod nginx-node-a
./minik8s delete pod nginx-node-b
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

- key 仍存在：确认对应节点 kubesailer 还在运行并已同步。
- 容器残留：检查 kubesailer 删除日志，必要时手动清理测试容器。

## CNI-05：静态 route fallback

目标：验证在 kubesailer 不负责网络路由同步时，也可以用 CNI 配置中的 `routes` 字段手动完成跨节点路由。

此 case 可选，只在排查动态路由同步时使用。执行前先停止 node-a/node-b 的 kubesailer，并重新启动不带 `--node-ip`、`--pod-cidr` 的 kubesailer，避免动态路由恢复影响观察。

在 node-a 重新初始化 CNI：

```bash
./minik8s cni init \
  --pod-cidr ${POD_CIDR_A} \
  --gateway 10.244.0.1 \
  --route ${POD_CIDR_B}=${NODE_B_IP}
./minik8s doctor network
```

在 node-b 重新初始化 CNI：

```bash
./minik8s cni init \
  --pod-cidr ${POD_CIDR_B} \
  --gateway 10.244.1.1 \
  --route ${POD_CIDR_A}=${NODE_A_IP}
./minik8s doctor network
```

两台机器分别启动不带网络参数的 kubesailer：

```bash
./minik8s kubesailer \
  --node-name <node-a-or-node-b> \
  --kubeharbor ${KUBEHARBOR}
```

然后按 CNI-03 创建 Pod 并验证跨节点访问。

期望：

- `doctor network` 显示 `route: <remote PodCIDR> via <remote NodeIP>`。
- 两方向跨节点访问均成功。

## 收尾清理

在 node-a 清理 API 对象：

```bash
./minik8s delete pod busybox-node-b || true
./minik8s delete pod busybox-client || true
./minik8s delete pod nginx-node-a || true
./minik8s delete pod nginx-node-b || true
./minik8s delete pod nginx-pod || true
sleep 8
```

在两台机器检查本地残留：

```bash
docker ps -a --filter label=minik8s.pod.namespace=default
cat .minik8s/state/cni-ipam.json 2>/dev/null || true
```

## 快速排查索引

- `curl ${KUBEHARBOR}/version` 失败：先查 node-a `kubebridge` 是否运行、node-a 入站 `18080` 是否放通。
- `get nodes` 缺 node-b：检查 node-b kubesailer 的 `--kubeharbor` 是否指向 node-a 局域网 IP。
- `/nodes` 为空：检查 kubesailer 是否带 `--node-ip` 和 `--pod-cidr`。
- 跨节点 PodIP 不通：检查两台机器的 `ip route | grep 10.244`，再查防火墙、`ip_forward` 和 kubesailer 日志。
- Pod 一直 `Pending`：检查对应节点 kubesailer 是否在运行。
