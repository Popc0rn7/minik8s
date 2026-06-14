# 双节点公共启动测试

本文是默认双节点环境的可执行基线。除 addon 或单节点特例外，其他 testcase 应先通过
本文，再继续执行 feature case。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| NODE-00 | 宿主机、工具和网络预检 | node-a + node-b | 不改变集群 |
| NODE-01 | bridge、CNI manifest、token 启动 | node-a | 保持 bridge 运行 |
| NODE-02 | 两个 worker join/run | node-a + node-b | 两节点 Ready |
| NODE-03 | PodCIDR、VXLAN、route 基线 | node-a + node-b | 保持默认网络 |
| NODE-04 | 旧 sailer 入口兼容性说明 | 任意 | 不作为默认验收 |

## NODE-00：环境预检

目标：确认两台机器可以互通，并具备运行 CNI/kube-proxy 的基础能力。

node-a：

```fish
ping -c 3 $NODE_B_IP
docker version
which ip
which bridge
which iptables
which nsenter
iptables-save -t nat >/tmp/minik8s-iptables-a.txt
```

node-b：

```fish
ping -c 3 $NODE_A_IP
docker version
which ip
which bridge
which iptables
which nsenter
iptables-save -t nat >/tmp/minik8s-iptables-b.txt
```

期望：

- 两台机器互 ping 成功。
- Docker client/server 正常。
- `ip`、`bridge`、`iptables`、`nsenter` 均存在。
- `iptables-save` 可用，说明网络规则检查能力正常。

失败排查：

- ping 失败时先修复局域网、防火墙或 VM 网络模式。
- Docker 失败时确认 Docker daemon 正常运行，且当前 root 环境可访问。

## NODE-01：启动控制面和 CNI manifest

目标：启动 Harbor API，启用 mooring CNI 兼容对象，并设置 worker bootstrap token。

两台机器都构建：

```fish
make prod-deploy
```

node-a 终端 1：

```fish
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24
```

node-a 测试终端：

```fish
./kubectl version
./kubectl apply -f manifest/cni/mooring.yaml
./minik8s bridge token set $MINIK8S_TOKEN --ttl 24h
curl --noproxy '*' -fsS $HARBOR/version
```

期望：

- bridge 输出 `bridge listening on :18080`。
- `kubectl version` 能访问 Harbor API。
- CNI manifest 创建或更新 `kube-mooring/mooring-cni-cfg` 和 `mooring-cni-ds`。
- token set 输出 bootstrap token 已写入。

失败排查：

- node-b 无法访问 Harbor：确认 `--listen :18080` 绑定外部可达端口，防火墙放通 TCP
  `18080`，并使用 `curl --noproxy '*'` 规避代理。
- CNI manifest apply 失败：确认当前 `kubectl` 连接的是 node-a 的 Harbor。

## NODE-02：两个 worker join/run

目标：用当前主路径注册 node-a/node-b，并启动节点本地 kubelet/CNI/proxy 循环。

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

node-a 测试终端：

```fish
./kubectl get nodes
curl --noproxy '*' -fsS $HARBOR/nodes
```

期望：

- `sailer join` 显示对应 node joined，并写入本机 `.minik8s/state/sailer.json`。
- `sailer run` 显示对应 node started。
- `get nodes` 包含 `node-a` 和 `node-b`，状态均为 `Ready`。
- `node-a` 的 PodCIDR 为 `10.244.0.0/24`，`node-b` 为 `10.244.1.0/24`。

失败排查：

- join 被拒绝：确认 token 未过期，Node YAML 的 name 和 InternalIP 正确。
- run 提示未 join：确认在同一个仓库和同一个 `MINIK8S_STATE_DIR` 下执行。
- 只看到一个节点：检查另一个 worker 的 Harbor 地址、token 和 sailer 日志。

## NODE-03：网络基线

目标：确认两个 worker 已写入 CNI 配置，并同步 VXLAN/FDB/route。

node-a：

```fish
cat /etc/cni/net.d/10-mooring.conf
./minik8s doctor network
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

node-b：

```fish
cat /etc/cni/net.d/10-mooring.conf
./minik8s doctor network
ip route | grep 10.244
ip link show mk8s-vxlan
bridge fdb show dev mk8s-vxlan
```

期望：

- 两台机器的 CNI 配置 `type` 为 `mooring`。
- node-a 配置 `podCIDR: 10.244.0.0/24`，node-b 配置 `podCIDR: 10.244.1.0/24`。
- 两台机器均存在 `mk8s-vxlan`。
- node-a 有到 `10.244.1.0/24` 的 route，node-b 有到 `10.244.0.0/24` 的 route。
- FDB 指向对端 Node IP。

失败排查：

- 无 VXLAN 或 route：检查两台节点的 `InternalIP`、`curl --noproxy '*' -fsS $HARBOR/nodes`、
  UDP `4789`、宿主机防火墙和 `ip_forward`。
- PodCIDR 为空：确认 `bridge` 启动时包含 `--cluster-cidr` 和
  `--node-cidr-mask-size 24`，然后重新 `sailer join`。

## NODE-04：旧入口兼容性

旧命令 `./minik8s sailer manifest/node/node_a.yaml --harbor ...` 仍可能在代码里保留为
兼容路径，但默认 testcase 不使用它。人工验收统一使用：

```fish
./minik8s sailer join --apiserver http://$NODE_A_IP:18080 --token $MINIK8S_TOKEN -f manifest/node/node_a.yaml
./minik8s sailer run
```

只有在验证历史兼容性或排查旧文档时，才记录旧入口输出；不要把旧入口混入默认启动流程。

## 恢复

如果只结束 testcase，先删除业务对象，再停止两个 `sailer run`。如果要清理本机网络状态，
在 node-a 和 node-b 分别执行：

```fish
./minik8s doctor network; or true
./minik8s doctor clean; or true
./minik8s doctor network; or true
```

如需继续运行 testcase，重新执行 NODE-02，让 worker 重新写入 CNI 配置和心跳状态。
