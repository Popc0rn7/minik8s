# Pod 测试用例

本文档覆盖 v0.1.0 的 Pod 生命周期、状态展示、资源映射、重启策略、双 worker 心跳调度，以及 NodeLost 时 Pod 状态级联更新。本文档可作为 `docs/testcase` 重构后的首个独立入口：每个 case 都写明前置，并在结束后恢复到可继续执行下一个 case 的状态。

## 测试拓扑

| 角色 | 节点名 | 运行组件 | PodCIDR | 说明 |
| --- | --- | --- | --- | --- |
| 控制面 + worker | `node-a` | `bridge`、`sailer` | `10.244.0.0/24` | API server、网络注册表与 etcd 推荐放这里 |
| worker | `node-b` | `sailer` | `10.244.1.0/24` | 只访问 Harbor，不直连 etcd |

默认端口：

- Harbor: `18080`
- nginx hostPort case: `8080`

两台机器都需要 Linux、Docker、`ip`、`iptables`、`nsenter`、`curl` 或 `wget`，并以 root 用户执行测试命令。`10.244.0.0/16` 不应与局域网或宿主机路由冲突。

## 公共前置

在 node-a 和 node-b 的所有测试终端先设置变量；按实际局域网地址替换 `NODE_A_IP`、`NODE_B_IP`。

```bash
export NODE_A_IP=192.168.1.8
export NODE_B_IP=192.168.1.6
export CLUSTER_CIDR=10.244.0.0/16
export HARBOR=http://${NODE_A_IP}:18080
export MINIK8S_HARBOR=${HARBOR}
```

确认 `manifest/node/node_a.yaml` 和 `manifest/node/node_b.yaml` 中的 `InternalIP` 与当前两台机器一致；如果不同，先按实际地址更新这两个 Node YAML。Node YAML 不需要写 `spec.podCIDR`，控制面会从 `${CLUSTER_CIDR}` 自动分配。

在两台机器的仓库根目录构建二进制：

```bash
make build
```

在 node-a 启动控制面。后续 CLI 命令默认在 node-a 的仓库根目录执行。

```bash
export MINIK8S_STATE_DIR=.minik8s/testcase-state
export MINIK8S_HARBOR=${HARBOR}
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr ${CLUSTER_CIDR} \
  --node-cidr-mask-size 24
```

在 node-a 的另一个终端验证控制面可用：

```bash
./minik8s version --server ${HARBOR}
```

除 `POD-01` 外，本文默认两台机器分别启动启用 CNI 的 sailer。sailer 会先注册 Node，等待控制面分配 `spec.podCIDR`，再自动写入本机 CNI 配置。

node-a：

```bash
unset MINIK8S_CNI_DISABLED
./minik8s sailer \
  manifest/node/node_a.yaml \
  --harbor ${HARBOR}
./minik8s doctor network
```

node-b：

```bash
unset MINIK8S_CNI_DISABLED
./minik8s sailer \
  manifest/node/node_b.yaml \
  --harbor ${HARBOR}
./minik8s doctor network
```

在 node-a 验证两个节点都已注册：

```bash
./minik8s get nodes
```

期望输出包含 `node-a` 和 `node-b`，两个节点状态均为 `Ready`，并显示控制面分配的 PodCIDR：`node-a` 为 `10.244.0.0/24`，`node-b` 为 `10.244.1.0/24`。

## 覆盖矩阵

| Case | 目标 | 机器 | 状态恢复要求 |
| --- | --- | --- | --- |
| POD-01 | 无 CNI 基础 Pod、hostPort、delete 清理 | node-a | 删除 Pod，恢复 node-a 启用 CNI 的 sailer |
| POD-02 | hostPath volume 与 CPU/memory limit | node-a，必要时 node-b 辅助检查 | 删除 Pod，删除 marker 文件 |
| POD-03 | restartPolicy 崩溃重启 | node-a | 删除 Pod |
| POD-04 | 双 worker 心跳与未指定 nodeName 的调度 | node-a + node-b | 删除 Pod，保持两个节点 Ready |
| POD-05 | NodeLost 后 Pod Unknown 与 Service endpoint 移除 | node-a + node-b | 重启 node-b sailer，删除 Pod 和 Service |
| POD-06 | mock runtime 失败原因回写 | 任意开发机 | 无集群状态变更 |

## POD-01：基础 Pod 生命周期

目标：验证 `kind: Pod`、metadata、container image/tag、command/args、hostPort、`apply/get/delete`。

机器：node-a。此 case 使用 hostPort，建议临时禁用 CNI，避免已有 CNI 配置影响 Docker hostPort 行为。此 case 仍然需要 node-a 的 `bridge` 运行，但不需要 node-b。

前置：停止 node-a 上当前启用 CNI 的 sailer，然后在 node-a 的单独终端临时启动禁用 CNI 的 sailer。

```bash
export MINIK8S_HARBOR=${HARBOR}
export MINIK8S_CNI_DISABLED=1
./minik8s sailer \
  manifest/node/node_a.yaml \
  --harbor ${HARBOR}
```

流程：

```bash
./minik8s delete pod nginx-pod || true
./minik8s apply -f manifest/pod/pod_nginx.yaml
./minik8s get pods
curl -fsS http://127.0.0.1:8080 >/tmp/minik8s-nginx.html
docker ps --filter label=minik8s.pod.name=nginx-pod
docker inspect nginx-pod-nginx --format '{{json .Config.Image}} {{json .Config.Entrypoint}} {{json .Config.Cmd}}'
./minik8s delete pod nginx-pod
sleep 6
docker ps -a --filter label=minik8s.pod.name=nginx-pod --format '{{.Names}} {{.Status}}'
```

期望：

- `apply` 输出 `pod/nginx-pod created`。
- `get pods` 包含 `nginx-pod`、`Running`、`default`、`app=nginx`。
- `curl` 返回 nginx 页面。
- Docker 中能看到 sandbox 和 workload 容器。
- 删除后不再有 `nginx-pod` 对应容器。

恢复状态：

```bash
./minik8s delete pod nginx-pod || true
rm -f /tmp/minik8s-nginx.html
```

停止 node-a 临时禁用 CNI 的 sailer，然后在 node-a sailer 终端恢复启用 CNI 的模式：

```bash
unset MINIK8S_CNI_DISABLED
./minik8s sailer \
  manifest/node/node_a.yaml \
  --harbor ${HARBOR}
```

确认 node-a 回到 Ready 后再继续后续 case：

```bash
./minik8s get nodes
```

失败排查：

- `curl 127.0.0.1:8080` 失败：确认 node-a 的临时 sailer 正在运行，且没有其他进程占用 `8080`。
- 删除后容器仍在：等待 sailer 下一轮同步，或临时执行 `./minik8s sailer manifest/node/node_a.yaml --harbor ${HARBOR} --once`。

## POD-02：volume 与资源限制

目标：验证 `hostPath` volume、`volumeMounts`、CPU/memory limit、namespace 过滤。Pod YAML 不指定 `nodeName`，实际运行节点由 scheduler 决定。

机器：node-a 执行 CLI。Pod 可能被调度到 node-a 或 node-b；marker 文件和 Docker inspect 需要在实际运行节点检查。

前置：node-a/node-b 的启用 CNI sailer 均在运行，`./minik8s get nodes` 能看到两个节点为 `Ready`。

```bash
./minik8s delete pod volume-resource-pod -n demo || true
mkdir -p /tmp/minik8s-case-data
rm -f /tmp/minik8s-case-data/marker
```

流程：

```bash
./minik8s apply -f manifest/pod/pod_volume_resource.yaml
sleep 6
./minik8s get pods -n demo
./minik8s describe pod volume-resource-pod -n demo
```

根据 `describe pod` 中的 Node，到实际运行节点执行宿主机检查：

```bash
cat /tmp/minik8s-case-data/marker
docker inspect volume-resource-pod-writer --format '{{json .HostConfig.Mounts}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}'
```

期望：

- `get pods -n demo` 包含 `volume-resource-pod`、`Running`、`demo`。
- `describe pod` 中 Node 不为空，说明 scheduler 已完成分配。
- 如果 Pod 被调度到 node-a，`/tmp/minik8s-case-data/marker` 内容为 `volume-ok`；如果被调度到 node-b，到 node-b 执行同一路径检查。
- Docker inspect 显示目标挂载 `/data`。
- `NanoCpus` 约为 `500000000`，`Memory` 约为 `134217728`。

恢复状态：

```bash
./minik8s delete pod volume-resource-pod -n demo || true
rm -f /tmp/minik8s-case-data/marker
sleep 6
docker ps -a --filter label=minik8s.pod.name=volume-resource-pod --format '{{.Names}} {{.Status}}'
```

如果 Pod 被调度到 node-b，也在 node-b 执行 marker 和 Docker 残留检查：

```bash
rm -f /tmp/minik8s-case-data/marker
docker ps -a --filter label=minik8s.pod.name=volume-resource-pod --format '{{.Names}} {{.Status}}'
```

失败排查：

- marker 文件不存在：确认容器已 Running，再检查实际运行节点上的 hostPath 目录权限。
- 资源值为 0：确认使用的是 workload 容器 `volume-resource-pod-writer`，不是 sandbox。

## POD-03：崩溃后重启

目标：验证 `restartPolicy: Always` 下容器退出后，sailer 下一轮同步会重启同一容器。

机器：node-a 执行 CLI。Pod 可能被调度到 node-a 或 node-b；`docker kill` 与 `docker inspect` 需要在实际运行节点执行。

前置：node-a/node-b 的启用 CNI sailer 均在运行，`./minik8s get nodes` 能看到两个节点为 `Ready`。

```bash
./minik8s delete pod busybox-client || true
```

流程：

```bash
./minik8s apply -f manifest/pod/pod_busybox_client.yaml
sleep 6
./minik8s describe pod busybox-client
```

根据 `describe pod` 中的 Node，到实际运行节点执行重启检查：

```bash
CLIENT_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker kill "${CLIENT_CID}"
docker inspect "${CLIENT_CID}" --format '{{.State.Status}}'
sleep 6
docker inspect "${CLIENT_CID}" --format '{{.State.Status}}'
./minik8s get pods
```

期望：

- kill 后第一次 inspect 显示 `exited`。
- 等待 sailer 同步后 inspect 显示 `running`。
- `get pods` 中 `busybox-client` 仍为 `Running`。

恢复状态：

```bash
./minik8s delete pod busybox-client || true
sleep 6
docker ps -a --filter label=minik8s.pod.name=busybox-client --format '{{.Names}} {{.Status}}'
```

最后一条 Docker 检查应在 Pod 实际运行节点执行；如果 Pod 被调度到 node-b，也在 node-b 执行。

失败排查：

- 容器没有重启：确认实际运行节点上的 sailer 没退出，并检查 Pod 的 `restartPolicy`。
- `CLIENT_CID` 为空：先通过 `describe pod busybox-client` 确认 Pod 被调度到哪个节点，再到该节点执行 Docker 命令。

## POD-04：双 worker 心跳与调度

目标：验证 node-a、node-b 都能注册为 Ready；未指定 `spec.nodeName` 的 Pod 会在节点心跳后被 Navigator 分配。

机器：node-a 执行 CLI；node-a/node-b 的 sailer 都需运行。

前置：node-a/node-b 均启动启用 CNI 的 sailer，且 node-a 已退出 `POD-01` 使用的 `MINIK8S_CNI_DISABLED=1` 临时模式。

```bash
./minik8s delete pod nginx-pod-2 || true
./minik8s get nodes
```

流程：

```bash
./minik8s apply -f manifest/pod/pod_nginx_service_peer.yaml
sleep 6
./minik8s get pod nginx-pod-2 -o yaml
./minik8s describe pod nginx-pod-2
```

期望：

- `get nodes` 显示 `node-a` 和 `node-b`，状态为 `Ready`。
- `nginx-pod-2` 的 `spec.nodeName` 不为空，且为 `node-a` 或 `node-b`。
- Pod 最终进入 `Running` 并拥有 PodIP。

恢复状态：

```bash
./minik8s delete pod nginx-pod-2 || true
sleep 6
./minik8s get nodes
docker ps -a --filter label=minik8s.pod.name=nginx-pod-2 --format '{{.Names}} {{.Status}}'
```

如果 Pod 被调度到 node-b，也在 node-b 执行最后一条 Docker 检查。恢复完成后，两个节点应仍为 `Ready`。

失败排查：

- `spec.nodeName` 为空：确认至少一个 sailer 正在心跳，默认 TTL 为 30s；用户 YAML 中不应指定 `nodeName`。
- Pod 被分到 node-b 但没 Running：到 node-b 查看 sailer 日志和 Docker 状态。

## POD-05：NodeLost 状态级联

目标：验证 node-b heartbeat 超时后，控制面将该节点上的非终态 Pod 从 `Running` 标为 `Unknown`，写入 `reason: NodeLost`，并从匹配的 Service endpoints 中移除该 PodIP。

机器：node-a 执行 CLI；node-b 需要先运行 sailer，再在流程中手动停止。

前置：node-a/node-b 均启动启用 CNI 的 sailer，且 `./minik8s get nodes` 能看到两个节点为 `Ready`。

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-node-b || true
./minik8s get nodes
```

流程：

```bash
./minik8s apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./minik8s apply -f manifest/service/service_clusterip_nginx.yaml
sleep 6
./minik8s get pod nginx-node-b -o yaml
./minik8s describe service nginx-service
```

在 node-b 的 sailer 终端按 `Ctrl-C` 停止 sailer，等待超过默认 Node TTL：

```bash
sleep 35
./minik8s get nodes
./minik8s get pod nginx-node-b -o yaml
./minik8s describe service nginx-service
```

期望：

- 停止 node-b sailer 前，`nginx-node-b` 为 `Running`，有非空 `podIP`。
- 停止 node-b sailer 前，`nginx-service` endpoints 包含 `nginx-node-b` 对应 PodIP。
- `get nodes` 触发 liveness refresh 后，`node-b` 状态为 `Unknown`。
- `nginx-node-b` 的 `status.phase` 为 `Unknown`，`status.reason` 为 `NodeLost`。
- `nginx-node-b` 的 `status.podIP` 仍保留，用于诊断。
- `nginx-service` endpoints 不再包含 `nginx-node-b` 的 PodIP；如果没有其他 Running 且 label `app=nginx` 的 Pod，endpoints 应为空。

恢复状态：先在 node-b 重新启动启用 CNI 的 sailer，使节点重新心跳。

```bash
unset MINIK8S_CNI_DISABLED
./minik8s sailer \
  manifest/node/node_b.yaml \
  --harbor ${HARBOR}
```

然后在 node-a 删除测试对象并确认节点恢复：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-node-b || true
sleep 6
./minik8s get nodes
./minik8s describe service nginx-service || true
```

如果 node-b 上仍有残留容器，在 node-b 检查：

```bash
docker ps -a --filter label=minik8s.pod.name=nginx-node-b --format '{{.Names}} {{.Status}}'
```

失败排查：

- Pod 仍为 `Running`：确认等待时间超过 Node TTL，且执行过 `./minik8s get nodes` 或 node liveness loop 正在运行。
- Service endpoint 仍包含 node-b PodIP：确认使用的是包含 NodeLost 级联修复后的 `bridge`，并等待一次 service sync。
- node-b 重新出现 Ready：确认 node-b sailer 已停止，且没有 systemd/supervisor 自动拉起。

## POD-06：失败原因回写

目标：用 mock runtime 稳定验证 sandbox/container 创建失败时 Pod 进入 Failed，并写入 reason。

机器：任意开发机。

前置：不依赖双机集群；只需要本地 Go 测试环境可用。

流程：

```bash
go test ./internal/sailer -run 'SandboxCreationFailure|SyncOnce' -count=1 -v
```

期望：

- sailer 测试断言 Pod 进入 `Failed`。
- sailer 测试断言只处理本节点 assigned Pods，并回写 status。

恢复状态：此 case 不创建集群对象，无需恢复。

失败排查：

- 若测试失败，优先查看 PodController 的状态转换和 Sailer 的 status API 回写逻辑。

## 全量恢复

如果中途停止执行，或需要在下一组 testcase 前恢复干净状态，在 node-a 执行：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-node-b || true
./minik8s delete pod nginx-pod-2 || true
./minik8s delete pod busybox-client || true
./minik8s delete pod volume-resource-pod -n demo || true
./minik8s delete pod nginx-pod || true
rm -f /tmp/minik8s-nginx.html
rm -f /tmp/minik8s-case-data/marker
sleep 6
./minik8s get nodes
./minik8s get pods
```

在 node-a 和 node-b 分别检查残留容器：

```bash
docker ps -a --filter label=minik8s.pod.namespace=default
docker ps -a --filter label=minik8s.pod.namespace=demo
```

全量恢复完成后，node-a/node-b 的启用 CNI sailer 应继续运行，`./minik8s get nodes` 应显示两个节点为 `Ready`。
