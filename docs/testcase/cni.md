# CNI 测试用例

本文档覆盖 v0.1.0 的自研 `mooring` CNI 主路径：manifest 激活、节点 PodCIDR、
CNI 配置落地、Pod IP 分配、同节点 PodIP 通信、VXLAN 跨节点 PodIP 通信、
CNI DEL/IPAM 清理，以及静态 route fallback。

CNI 架构、运行模式和成熟化目标见 [docs/cni.md](../cni.md)。通用双机环境见
[docs/testcase/README.md](README.md)。

## 覆盖矩阵

| Case | 目标 | 机器 | 状态恢复要求 |
| --- | --- | --- | --- |
| CNI-00 | 默认 CNI 环境基线 | node-a + node-b | 保持两个节点 Ready |
| CNI-01 | manifest 激活自研 CNI | node-a + node-b | 保留默认 CNI manifest 和 worker |
| CNI-02 | CNI 配置与 Pod IP 分配 | node-a + node-b | 删除测试 Pod，IPAM 不含测试 Pod key |
| CNI-03 | 同节点 PodIP 通信 | node-a | 删除测试 Pod 和临时 HTML |
| CNI-04 | 跨节点 PodIP 通信 | node-a + node-b | 删除测试 Pod 和临时 HTML |
| CNI-05 | CNI DEL 与 IPAM 清理 | node-a + node-b | 删除测试 Pod，无 runtime/IPAM 残留 |
| CNI-06 | 静态 route fallback | node-a + node-b | 可选；恢复默认 worker 或执行 `doctor clean` |

## 默认环境

机器：node-a + node-b。

前置：

- 两台机器都使用 Linux root shell，并具备 Docker、`ip`、`bridge`、`iptables`、
  `nsenter`、`curl` 或 `wget`。
- node-a 入站 TCP `18080` 可达，两台节点之间双向 UDP `4789` 可达。
- `manifest/node/node_a.yaml` 和 `manifest/node/node_b.yaml` 中的
  `status.addresses[type=InternalIP]` 与实际主机 IP 一致。
- 每个测试终端已设置 `docs/testcase/README.md` 中的 fish 环境变量；如果 IP 不同，
  先改 `NODE_A_IP` 和 `NODE_B_IP`。
- 如果 `/root/.config/fish/config.fish` 设置了 `HTTP_PROXY`、`HTTPS_PROXY` 或
  `all_proxy`，同时确认 `NO_PROXY/no_proxy` 覆盖 `192.168.0.0/16`、`10.244.0.0/16`
  和 `10.96.0.0/12`。本 testcase 中访问 Harbor LAN 地址的 `curl` 命令统一使用
  `--noproxy '*'`，避免代理把 `http://192.168.1.8:18080` 转成 502。
- 默认使用 `make prod-deploy` 构建和同步产物；需要手工构建时，至少确保
  `./minik8s`、`./kubectl` 和 `mooring-cni` 安装镜像可用。

node-a 和 node-b 的每个测试终端：

```fish
set -gx NODE_A_IP 192.168.1.8; set -gx NODE_B_IP 192.168.1.6; set -gx CLUSTER_CIDR 10.244.0.0/16; set -gx HARBOR http://$NODE_A_IP:18080; set -gx MINIK8S_HARBOR $HARBOR; set -gx MINIK8S_STATE_DIR .minik8s/testcase-state; set -gx MINIK8S_TOKEN minik8s
set -e MINIK8S_CNI_DISABLED
```

node-a 终端 1 启动控制面：

```fish
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24
```

node-a 测试终端启用自研 CNI manifest 并设置 worker token：

```fish
./kubectl apply -f manifest/cni/mooring.yaml
./minik8s bridge token set $MINIK8S_TOKEN --ttl 24h
```

node-a worker 终端：

```fish
./minik8s sailer join \
  --apiserver http://$NODE_A_IP:18080 \
  --token $MINIK8S_TOKEN \
  -f manifest/node/node_a.yaml

./minik8s sailer run
```

node-b worker 终端：

```fish
./minik8s sailer join \
  --apiserver http://$NODE_A_IP:18080 \
  --token $MINIK8S_TOKEN \
  -f manifest/node/node_b.yaml

./minik8s sailer run
```

如果只需要重跑单个 case，先确认以上默认环境仍在运行，不要重复启动多个 worker。

## CNI-00：默认 CNI 环境基线

目标：确认控制面、两个 worker、PodCIDR 分配、CNI 配置、VXLAN 设备和跨节点 route
都处于可测试状态。

机器：node-a + node-b。

前置：已按“默认环境”启动 `bridge`、应用 `manifest/cni/mooring.yaml`，并在两台机器
启动默认 `sailer run`。

流程：

node-a 测试终端：

```fish
./kubectl version
./kubectl get nodes
curl --noproxy '*' -fsS $HARBOR/nodes
cat /etc/cni/net.d/10-mooring.conf
./minik8s doctor network
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

node-b 终端：

```fish
cat /etc/cni/net.d/10-mooring.conf
./minik8s doctor network
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

期望：

- `get nodes` 包含 `node-a` 和 `node-b`，状态均为 `Ready`。
- node-a/node-b 的 PodCIDR 分别为 `10.244.0.0/24` 和 `10.244.1.0/24`。
- 两台机器的 `/etc/cni/net.d/10-mooring.conf` 中 `type` 为 `mooring`。
- node-a 的 CNI 配置包含 `podCIDR: 10.244.0.0/24` 和 `gateway: 10.244.0.1`。
- node-b 的 CNI 配置包含 `podCIDR: 10.244.1.0/24` 和 `gateway: 10.244.1.1`。
- node-a 有到 `10.244.1.0/24` 的 route，node-b 有到 `10.244.0.0/24` 的 route。
- 两台机器都有 `mk8s-vxlan`，且 FDB 指向对端 Node IP。

恢复状态：此 case 不创建业务对象。完成后保持两个 worker 运行。

失败排查：

- `curl $HARBOR/version` 失败：先查 node-a `bridge` 是否仍在运行，以及 TCP `18080`
  是否对 node-b 可达；如果返回 502，先检查 `/root/.config/fish/config.fish` 的代理设置，
  或临时使用 `curl --noproxy '*' $HARBOR/version` 复核。
- `get nodes` 缺 node-b：检查 node-b `sailer join` 使用的 `--apiserver`、token 和
  Node YAML。
- PodCIDR 为空：确认 `bridge` 启动参数包含 `--cluster-cidr` 和
  `--node-cidr-mask-size 24`，再重启 worker 完成一次 join/heartbeat。
- 无 VXLAN 或 route：检查两台节点的 `InternalIP`、`curl -fsS $HARBOR/nodes` 输出、
  UDP `4789`、宿主机防火墙和 `ip_forward`。如果远端 shell 配了代理，用
  `curl --noproxy '*' -fsS $HARBOR/nodes` 复核。

## CNI-01：manifest 激活自研 CNI

目标：验证自研 `mooring` 可以通过 ConfigMap + DaemonSet 兼容对象激活，并由
`sailer` 在节点启动时完成本地 CNI 引导。该模式仍由 `sailer` 写本机 CNI 配置，
并保留内置 `netagent` 做跨节点同步。

机器：node-a + node-b。

前置：

- 推荐在干净状态目录或默认启动流程早期执行本 case。
- 若两个 worker 已经运行，先记录状态；只为了验证启动激活路径时，可以重启两台
  `sailer run`，不要停止 `bridge`。

流程：

node-a 测试终端：

```fish
./kubectl apply -f manifest/cni/mooring.yaml
./kubectl get nodes
```

如果 worker 尚未启动，按“默认环境”分别启动 node-a/node-b 的 `sailer run`。启动后在两台机器检查：

```fish
cat /etc/cni/net.d/10-mooring.conf
ls -l /opt/cni/bin/mooring
./minik8s doctor network
```

期望：

- `apply` 输出包含 `namespace/kube-mooring`、`configmap/mooring-cni-cfg` 和
  `daemonset/mooring-cni-ds` 的创建或 accepted 结果。
- `sailer` 已通过 `ghcr.io/popc0rn7/mooring-cni:latest` 安装
  `/opt/cni/bin/mooring`，或该文件已经存在且可执行。
- 两台机器的 `10-mooring.conf` 使用各自 PodCIDR 和 gateway。
- 两台机器仍能看到 `mk8s-vxlan` 和对端 PodCIDR route，说明内置 `netagent` 未被禁用。

恢复状态：保留 `manifest/cni/mooring.yaml` 创建的对象，保持默认 CNI 环境用于后续 case。

失败排查：

- 没有生成 `10-mooring.conf`：确认 ConfigMap 名称是
  `kube-mooring/mooring-cni-cfg`，DaemonSet 名称是 `kube-mooring/mooring-cni-ds`，
  且节点能拉取 `mooring-cni` 镜像。
- `type` 不是 `mooring`：检查 ConfigMap 的 `data.cni-conf.json`。
- PodCIDR 为空或错误：确认 `kubectl get nodes` 已显示节点 PodCIDR，且 worker 使用了正确
  Node YAML。

## CNI-02：配置与 Pod IP 分配

目标：确认两台机器使用不同 PodCIDR，固定到对应节点的 Pod 启动后获得各自网段内 IP，
并写入本节点 IPAM 状态。

机器：node-a + node-b。

前置：node-a/node-b 均运行默认 `sailer run`，`./kubectl get nodes` 能看到两个节点为
`Ready`。

流程：

node-a 测试终端：

```fish
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod nginx-node-b; or true
sleep 6

./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./kubectl get pods
./kubectl get pod nginx-node-a -o yaml
./kubectl get pod nginx-node-b -o yaml
```

node-a 终端：

```fish
cat .minik8s/state/cni-ipam.json
docker ps --filter label=minik8s.pod.name=nginx-node-a
```

node-b 终端：

```fish
cat .minik8s/state/cni-ipam.json
docker ps --filter label=minik8s.pod.name=nginx-node-b
```

期望：

- `nginx-node-a` 为 `Running`，`status.podIP` 属于 `10.244.0.0/24`。
- `nginx-node-b` 为 `Running`，`status.podIP` 属于 `10.244.1.0/24`。
- node-a IPAM 文件包含 `default/nginx-node-a`，不包含 `default/nginx-node-b`。
- node-b IPAM 文件包含 `default/nginx-node-b`，不包含 `default/nginx-node-a`。
- 两台机器 Docker 中分别能看到对应 Pod 的 sandbox 和 workload 容器。

恢复状态：

```fish
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod nginx-node-b; or true
sleep 8
```

在 node-a/node-b 分别确认对应 IPAM key 和 Docker 容器已清理；如果后续马上执行
CNI-04 或 CNI-05，也可以保留对象并在下一个 case 的前置清理中处理。

失败排查：

- PodIP 为空：检查对应节点 `./minik8s doctor network`，确认未设置
  `MINIK8S_CNI_DISABLED=1`。
- 两个 Pod 拿到同一网段：检查 `./kubectl get nodes` 中两个节点的 PodCIDR 是否不同，
  并确认两个 worker 使用了不同 Node YAML。
- IPAM key 出现在错误节点：确认 Pod YAML 的 `spec.nodeName` 是否分别为
  `node-a` 和 `node-b`。

## CNI-03：同节点 PodIP 通信

目标：验证 node-a 上的 client Pod 可以通过 PodIP 访问 node-a 上的 nginx Pod。

机器：node-a。

前置：

- 默认双 worker 环境保持运行。
- 本 case 使用固定到 node-a 的 nginx 和固定到 node-a 的
  `manifest/pod/pod_busybox_node_a.yaml`。不要用未指定 `nodeSelector` 的
  `manifest/pod/pod_busybox_client.yaml` 作为同节点断言依据。

流程：

node-a 测试终端：

```fish
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod busybox-node-a; or true
rm -f /tmp/mooring-cni-same-node.html
sleep 6

./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_busybox_node_a.yaml
sleep 8
./kubectl get pods
./kubectl get pod nginx-node-a -o yaml
./kubectl get pod busybox-node-a -o yaml
```

确认 `busybox-node-a` 的 `spec.nodeName` 是 `node-a` 后，在 node-a 执行：

```fish
set SERVER_IP (./kubectl get pod nginx-node-a -o yaml | awk '/podIP:/ {print $2; exit}')
set CLIENT_CID (docker ps -q --filter label=minik8s.pod.name=busybox-node-a --filter label=minik8s.container.name=client)
docker exec "$CLIENT_CID" wget -qO- "http://$SERVER_IP:80" >/tmp/mooring-cni-same-node.html
head -n 1 /tmp/mooring-cni-same-node.html
```

期望：

- `nginx-node-a` 和 `busybox-node-a` 均为 `Running`，且都运行在 node-a。
- `SERVER_IP` 属于 `10.244.0.0/24`。
- `wget` 返回 nginx HTML。

恢复状态：

```fish
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod busybox-node-a; or true
rm -f /tmp/mooring-cni-same-node.html
sleep 8
```

失败排查：

- `busybox-node-a` 被分到 node-b：检查 `manifest/pod/pod_busybox_node_a.yaml` 是否保留
  `nodeSelector: node: node-a`，并确认 Node label 中有 `node=node-a`。
- `CLIENT_CID` 为空：确认 node-a 的 worker 正在运行，并检查 `busybox-node-a` 的实际节点。
- `wget` 超时：检查 node-a 的 `mk8s0`、veth、iptables MASQUERADE 和
  `./minik8s doctor network`。

## CNI-04：跨节点 PodIP 通信

目标：验证 node-b 上的 client 可以访问 node-a 的 nginx PodIP，并验证 node-a 上的
client 可以访问 node-b 的 nginx PodIP。

机器：node-a + node-b。

前置：

- 默认双 worker 环境保持运行，CNI-00 的 VXLAN 和 route 基线通过。
- 本 case 使用固定到 node-a 的 `manifest/pod/pod_busybox_node_a.yaml` 和固定到 node-b 的
  `manifest/pod/pod_busybox_node_b.yaml`，避免调度结果影响跨节点方向判断。

流程：

node-a 测试终端创建测试 Pod 并记录 IP：

```fish
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod nginx-node-b; or true
./kubectl delete pod busybox-node-a; or true
./kubectl delete pod busybox-node-b; or true
rm -f /tmp/mooring-cni-cross-b.html
sleep 8

./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
./kubectl apply -f manifest/pod/pod_busybox_node_a.yaml
./kubectl apply -f manifest/pod/pod_busybox_node_b.yaml
sleep 10
./kubectl get pods
./kubectl get pod busybox-node-a -o yaml
./kubectl get pod busybox-node-b -o yaml
set NGINX_A_IP (./kubectl get pod nginx-node-a -o yaml | awk '/podIP:/ {print $2; exit}')
set NGINX_B_IP (./kubectl get pod nginx-node-b -o yaml | awk '/podIP:/ {print $2; exit}')
echo "$NGINX_A_IP $NGINX_B_IP"
```

node-b 终端验证访问 node-a：

```fish
set -gx NGINX_A_IP <node-a 输出的 NGINX_A_IP>
set CLIENT_B_CID (docker ps -q --filter label=minik8s.pod.name=busybox-node-b --filter label=minik8s.container.name=client)
docker exec "$CLIENT_B_CID" wget -qO- "http://$NGINX_A_IP:80" >/tmp/mooring-cni-cross-a.html
head -n 1 /tmp/mooring-cni-cross-a.html
```

node-a 终端验证访问 node-b：

```fish
set CLIENT_A_CID (docker ps -q --filter label=minik8s.pod.name=busybox-node-a --filter label=minik8s.container.name=client)
docker exec "$CLIENT_A_CID" wget -qO- "http://$NGINX_B_IP:80" >/tmp/mooring-cni-cross-b.html
head -n 1 /tmp/mooring-cni-cross-b.html
```

期望：

- `NGINX_A_IP` 属于 `10.244.0.0/24`。
- `NGINX_B_IP` 属于 `10.244.1.0/24`。
- node-b 到 node-a、node-a 到 node-b 两个跨节点 `wget` 都返回 nginx HTML。
- 两台宿主机 `ip route` 中都有对端 PodCIDR，且 `mk8s-vxlan` 存在。

恢复状态：

```fish
./kubectl delete pod busybox-node-b; or true
./kubectl delete pod busybox-node-a; or true
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod nginx-node-b; or true
rm -f /tmp/mooring-cni-cross-b.html
sleep 8
```

node-b 也删除临时文件：

```fish
rm -f /tmp/mooring-cni-cross-a.html
```

失败排查：

- node-b shell 没有 `NGINX_A_IP`：从 node-a 输出复制该变量，或在 node-b 手动
  `set -gx NGINX_A_IP <value>`。
- `busybox-node-a` 或 `busybox-node-b` 跑到错误节点：检查对应 manifest 的
  `nodeSelector` 和 Node label。
- 跨节点不通但同节点通：检查 `./kubectl get nodes` 是否已有 PodCIDR，
  `curl --noproxy '*' -fsS $HARBOR/nodes` 是否有 `nodeIP + podCIDR`，再检查
  `ip link show mk8s-vxlan`、`bridge fdb show dev mk8s-vxlan`、`ip route`、
  宿主机防火墙、Linux `ip_forward`。
- 云主机跨节点不通：确认安全组双向放通 UDP `4789`，并用
  `tcpdump -ni <iface> udp port 4789` 确认 VXLAN 包是否到达对端。
- node-b Pod 没启动：看 node-b 的 sailer 日志和 Docker 状态。

## CNI-05：删除释放 IPAM

目标：验证删除 Pod 后 `sailer` 调用 CNI `DEL`，释放 IPAM allocation 并删除
veth/runtime 资源。

机器：node-a + node-b。

前置：默认双 worker 环境保持运行。

流程：

node-a 测试终端：

```fish
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod nginx-node-b; or true
sleep 6

./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./kubectl get pods

./kubectl delete pod nginx-node-a
./kubectl delete pod nginx-node-b
sleep 8
```

node-a 终端：

```fish
cat .minik8s/state/cni-ipam.json 2>/dev/null; or true
docker ps -a --filter label=minik8s.pod.name=nginx-node-a --format '{{.Names}} {{.Status}}'
ip link show | grep nginx-node-a; or true
```

node-b 终端：

```fish
cat .minik8s/state/cni-ipam.json 2>/dev/null; or true
docker ps -a --filter label=minik8s.pod.name=nginx-node-b --format '{{.Names}} {{.Status}}'
ip link show | grep nginx-node-b; or true
```

期望：

- node-a IPAM 文件不再包含 `default/nginx-node-a`。
- node-b IPAM 文件不再包含 `default/nginx-node-b`。
- Docker 不再有对应 sandbox/workload 容器。
- 对应 veth 不再存在；如果 veth 名称不可直接从 Pod 名推导，至少 `doctor network`
  不应报告该 Pod 的残留 allocation。

恢复状态：此 case 自带删除流程。完成后 `./kubectl get pods` 不应包含
`nginx-node-a` 或 `nginx-node-b`。

失败排查：

- IPAM key 仍存在：确认对应节点 worker 还在运行并已同步。
- 容器残留：检查 worker 删除日志；只有 worker 在线并完成同步后仍残留，才记为 runtime
  orphan GC 问题。
- veth 残留：执行 `./minik8s doctor network` 记录诊断结果，再用 `./minik8s doctor clean`
  清理本机 CNI 状态。

## CNI-06：静态 route fallback

目标：验证在 `sailer` 不负责动态网络路由同步时，也可以用 CNI 配置中的 `routes`
字段手动完成跨节点路由。

机器：node-a + node-b。

前置：

- 此 case 可选，只在排查动态 VXLAN/route 同步时使用。
- 执行前先停止 node-a/node-b 的 `sailer run`，避免动态 VXLAN route 恢复影响观察。
- 记录当前 `/etc/cni/net.d/10-mooring.conf`，便于恢复默认环境。

流程：

node-a 重新初始化 CNI：

```fish
./minik8s cni init \
  --pod-cidr 10.244.0.0/24 \
  --gateway 10.244.0.1 \
  --route 10.244.1.0/24=$NODE_B_IP
./minik8s doctor network
```

node-b 重新初始化 CNI：

```fish
./minik8s cni init \
  --pod-cidr 10.244.1.0/24 \
  --gateway 10.244.1.1 \
  --route 10.244.0.0/24=$NODE_A_IP
./minik8s doctor network
```

两台机器分别恢复默认 worker：

```fish
./minik8s sailer run
```

然后按 CNI-04 创建 Pod 并验证跨节点访问。

期望：

- `doctor network` 显示 `route: <remote PodCIDR> via <remote NodeIP>`。
- 两方向跨节点访问均成功。

恢复状态：

- 如需恢复默认 manifest CNI 路径，停止两台 worker，重新执行默认环境中的
  `sailer run`，让 worker 根据 `manifest/cni/mooring.yaml` 和节点 PodCIDR 重写 CNI 配置。
- 如需彻底清理网络状态，在两台 worker 上执行 `./minik8s doctor clean`，再回到默认环境重启。

失败排查：

- route 没写入：检查 `--route` 参数是否使用 fish 的 `$NODE_A_IP/$NODE_B_IP` 变量，
  并确认 `doctor network` 读取的是 `/etc/cni/net.d/10-mooring.conf`。
- 静态 route 仍不通：先验证同节点 CNI-03，再查宿主机三层可达、防火墙和 `ip_forward`。

## 全量恢复

如果中途停止执行，或需要在下一组 testcase 前恢复干净状态，在 node-a 执行：

```fish
./kubectl delete pod busybox-node-b; or true
./kubectl delete pod busybox-node-a; or true
./kubectl delete pod busybox-client; or true
./kubectl delete pod nginx-node-a; or true
./kubectl delete pod nginx-node-b; or true
./kubectl delete pod nginx-pod; or true
rm -f /tmp/mooring-cni-same-node.html
rm -f /tmp/mooring-cni-cross-b.html
sleep 8
./kubectl get nodes
./kubectl get pods
```

在 node-a 和 node-b 分别检查本地残留：

```fish
docker ps -a --filter label=minik8s.pod.namespace=default
cat .minik8s/state/cni-ipam.json 2>/dev/null; or true
./minik8s doctor network; or true
```

node-b 也删除跨节点测试临时文件：

```fish
rm -f /tmp/mooring-cni-cross-a.html
```

如需清理 mooring 网络设备、iptables 规则和 IPAM 状态，在每台 worker 上执行：

```fish
./minik8s doctor clean
```

全量恢复完成后，node-a/node-b 的默认 `sailer run` 应继续运行，`./kubectl get nodes`
应显示两个节点为 `Ready`。

## 快速排查索引

- `curl $HARBOR/version` 失败：先查 node-a `bridge` 是否运行、node-a 入站 `18080`
  是否放通；如果返回 502，先查 `/root/.config/fish/config.fish` 的代理环境变量，
  或直接用 `curl --noproxy '*' $HARBOR/version` 复核。
- `get nodes` 缺 node-b：检查 node-b `sailer join` 的 `--apiserver` 是否指向 node-a
  局域网 IP，token 是否有效。
- `/nodes` 为空：检查 Node YAML 是否包含 `InternalIP`，并确认 worker 已从控制面拿到
  `spec.podCIDR`。
- 跨节点 PodIP 不通：检查两台机器的 `ip link show mk8s-vxlan`、
  `bridge fdb show dev mk8s-vxlan`、`ip route | grep 10.244`，再查 UDP `4789`、
  防火墙、`ip_forward` 和 worker 日志。
- Pod 一直 `Pending`：检查对应节点 `sailer run` 是否在运行。
- Docker 容器存在但 PodIP 为空：优先看 `status.reason/message` 和
  `./minik8s doctor network`，不要只按 Docker 运行状态判断 CNI 成功。
