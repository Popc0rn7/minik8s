# CNI CLI 测试用例与实现映射

本文档覆盖 Handout 中 “实现 CNI 功能，支持 Pod 间通信” 的要求。所有 case 使用的 YAML 均放在 `manifest/testdata/`。

## 0. 前置准备

CNI bridge 插件需要操作 Linux network namespace、veth、bridge、iptables，通常需要 root 权限或等价能力。以下命令在仓库根目录执行：

```bash
go build -o minik8s ./cmd/minik8s
unset MINIK8S_CNI_DISABLED
./minik8s cni init
go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
./minik8s doctor network
./minik8s kubecaptain --listen :8080
sudo ./minik8s kubelet --node-name node-a --apiserver http://127.0.0.1:8080
```

期望 `doctor network` 输出：

- `config: present`
- `minik8s-bridge: present`
- `plugin: minik8s-bridge`

如果需要隔离本次 case 的状态：

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
rm -rf .minik8s/testcase-state .minik8s/state/cni-ipam.json
```

## 1. 需求追踪矩阵

| Handout 要求 | 验证 case | Manifest | 主要命令 | 当前状态 |
| --- | --- | --- | --- | --- |
| Pod 启动时分配独立内网 IP | CNI-01 | `pod_nginx.yaml`、`pod_busybox_client.yaml` | `apply`、`kubelet`、`get pods` | 已实现，可验证 |
| Pod IP 写入可视化输出 | CNI-01 | 同上 | `get pods` | 已实现，可验证 |
| 同节点 Pod 通过 CNI IP 直接通信 | CNI-02 | 同上 | client Pod 内 `wget serverIP` | 已实现，需 root/网络环境 |
| 删除 Pod 时释放 CNI 状态 | CNI-03 | 同上 | `delete pod`、检查 IPAM 状态 | 已实现，可验证 |
| 跨节点 Pod 通过 CNI 通信 | CNI-04 | 同上 | 多节点 route 配置 | 代码有 route 字段，完整多机流程需补齐 |

## 2. Case CNI-01：初始化 CNI 并为 Pod 分配 IP

目标：验证 `cni init` 生成配置，Pod 启动时通过 CNI bridge 获得独立 Pod IP，并在 `get pods` 中展示。

Manifest：

- `manifest/testdata/pod_nginx.yaml`
- `manifest/testdata/pod_busybox_client.yaml`

流程：

```bash
./minik8s cni init
go build -o .minik8s/cni/bin/minik8s-bridge ./cmd/minik8s-bridge
./minik8s doctor network
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/pod_busybox_client.yaml
./minik8s get pods
cat .minik8s/state/cni-ipam.json
```

期望：

- `doctor network` 显示配置和插件均存在。
- `apply` 后两个 Pod 进入 `Pending`，kubelet 同步后均为 `Running`。
- `get pods` 的 `IP` 列中，`nginx-pod` 和 `busybox-client` 均显示 `10.244.0.0/24` 内的不同 IP，通常从 `10.244.0.2` 开始。
- `.minik8s/state/cni-ipam.json` 中有 `default/nginx-pod` 和 `default/busybox-client` 两个 allocation。

清理：

```bash
./minik8s delete pod busybox-client || true
./minik8s delete pod nginx-pod || true
```

## 3. Case CNI-02：验证同节点 Pod 间通过 Pod IP 通信

目标：验证 client Pod 可以直接访问 server Pod 的 CNI IP，而不是 hostPort 或宿主机端口。

Manifest：

- `manifest/testdata/pod_nginx.yaml`
- `manifest/testdata/pod_busybox_client.yaml`

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/pod_busybox_client.yaml
./minik8s get pods
SERVER_IP=$(./minik8s get pods | awk '/nginx-pod/ {print $(NF-3)}')
CLIENT_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker exec "$CLIENT_CID" wget -qO- "http://${SERVER_IP}:80" >/tmp/minik8s-cni-response.html
head -n 1 /tmp/minik8s-cni-response.html
```

期望：

- `SERVER_IP` 是 `10.244.0.0/24` 内地址，不是 `-`。
- `docker exec` 在 client 容器内访问 `http://SERVER_IP:80` 成功。
- 响应内容为 nginx 默认 HTML。

清理：

```bash
./minik8s delete pod busybox-client || true
./minik8s delete pod nginx-pod || true
```

## 4. Case CNI-03：验证 Pod 删除时释放 CNI 状态

目标：验证删除 Pod 时 controller 调用 CNI `DEL`，释放 IPAM 分配并清理 runtime 资源。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
cat .minik8s/state/cni-ipam.json
./minik8s delete pod nginx-pod
cat .minik8s/state/cni-ipam.json
docker ps -a --filter label=minik8s.pod.name=nginx-pod
```

期望：

- 创建后 IPAM 文件包含 `default/nginx-pod`。
- 删除后 IPAM 文件不再包含 `default/nginx-pod`。
- Docker 不再有 `nginx-pod` 对应的 sandbox/workload 容器。

## 5. Case CNI-04：跨节点通信能力现状记录

Handout 要求 “同节点或跨节点” Pod 都能通过 CNI 直接通信。当前代码中的 `internal/cniplugin/bridge.go` 支持在 CNI 配置中读取：

```json
"routes": [
  {"dst": "10.244.1.0/24", "gw": "192.168.1.11"}
]
```

插件在 `ADD` 时会执行宿主机 route replace，用于把远端 PodCIDR 指向远端节点网关。因此设计上已经为 host-gw 风格跨节点路由留出了配置入口。

当前缺口：

- `minik8s cni init` 只生成单节点默认配置：`podCIDR=10.244.0.0/24`、`gateway=10.244.0.1`。
- 代码中还没有 Node 抽象、节点 PodCIDR 分配、跨节点 route 自动下发流程。
- 因此跨节点通信无法仅通过当前 CLI 完整复现，需要后续多机 Node/Scheduler 功能补齐后再沉淀自动化 case。

建议的后续验收 case：

```bash
# node-a: podCIDR 10.244.0.0/24, gateway 10.244.0.1
# node-b: podCIDR 10.244.1.0/24, gateway 10.244.1.1
# 两边 CNI config 都包含对端 routes。
./minik8s apply -f manifest/testdata/pod_nginx.yaml
./minik8s apply -f manifest/testdata/pod_busybox_client.yaml
docker exec "$CLIENT_CID" wget -qO- "http://${REMOTE_SERVER_IP}:80"
```

期望：跨节点 client Pod 可以直接访问 remote server Pod IP。

## 6. 实现设计映射

CLI 初始化：`internal/cli/cli.go` 的 `cni init` 创建 `.minik8s/cni/net.d/10-minik8s.conf`，默认使用 `minik8s-bridge`、bridge `mk8s0`、Pod CIDR `10.244.0.0/24`、gateway `10.244.0.1`。

CNI 发现：`defaultNetworkManager` 检测 CNI conf dir 后创建 `internal/cni.Runner`。如设置 `MINIK8S_CNI_DISABLED=1` 或配置不存在，则 Pod 回退为不启用 CNI 的普通 Docker 网络行为。

CNI 调用：`internal/cni/runner.go` 读取第一个 `.conf/.conflist/.json`，解析插件 type，执行插件二进制并设置 `CNI_COMMAND`、`CNI_CONTAINERID`、`CNI_NETNS`、`CNI_IFNAME`、`K8S_POD_NAME`、`K8S_POD_NAMESPACE`。

Pod 接入点：`internal/kubecaptain/controller/pod_controller.go` 创建并启动 sandbox 后，通过 runtime 获取 sandbox netns 路径，再调用 `network.Add`。成功后把 `PodIP` 和原始 CNI result 写入 Pod status。

Bridge 插件：`internal/cniplugin/bridge.go` 创建/复用 `mk8s0`，创建 veth pair，将一端放入 Pod netns，配置 Pod IP、默认路由和宿主机 NAT。

IPAM：`internal/cniplugin/ipam.go` 使用 `.minik8s/state/cni-ipam.json` 持久化 Pod IP allocation；key 优先使用 `namespace/name`，保证同一 Pod 重建时可获得稳定 IP。

删除路径：`delete pod` 删除控制面期望状态；下一次 kubelet 同步发现本节点 Pod 消失后调用 controller `DeletePod`，先执行 `network.Del` 删除 veth 并释放 IPAM，再停止并删除 sandbox。
