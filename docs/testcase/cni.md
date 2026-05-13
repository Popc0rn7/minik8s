# CNI 测试用例

本文档覆盖 v0.1.0 的 CNI bridge、IPAM、同节点 PodIP 通信、跨节点 PodIP 通信和删除清理。双机公共启动流程见 `docs/testcase/two-node.md`。

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| CNI-01 | CNI 配置与 Pod IP 分配 | node-a + node-b | 是 |
| CNI-02 | 同节点 PodIP 通信 | node-a | 是 |
| CNI-03 | 跨节点 PodIP 通信 | node-a + node-b | 是 |
| CNI-04 | CNI DEL 与 IPAM 清理 | node-a + node-b | 是 |
| CNI-05 | 静态 route fallback | node-a + node-b | 可选 |

## CNI-01：配置与 Pod IP 分配

目标：确认两台机器都使用不同 PodCIDR，Pod 启动后获得各自网段内 IP。

机器：node-a 执行 CLI；node-a/node-b 都运行 kubesailer 和 netd。

流程：

```bash
unset MINIK8S_CNI_DISABLED
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

- PodIP 为空：检查对应节点 `doctor network`，确认 `MINIK8S_CNI_DISABLED` 未设置为 `1`。
- 两个 Pod 拿到同一网段：检查两台机器的 `cni init --pod-cidr` 参数。

## CNI-02：同节点 PodIP 通信

目标：验证同一节点上的 client Pod 可以通过 PodIP 访问 nginx Pod。

机器：node-a。

流程：

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
- `wget` 成功返回 nginx HTML。

失败排查：

- client 容器不存在：确认 `busybox-client` 固定在 `node-a` 且 node-a kubesailer 正常。
- `wget` 超时：检查 `mk8s0`、veth、iptables MASQUERADE。

清理：

```bash
./minik8s delete pod busybox-client || true
./minik8s delete pod nginx-pod || true
```

## CNI-03：跨节点 PodIP 通信

目标：验证 node-b 上的 client 可以访问 node-a 上的 nginx PodIP，并验证反向从 node-a 访问 node-b 后端。

机器：node-a 执行 API 命令；node-a/node-b 分别执行本地 Docker exec。

流程：

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
- 两个跨节点 `wget` 均返回 nginx HTML。
- 两台宿主机 `ip route` 中都有对端 PodCIDR。

失败排查：

- node-b shell 没有 `NGINX_A_IP`：从 node-a 输出复制该变量，或在 node-b 手动 `export NGINX_A_IP=<value>`。
- 跨节点不通但同节点通：检查 `netd`、`ip route`、宿主机防火墙、Linux `ip_forward`。

清理：

```bash
./minik8s delete pod busybox-node-b || true
./minik8s delete pod busybox-client || true
./minik8s delete pod nginx-node-a || true
./minik8s delete pod nginx-node-b || true
```

## CNI-04：删除释放 IPAM

目标：验证删除 Pod 后 kubesailer 调用 CNI `DEL`，释放 IPAM allocation 并删除 veth/runtime 资源。

机器：node-a 执行 API 删除；两台机器检查本地状态。

流程：

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

目标：在不运行 `netd` 时，用 CNI 配置中的 `routes` 字段手动完成跨节点路由。

机器：node-a + node-b。此 case 可选，只在排查 `netd` 时使用。

流程：

```bash
# node-a
sudo ./minik8s cni init \
  --pod-cidr ${POD_CIDR_A} \
  --gateway 10.244.0.1 \
  --route ${POD_CIDR_B}=${NODE_B_IP}

# node-b
sudo ./minik8s cni init \
  --pod-cidr ${POD_CIDR_B} \
  --gateway 10.244.1.1 \
  --route ${POD_CIDR_A}=${NODE_A_IP}
```

期望：

- `./minik8s doctor network` 显示 `route: <remote PodCIDR> via <remote NodeIP>`。
- 后续执行 CNI-03 时跨节点访问成功。
