# Pod 测试用例

本文档覆盖 v0.1.0 的 Pod 生命周期、状态展示、资源映射、重启策略，以及双 worker 心跳调度。双机前置步骤见 `docs/testcase/two-node.md`。

## 覆盖矩阵

| Case | 目标 | 机器 | 必跑 |
| --- | --- | --- | --- |
| POD-01 | 无 CNI 基础 Pod、hostPort、delete 清理 | node-a | 是 |
| POD-02 | hostPath volume 与 CPU/memory limit | node-a | 是 |
| POD-03 | restartPolicy 崩溃重启 | node-a | 是 |
| POD-04 | 双 worker 心跳与未指定 nodeName 的调度 | node-a + node-b | 是 |
| POD-05 | mock runtime 失败原因回写 | 任意开发机 | 是 |

## POD-01：基础 Pod 生命周期

目标：验证 `kind: Pod`、metadata、container image/tag、command/args、hostPort、`apply/get/delete`。

机器：node-a。此 case 使用 hostPort，建议临时禁用 CNI，避免已有 CNI 配置影响 Docker hostPort 行为。

前置：停止 node-a 上当前 kubesailer，临时在单独终端用禁用 CNI 的环境重新启动一个 kubesailer。

```bash
export MINIK8S_CNI_DISABLED=1
export MINIK8S_KUBEHARBOR=${KUBEHARBOR}
sudo env MINIK8S_PLAIN=1 NO_COLOR=1 MINIK8S_CNI_DISABLED=1 ./minik8s kubesailer \
  --node-name node-a \
  --kubeharbor ${KUBEHARBOR}
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
docker ps -a --filter label=minik8s.pod.name=nginx-pod
```

期望：

- `apply` 输出 `pod/nginx-pod created`。
- `get pods` 包含 `nginx-pod`、`Running`、`default`、`app=nginx`。
- `curl` 返回 nginx 页面。
- Docker 中能看到 sandbox 和 workload 容器。
- 删除后不再有 `nginx-pod` 对应容器。

失败排查：

- `curl 127.0.0.1:8080` 失败：确认 node-a 的 kubesailer 正在运行，且没有其他进程占用 8080。
- 删除后容器仍在：等待 kubesailer 下一轮同步，或临时执行 `sudo ./minik8s kubesailer --node-name node-a --kubeharbor ${KUBEHARBOR} --once`。

清理：

```bash
./minik8s delete pod nginx-pod || true
```

清理后停止这个临时 kubesailer，并按 `two-node.md` 重新启动启用 CNI 的 node-a kubesailer，再继续后续 CNI/Service case。

## POD-02：volume 与资源限制

目标：验证 `hostPath` volume、`volumeMounts`、CPU/memory limit、namespace 过滤。

机器：node-a。

流程：

```bash
mkdir -p /tmp/minik8s-case-data
rm -f /tmp/minik8s-case-data/marker
./minik8s apply -f manifest/testdata/pod_volume_resource.yaml
./minik8s get pods -n demo
cat /tmp/minik8s-case-data/marker
docker inspect volume-resource-pod-writer --format '{{json .HostConfig.Mounts}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}'
```

期望：

- `get pods -n demo` 包含 `volume-resource-pod`、`Running`、`demo`。
- `/tmp/minik8s-case-data/marker` 内容为 `volume-ok`。
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

目标：验证 `restartPolicy: Always` 下容器退出后，kubesailer 下一轮同步会重启同一容器。

机器：node-a。

流程：

```bash
./minik8s apply -f manifest/testdata/pod_busybox_client.yaml
CLIENT_CID=$(docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker kill "${CLIENT_CID}"
docker inspect "${CLIENT_CID}" --format '{{.State.Status}}'
sleep 6
docker inspect "${CLIENT_CID}" --format '{{.State.Status}}'
./minik8s get pods
```

期望：

- kill 后第一次 inspect 显示 `exited`。
- 等待 kubesailer 同步后 inspect 显示 `running`。
- `get pods` 中 `busybox-client` 仍为 `Running`。

失败排查：

- 容器没有重启：确认 kubesailer 没退出，并检查 Pod 的 `restartPolicy`。

清理：

```bash
./minik8s delete pod busybox-client || true
```

## POD-04：双 worker 心跳与调度

目标：验证 node-a、node-b 都能注册为 Ready；未指定 `spec.nodeName` 的 Pod 会在节点心跳后被 Kubenavigator 分配。

机器：node-a 执行 CLI；node-a/node-b 的 kubesailer 都需运行。

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

- `spec.nodeName` 为空：确认至少一个 kubesailer 正在心跳，默认 TTL 为 30s。
- Pod 被分到 node-b 但没 Running：到 node-b 查看 kubesailer 日志和 Docker 状态。

清理：

```bash
./minik8s delete pod nginx-pod-2 || true
```

## POD-05：失败原因回写

目标：用 mock runtime 稳定验证 sandbox/container 创建失败时 Pod 进入 Failed，并写入 reason。

机器：任意开发机。

流程：

```bash
go test ./internal/kubebridge/kubecaptain ./internal/kubesailer -run 'SandboxCreationFailure|SyncOnce' -count=1 -v
```

期望：

- kubecaptain 测试断言 Pod 进入 `Failed`。
- kubesailer 测试断言只处理本节点 assigned Pods，并回写 status。

失败排查：

- 若测试失败，优先查看 PodKubecaptain 的状态转换和 Kubesailer 的 status API 回写逻辑。
