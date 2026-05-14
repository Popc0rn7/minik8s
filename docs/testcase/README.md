# v0.1.0 Testcase 总入口

本目录记录 Minik8s v0.1.0 的验收 case。v0.1.0 的稳定边界是：Pod 生命周期、双 worker 心跳与调度、单/双节点 CNI、Service endpoints、iptables ClusterIP/NodePort、etcd Pod/Service 持久化。不覆盖 ReplicaSet、HPA、DNS、Serverless、SecurityContext、GPU。

## 默认双机拓扑

| 角色 | 节点名 | 运行组件 | PodCIDR | 说明 |
| --- | --- | --- | --- | --- |
| 控制面 + worker | `node-a` | `kubebridge`、`kubesailer` | `10.244.0.0/24` | API server、网络注册表与 etcd 推荐放这里 |
| worker | `node-b` | `kubesailer` | `10.244.1.0/24` | 只访问 Kubeharbor，不直连 etcd |

默认端口：

- Kubeharbor: `18080`
- nginx hostPort case: `8080`
- Service NodePort: `30080`

两台机器都需要 Linux、Docker、`ip`、`iptables`、`nsenter`、`curl` 或 `wget`，并以 root 用户执行测试命令。`10.244.0.0/16` 不应与局域网或宿主机路由冲突。

## 全局变量

在 node-a：

```bash
export NODE_A_IP=192.168.1.8
export NODE_B_IP=192.168.1.6
export POD_CIDR_A=10.244.0.0/24
export POD_CIDR_B=10.244.1.0/24
export KUBEHARBOR=http://${NODE_A_IP}:18080
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
```

在 node-b：

```bash
export NODE_A_IP=192.168.1.8
export NODE_B_IP=192.168.1.6
export POD_CIDR_A=10.244.0.0/24
export POD_CIDR_B=10.244.1.0/24
export KUBEHARBOR=http://${NODE_A_IP}:18080
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
```

## 必测矩阵

| Case | 文档 | 目标 | 必跑 |
| --- | --- | --- | --- |
| CASE-00 | `two-node.md` | 双机环境预检、构建、启动控制面和 worker | 是 |
| CASE-01 | `pod.md` | Pod apply/get/delete、restartPolicy、双 worker 心跳调度 | 是 |
| CASE-02 | `cni.md` | 单节点 PodIP、跨节点 PodIP 通信、IPAM 清理 | 是 |
| CASE-03 | `service.md` | endpoints、ClusterIP、NodePort、多 endpoint、iptables 清理 | 是 |
| CASE-04 | `etcd.md` | Pod/Service 写入 etcd，kubebridge 重启后恢复 | 是 |

建议执行顺序：

```bash
# 1. 两台机器按 two-node.md 完成启动。
# 2. 在 node-a 执行 pod.md。
# 3. 在 node-a 和 node-b 按 cni.md 验证跨节点。
# 4. 在 node-a 执行 service.md，必要时在 node-b 辅助 curl NodePort。
# 5. 需要持久化验收时执行 etcd.md。
```

## 清理顺序

在 node-a 统一清理 API 对象：

```bash
./minik8s delete service nginx-service || true
./minik8s delete service nginx-nodeport || true
./minik8s delete pod nginx-node-a || true
./minik8s delete pod nginx-node-b || true
./minik8s delete pod busybox-node-b || true
./minik8s delete pod busybox-client || true
./minik8s delete pod nginx-pod-2 || true
./minik8s delete pod nginx-pod || true
./minik8s delete pod volume-resource-pod -n demo || true
```

等待两个 kubesailer 各同步一次后，在两台机器检查：

```bash
docker ps -a --filter label=minik8s.pod.namespace=default
cat .minik8s/state/cni-ipam.json 2>/dev/null || true
iptables-save -t nat | grep MK8S-SVC || true
```

如需完全重置测试状态，停止 `kubebridge`、`kubesailer` 后再删除 `.minik8s/testcase-state`、`.minik8s/state/cni-ipam.json`，并清理残留 Docker 容器。

## 常见故障定位

- `get nodes` 只有一个节点：检查 node-b 的 `KUBEHARBOR` 是否指向 node-a 局域网 IP，而不是 `127.0.0.1`。
- Pod 一直 `Pending`：检查对应 `nodeName` 的 kubesailer 是否在运行，或执行 `./minik8s get nodes` 看节点心跳。
- Pod 没有 IP：检查 `MINIK8S_CNI_DISABLED` 是否被设置为 `1`，以及 `doctor network` 的 CNI conf/bin 路径。
- 跨节点 PodIP 不通：检查 `kubesailer` 是否带了 `--node-ip` 和 `--pod-cidr`，`ip route` 是否有对端 PodCIDR，宿主机防火墙是否拦截转发。
- Service ClusterIP 不通：检查 `kubebridge` 是否启用了默认 ServiceProxy，`iptables-save -t nat | grep MK8S-SVC` 是否有规则。
- NodePort 不通：确认访问的是运行了 proxy 规则的节点 IP，且宿主机防火墙允许 `30080`。
