# Pod 测试用例

本文档覆盖 v0.1.0 的 Pod 生命周期、状态展示、资源映射、重启策略、双 worker 心跳调度，以及 NodeLost 时 Pod 状态级联更新。双机公共启动流程见 `docs/testcase/two-node.md`。

## 公共前置

在 node-a 的测试终端设置一次公共变量。后续命令默认在仓库根目录执行，并复用这些环境变量；如果另开 daemon 终端，也先执行同一组变量。

```bash
export MINIK8S_HARBOR=${HARBOR}
```

确认控制面已在 node-a 运行；`bridge` 是 `apply/get/delete` 和 `sailer` 拉取 Pod 的 API 入口，不能省略。

```bash
./minik8s version --server ${HARBOR}
```

如果从 `two-node.md` 连续执行到本文档，通常已经有 node-a/node-b 的 CNI 和 sailer。`POD-01` 会临时停掉 node-a 当前 sailer，并以禁用 CNI 的方式重启 node-a sailer；`POD-02` 到 `POD-05` 再恢复启用 CNI 的 sailer。

本文默认以 root 用户执行测试命令；另开 daemon 终端时，按对应步骤重新设置需要的环境变量。

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| POD-01 | 无 CNI 基础 Pod、hostPort、delete 清理 | node-a | 是 |
| POD-02 | hostPath volume 与 CPU/memory limit | node-a | 是 |
| POD-03 | restartPolicy 崩溃重启 | node-a | 是 |
| POD-04 | 双 worker 心跳与未指定 nodeName 的调度 | node-a + node-b | 是 |
| POD-05 | NodeLost 后 Pod Unknown 与 Service endpoint 移除 | node-a + node-b | 是 |
| POD-06 | mock runtime 失败原因回写 | 任意开发机 | 是 |

## POD-01：基础 Pod 生命周期

目标：验证 `kind: Pod`、metadata、container image/tag、command/args、hostPort、`apply/get/delete`。

机器：node-a。此 case 使用 hostPort，建议临时禁用 CNI，避免已有 CNI 配置影响 Docker hostPort 行为。此 case 仍然需要 node-a 的 `bridge` 运行，但不需要 node-b。

前置：停止 node-a 上当前 sailer，临时在 node-a 的单独终端用禁用 CNI 的环境重新启动一个 sailer。

```bash
export MINIK8S_CNI_DISABLED=1
./minik8s sailer \
  --node-name node-a \
  --harbor ${HARBOR}
```

流程：

```bash
./minik8s apply -f manifest/testdata/pod_nginx.yaml
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

失败排查：

- `curl 127.0.0.1:8080` 失败：确认 node-a 的 sailer 正在运行，且没有其他进程占用 8080。
- 删除后容器仍在：等待 sailer 下一轮同步，或临时执行 `./minik8s sailer --node-name node-a --harbor ${HARBOR} --once`。

清理：

```bash
./minik8s delete pod nginx-pod || true
```

清理后停止这个临时 sailer，并在 node-a sailer 终端恢复 CNI：

```bash
unset MINIK8S_CNI_DISABLED
./minik8s sailer \
  --node-name node-a \
  --harbor ${HARBOR}
```

确认 node-a 回到 Ready 后再继续后续 case：

```bash
./minik8s get nodes
```

## POD-02：volume 与资源限制

目标：验证 `hostPath` volume、`volumeMounts`、CPU/memory limit、namespace 过滤。Pod YAML 不指定 `nodeName`，实际运行节点由 scheduler 决定。

机器：node-a。

流程：

```bash
mkdir -p /tmp/minik8s-case-data
rm -f /tmp/minik8s-case-data/marker
./minik8s apply -f manifest/testdata/pod_volume_resource.yaml
sleep 6
./minik8s get pods -n demo
./minik8s describe pod volume-resource-pod -n demo
cat /tmp/minik8s-case-data/marker
docker inspect volume-resource-pod-writer --format '{{json .HostConfig.Mounts}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}'
```

期望：

- `get pods -n demo` 包含 `volume-resource-pod`、`Running`、`demo`。
- `describe pod` 中 Node 不为空，说明 scheduler 已完成分配。
- 如果 Pod 被调度到 node-a，`/tmp/minik8s-case-data/marker` 内容为 `volume-ok`；如果被调度到 node-b，到 node-b 执行同一路径检查。
- Docker inspect 显示目标挂载 `/data`。
- `NanoCpus` 约为 `500000000`，`Memory` 约为 `134217728`。

失败排查：

- marker 文件不存在：确认容器已 Running，再检查 hostPath 目录权限。
- 资源值为 0：确认使用的是 workload 容器 `volume-resource-pod-writer`，不是 sandbox。

清理：

```bash
./minik8s delete pod volume-resource-pod -n demo || true
rm -f /tmp/minik8s-case-data/marker
```

## POD-03：崩溃后重启

目标：验证 `restartPolicy: Always` 下容器退出后，sailer 下一轮同步会重启同一容器。

机器：node-a。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_busybox_client.yaml
sleep 6
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

失败排查：

- 容器没有重启：确认 sailer 没退出，并检查 Pod 的 `restartPolicy`。

清理：

```bash
./minik8s delete pod busybox-client || true
```

## POD-04：双 worker 心跳与调度

目标：验证 node-a、node-b 都能注册为 Ready；未指定 `spec.nodeName` 的 Pod 会在节点心跳后被 Navigator 分配。

机器：node-a 执行 CLI；node-a/node-b 的 sailer 都需运行。

前置：node-a/node-b 均按 `two-node.md` 启动启用 CNI 的 sailer，且 node-a 已退出 `POD-01` 使用的 `MINIK8S_CNI_DISABLED=1` 临时模式。

流程：

```bash
./minik8s get nodes
./minik8s apply -f manifest/testdata/pod_nginx_service_peer.yaml
sleep 6
./minik8s get pod nginx-pod-2 -o yaml
./minik8s describe pod nginx-pod-2
```

期望：

- `get nodes` 显示 `node-a` 和 `node-b`，状态为 `Ready`。
- `nginx-pod-2` 的 `spec.nodeName` 不为空，且为 `node-a` 或 `node-b`。
- Pod 最终进入 `Running` 并拥有 PodIP。

失败排查：

- `spec.nodeName` 为空：确认至少一个 sailer 正在心跳，默认 TTL 为 30s；用户 YAML 中不应指定 `nodeName`。
- Pod 被分到 node-b 但没 Running：到 node-b 查看 sailer 日志和 Docker 状态。

清理：

```bash
./minik8s delete pod nginx-pod-2 || true
```

## POD-05：NodeLost 状态级联

目标：验证 node-b heartbeat 超时后，控制面将该节点上的非终态 Pod 从 `Running` 标为 `Unknown`，写入 `reason: NodeLost`，并从匹配的 Service endpoints 中移除该 PodIP。

机器：node-a 执行 CLI；node-b 需要先运行 sailer，再在流程中手动停止。

前置：node-a/node-b 均按 `two-node.md` 启动启用 CNI 的 sailer，且 `./minik8s get nodes` 能看到两个节点为 `Ready`。

流程：

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-node-b || true

./minik8s apply -f manifest/testdata/pod_nginx_node_b.yaml
sleep 8
./minik8s apply -f manifest/testdata/service_clusterip_nginx.yaml
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

失败排查：

- Pod 仍为 `Running`：确认等待时间超过 Node TTL，且执行过 `./minik8s get nodes` 或 node liveness loop 正在运行。
- Service endpoint 仍包含 node-b PodIP：确认使用的是包含 NodeLost 级联修复后的 `bridge`，并等待一次 service sync。
- node-b 重新出现 Ready：确认 node-b sailer 已停止，且没有 systemd/supervisor 自动拉起。

清理：重新启动 node-b sailer，使节点回到 Ready，然后删除测试对象。

```bash
./minik8s delete service nginx-service || true
./minik8s delete pod nginx-node-b || true
./minik8s get nodes
```

## POD-06：失败原因回写

目标：用 mock runtime 稳定验证 sandbox/container 创建失败时 Pod 进入 Failed，并写入 reason。

机器：任意开发机。

流程：

```bash
go test ./internal/sailer -run 'SandboxCreationFailure|SyncOnce' -count=1 -v
```

期望：

- sailer 测试断言 Pod 进入 `Failed`。
- sailer 测试断言只处理本节点 assigned Pods，并回写 status。

失败排查：

- 若测试失败，优先查看 PodController 的状态转换和 Sailer 的 status API 回写逻辑。
