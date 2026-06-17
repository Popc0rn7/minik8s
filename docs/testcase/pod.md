# Pod 测试用例

本文档覆盖 v0.1.0 的 Pod 生命周期、状态展示、资源映射、重启策略、双 worker
心跳调度，以及 NodeLost/删除 Node 时的 Pod 状态级联更新。

## 覆盖矩阵

| Case | 目标 | 机器 | 状态恢复要求 |
| --- | --- | --- | --- |
| POD-01 | 无 CNI 基础 Pod、hostPort、delete 清理 | node-a | 删除 Pod，恢复 node-a 的 `sailer run` |
| POD-02 | hostPath volume 与 CPU/memory limit | node-a，必要时 node-b 辅助检查 | 删除 Pod，删除 marker 文件 |
| POD-02B | 多容器 localhost 通信与共享 volume | node-a，必要时实际运行节点辅助检查 | 删除 Pod，删除 marker 文件 |
| POD-03 | restartPolicy 崩溃重启与 restart count 展示 | node-a | 删除 Pod |
| POD-03A | livenessProbe 失败后重启容器 | node-a | 删除 Pod |
| POD-03B | readinessProbe 控制 Service endpoints | node-a | 删除 Pod 和 Service |
| POD-04 | 双 worker 心跳与未指定 nodeName 的调度 | node-a + node-b | 删除 Pod，保持两个节点 Ready |
| POD-05 | NodeLost 后 Pod Unknown 与 Service endpoint 移除 | node-a + node-b | 重启 node-b 的 `sailer run`，删除 Pod 和 Service |
| POD-05B | 删除 Node 后级联删除 assigned Pods | node-a + node-b | node-b 重新 join 并启动 worker |
| POD-06 | mock runtime 失败原因回写 | 任意开发机 | 无集群状态变更 |

## POD-01：基础 Pod 生命周期

目标：验证 `kind: Pod`、metadata、container image/tag、command/args、hostPort、`apply/get/delete`。

机器：node-a。此 case 是 hostPort 诊断用例，必须和默认双 worker + mooring CNI
环境隔离执行；不要在已经 `apply -f manifest/cni/mooring.yaml` 的默认环境里把
`curl 127.0.0.1:8080` 写作必过项。

前置：

- 使用干净的测试状态目录，或在启用 mooring CNI manifest 前执行本 case。
- 本 case 应使用固定到 node-a 的 Pod manifest。可以直接使用
  `manifest/pod/pod_nginx_hostport_node_a.yaml`；不要用未指定 node 的
  `manifest/pod/pod_nginx.yaml` 验证 node-a hostPort。
- 停止 node-a 上当前默认 `sailer run`，然后只在该终端设置
  `MINIK8S_CNI_DISABLED=1`。
- 如果集群里已经存在 `kube-mooring/mooring-cni-cfg` 和
  `kube-mooring/mooring-cni-ds`，当前 `sailer run` 会优先启用 manifest CNI；
  `MINIK8S_CNI_DISABLED=1` 不会覆盖这条路径。此时本 case 只能验证
  Pod lifecycle 和 Docker runtime 信息，hostPort curl 预期应记录为当前实现偏差。

```fish
set -gx MINIK8S_CNI_DISABLED 1
./minik8s sailer run
```

流程：

```fish
# 清掉上一次残留，保证 apply 结果可判断
./kubectl delete pod nginx-hostport-node-a; or true

# 创建固定到 node-a 的 Pod 并观察 API 状态
./kubectl apply -f manifest/pod/pod_nginx_hostport_node_a.yaml
./kubectl get pods
./kubectl get pod nginx-hostport-node-a -o yaml

# 验证 hostPort 和 Docker runtime 状态
curl -fsS http://127.0.0.1:8080 >/tmp/minik8s-nginx.html
docker ps --filter label=minik8s.pod.name=nginx-hostport-node-a
docker inspect nginx-hostport-node-a-nginx --format '{{json .Config.Image}} {{json .Config.Entrypoint}} {{json .Config.Cmd}}'
docker inspect minik8s-pod-default-nginx-hostport-node-a-sandbox --format '{{json .HostConfig.PortBindings}} {{json .HostConfig.NetworkMode}}'

# 删除 Pod，等待 sailer 清理本地容器
./kubectl delete pod nginx-hostport-node-a
sleep 6
docker ps -a --filter label=minik8s.pod.name=nginx-hostport-node-a --format '{{.Names}} {{.Status}}'
```

期望：

- `apply` 输出 `pod/nginx-hostport-node-a created`。
- `get pods` 包含 `nginx-hostport-node-a`、`Running`、`default`、`app=nginx`；`get pod -o yaml`
  中 `spec.nodeName` 应为 `node-a`。
- 在未启用 manifest CNI 且 Docker hostPort 生效时，`curl` 返回 nginx 页面。
- 在已启用 mooring CNI 的默认环境中，如果 Pod status 含 `cniResult`，则
  `curl 127.0.0.1:8080` 失败是当前实现偏差，不应归因于 nginx 容器未启动。
- Docker 中能看到 sandbox 和 workload 容器。
- 删除后不再有 `nginx-hostport-node-a` 对应容器。

本轮实测暴露的问题与处理方式：

| 反预期点 | 证据 | Solution |
| --- | --- | --- |
| hostPort case 使用了未固定节点的 manifest，导致测试命令和实际运行节点不一致 | `get pod -o yaml` 中 `spec.nodeName` 不是 node-a | 这不是调度器错误，而是 testcase 设计错误；POD-01 必须 apply 固定到 node-a 的 manifest。 |
| 已设置 `MINIK8S_CNI_DISABLED=1`，Pod 仍走 CNI | `status.cniResult` 非空，sandbox `NetworkMode` 为 `none` | 预期上，显式禁用 CNI 应让本 case 走 Docker 原生网络/端口发布路径；如果 manifest CNI 已启用并覆盖该环境变量，则 hostPort 正向验证不成立。可选修正是让 `MINIK8S_CNI_DISABLED=1` 优先级高于 manifest CNI，或把本 case 放到未启用 manifest CNI 的独立状态目录。 |
| sandbox 有 `HostPort: 8080`，但宿主机没有 8080 监听 | `docker inspect` 有 `HostConfig.PortBindings`，`ss -ltnp | grep :8080` 为空 | 如果走 Docker hostPort 路径，宿主机应监听或可访问 8080；如果走 CNI `NetworkMode=none` 路径，Docker 不会自动完成 hostPort 转发。当前实现没有额外 hostPort portmap，因此该现象是 CNI 路径下 hostPort 未实现。 |
| 删除 API Pod 后实际节点仍有容器 | 删除事件发生时对应 node worker 未运行，节点无法执行本地 GC | Kubernetes 合理行为是：API 删除 Pod 后，kubelet 在线时应尽快终止容器；kubelet 离线时控制面不能直接清理该节点容器。Minik8s 的预期行为是 worker 恢复后列出本节点 runtime Pod，并清理不在当前 assigned desired 集合里的 orphan 容器。testcase 不应在 worker 停止时断言容器立即消失，应在 worker 恢复并完成一次 sync 后检查残留为空。 |

恢复状态：

```fish
./kubectl delete pod nginx-hostport-node-a; or true
rm -f /tmp/minik8s-nginx.html
```

停止 node-a 临时禁用 CNI 的 `sailer run`，然后在 node-a worker 终端恢复默认模式。
如果为了保证 node-a 单节点调度而停止了 node-b，也要恢复 node-b 的默认
`sailer run`。worker 恢复后应完成一次同步，并自动清理该节点上已经不属于
assigned Pods 的 Minik8s runtime 容器：

```fish
set -e MINIK8S_CNI_DISABLED
./minik8s sailer run
```

确认 node-a 回到 Ready 后再继续后续 case：

```fish
./kubectl get nodes
```

失败排查：

- `curl 127.0.0.1:8080` 失败：先看 `spec.nodeName` 是否为 node-a，再看
  `status.cniResult` 是否非空；如果非空，说明当前走的是 CNI 路径，不是 hostPort
  正向环境。
- 删除后容器仍在：确认对应节点的 `sailer run` 已恢复并完成一次同步；恢复后仍残留才是
  runtime orphan GC 问题。

## POD-02：volume 与资源限制

目标：验证 `hostPath` volume、`volumeMounts`、CPU/memory limit、namespace 过滤。Pod YAML 不指定 `nodeName`，实际运行节点由 scheduler 决定。

机器：node-a 执行 CLI。Pod 可能被调度到 node-a 或 node-b；marker 文件和 Docker inspect 需要在实际运行节点检查。

前置：node-a/node-b 均运行默认 `sailer run`，`./kubectl get nodes` 能看到两个节点为 `Ready`。

```fish
./kubectl delete pod volume-resource-pod -n demo; or true
mkdir -p /tmp/minik8s-case-data
rm -f /tmp/minik8s-case-data/marker
```

流程：

```fish
# 创建带 hostPath volume 和 resource limit 的 Pod
./kubectl apply -f manifest/pod/pod_volume_resource.yaml
sleep 6

# 查看 namespace 过滤、调度节点和资源声明
./kubectl get pods -n demo
./kubectl describe pod volume-resource-pod -n demo
```

根据 `describe pod` 中的 Node，到实际运行节点执行宿主机检查：

```fish
# 在实际运行节点检查 hostPath 写入和 Docker resource 映射
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

```fish
./kubectl delete pod volume-resource-pod -n demo; or true
rm -f /tmp/minik8s-case-data/marker
sleep 6
docker ps -a --filter label=minik8s.pod.name=volume-resource-pod --format '{{.Names}} {{.Status}}'
```

如果 Pod 被调度到 node-b，也在 node-b 执行 marker 和 Docker 残留检查：

```fish
rm -f /tmp/minik8s-case-data/marker
docker ps -a --filter label=minik8s.pod.name=volume-resource-pod --format '{{.Names}} {{.Status}}'
```

失败排查：

- marker 文件不存在：确认容器已 Running，再检查实际运行节点上的 hostPath 目录权限。
- 资源值为 0：确认使用的是 workload 容器 `volume-resource-pod-writer`，不是 sandbox。

## POD-02B：多容器 localhost 通信与共享 volume

目标：验证同一 Pod 内多个容器共享网络命名空间和 volume。一个容器提供 HTTP 服务，另一个
容器通过 `localhost` 访问该服务并把结果写入共享 volume。

机器：node-a 执行 CLI。Pod 可能被调度到 node-a 或 node-b；Docker exec 和 marker 文件检查
需要在实际运行节点执行。

前置：node-a/node-b 均运行默认 `sailer run`，`./kubectl get nodes` 能看到两个节点为 `Ready`。

```fish
./kubectl delete pod multicontainer-localhost -n demo; or true
mkdir -p /tmp/minik8s-multicontainer
rm -f /tmp/minik8s-multicontainer/result
```

创建临时 YAML：

```fish
begin
  echo 'kind: Pod'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: multicontainer-localhost'
  echo '  namespace: demo'
  echo '  labels:'
  echo '    app: multicontainer-localhost'
  echo 'spec:'
  echo '  containers:'
  echo '  - name: web'
  echo '    image: python'
  echo '    imageTag: "3.12-alpine"'
  echo '    command: ["sh", "-c"]'
  echo '    args: ["mkdir -p /srv && echo pod-localhost-ok > /srv/index.html && python -m http.server 8080 -d /srv"]'
  echo '    ports:'
  echo '    - containerPort: 8080'
  echo '    volumeMounts:'
  echo '    - name: shared'
  echo '      mountPath: /shared'
  echo '  - name: client'
  echo '    image: busybox'
  echo '    imageTag: 1.36'
  echo '    command: ["sh", "-c"]'
  echo '    args: ["sleep 3; wget -qO- http://127.0.0.1:8080 > /shared/result; sleep 3600"]'
  echo '    volumeMounts:'
  echo '    - name: shared'
  echo '      mountPath: /shared'
  echo '  volumes:'
  echo '  - name: shared'
  echo '    hostPath:'
  echo '      path: /tmp/minik8s-multicontainer'
  echo '      type: Directory'
  echo '  restartPolicy: Always'
end > /tmp/minik8s-pod-multicontainer.yaml
```

流程：

```fish
./kubectl apply -f /tmp/minik8s-pod-multicontainer.yaml
sleep 10
./kubectl get pod multicontainer-localhost -n demo -o yaml
./kubectl describe pod multicontainer-localhost -n demo
```

根据 `describe pod` 中的 Node，到实际运行节点执行：

```fish
cat /tmp/minik8s-multicontainer/result
set WEB_CID (docker ps -q --filter label=minik8s.pod.name=multicontainer-localhost --filter label=minik8s.container.name=web)
set CLIENT_CID (docker ps -q --filter label=minik8s.pod.name=multicontainer-localhost --filter label=minik8s.container.name=client)
docker exec "$CLIENT_CID" wget -qO- http://127.0.0.1:8080
docker inspect "$WEB_CID" --format '{{json .HostConfig.NetworkMode}} {{json .HostConfig.Mounts}}'
docker inspect "$CLIENT_CID" --format '{{json .HostConfig.NetworkMode}} {{json .HostConfig.Mounts}}'
```

期望：

- Pod 有两个 workload 容器 `web` 和 `client`，最终为 `Running`。
- `client` 容器内访问 `http://127.0.0.1:8080` 返回 `pod-localhost-ok`。
- 宿主机共享目录 `/tmp/minik8s-multicontainer/result` 内容为 `pod-localhost-ok`。
- 两个 workload 容器都挂载同一个 shared volume。

恢复状态：

```fish
./kubectl delete pod multicontainer-localhost -n demo; or true
rm -f /tmp/minik8s-pod-multicontainer.yaml
rm -f /tmp/minik8s-multicontainer/result
sleep 6
docker ps -a --filter label=minik8s.pod.name=multicontainer-localhost --format '{{.Names}} {{.Status}}'
```

失败排查：

- `localhost` 访问失败：确认当前 runtime 是否让同 Pod workload 容器加入同一个 sandbox
  network namespace，而不是各自独立网络。
- marker 文件不存在：先确认 `client` 容器仍在运行，再查看 `docker logs` 或容器退出状态。
- Pod 长时间 Pending：确认 `demo` namespace 不影响调度，且至少一个 Node Ready。

## POD-03：崩溃后重启

目标：验证 `restartPolicy: Always` 下容器退出后，sailer 下一轮同步会重启同一容器。

机器：node-a 执行 CLI。Pod 可能被调度到 node-a 或 node-b；`docker kill` 与 `docker inspect` 需要在实际运行节点执行。

前置：node-a/node-b 均运行默认 `sailer run`，`./kubectl get nodes` 能看到两个节点为 `Ready`。

```fish
./kubectl delete pod busybox-client; or true
```

流程：

```fish
# 创建会长期 sleep 的 client Pod
./kubectl apply -f manifest/pod/pod_busybox_client.yaml
sleep 6

# 先确认 Pod 被调度到哪个节点，再去该节点执行 Docker 检查
./kubectl describe pod busybox-client
```

根据 `describe pod` 中的 Node，到实际运行节点执行重启检查：

```fish
# 找到 workload 容器并模拟崩溃
set CLIENT_CID (docker ps -q --filter label=minik8s.pod.name=busybox-client --filter label=minik8s.container.name=client)
docker kill "$CLIENT_CID"
docker inspect "$CLIENT_CID" --format '{{.State.Status}}'

# 等待 sailer 下一轮同步后确认容器恢复运行，并观察重启计数
sleep 6
docker inspect "$CLIENT_CID" --format '{{.State.Status}}'
./kubectl get pods
./kubectl describe pod busybox-client
```

期望：

- kill 后第一次 inspect 显示 `exited`。
- 等待 sailer 同步后 inspect 显示 `running`。
- `get pods` 中 `busybox-client` 仍为 `Running`，`READY` 为 `1/1`，`RESTARTS` 至少为 `1`。
- `describe pod busybox-client` 的 `Containers` 段显示 `client ready=true restarts=... state=Running`。

恢复状态：

```fish
./kubectl delete pod busybox-client; or true
sleep 6
docker ps -a --filter label=minik8s.pod.name=busybox-client --format '{{.Names}} {{.Status}}'
```

最后一条 Docker 检查应在 Pod 实际运行节点执行；如果 Pod 被调度到 node-b，也在 node-b 执行。

失败排查：

- 容器没有重启：确认实际运行节点上的 sailer 没退出，并检查 Pod 的 `restartPolicy`。
- `CLIENT_CID` 为空：先通过 `describe pod busybox-client` 确认 Pod 被调度到哪个节点，再到该节点执行 Docker 命令。

## POD-03A：livenessProbe 失败后重启

目标：验证 `livenessProbe` 不是只解析字段；当探针连续失败达到阈值后，sailer 在实际运行节点重启该容器，并把原因写回 Pod status。

机器：node-a 执行 CLI。Pod 可能被调度到 node-a 或 node-b；Docker 检查需要在实际运行节点执行。

前置：node-a/node-b 均运行默认 `sailer run`，`./kubectl get nodes` 能看到两个节点为 `Ready`。

```fish
./kubectl delete pod liveness-demo; or true
```

创建临时 YAML：

```fish
begin
  echo 'kind: Pod'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: liveness-demo'
  echo '  namespace: default'
  echo '  labels:'
  echo '    app: liveness-demo'
  echo 'spec:'
  echo '  restartPolicy: Always'
  echo '  containers:'
  echo '  - name: app'
  echo '    image: busybox'
  echo '    imageTag: 1.36'
  echo '    command: ["sh", "-c"]'
  echo '    args: ["touch /tmp/healthy && sleep 3600"]'
  echo '    livenessProbe:'
  echo '      exec:'
  echo '        command: ["cat", "/tmp/healthy"]'
  echo '      initialDelaySeconds: 1'
  echo '      timeoutSeconds: 1'
  echo '      failureThreshold: 1'
end >/tmp/minik8s-pod-liveness.yaml
```

流程：

```fish
./kubectl apply -f /tmp/minik8s-pod-liveness.yaml
sleep 8
./kubectl describe pod liveness-demo
```

根据 `describe pod` 中的 Node，到实际运行节点删除健康标记并等待重启：

```fish
set APP_CID (docker ps -q --filter label=minik8s.pod.name=liveness-demo --filter label=minik8s.container.name=app)
docker exec "$APP_CID" rm -f /tmp/healthy
sleep 12
./kubectl get pods
./kubectl describe pod liveness-demo
```

期望：

- 删除 `/tmp/healthy` 前，`liveness-demo` 为 `Running`，`READY` 为 `1/1`。
- 删除健康标记后，`RESTARTS` 至少为 `1`。
- `describe pod liveness-demo` 显示 `Reason: LivenessProbeFailed` 或 `Message` 中包含 liveness probe failure。
- `Containers` 段显示 `app ready=true restarts=... state=Running`。

恢复状态：

```fish
./kubectl delete pod liveness-demo; or true
rm -f /tmp/minik8s-pod-liveness.yaml
sleep 6
```

失败排查：

- 没有重启：确认 `docker exec rm -f /tmp/healthy` 在 Pod 实际运行节点执行，且对应节点的 sailer 仍在线。
- Pod 一直 NotReady：确认容器是否反复快速重启，检查 `describe pod` 的 `Message`。

## POD-03B：readinessProbe 控制 Service endpoints

目标：验证 `readinessProbe` 会影响 Service endpoint。Pod 运行但 readiness 失败时，不应接入 Service；readiness 恢复后应进入 endpoints。

机器：node-a 执行 CLI。Pod 可能被调度到 node-a 或 node-b；Docker exec 需要在实际运行节点执行。

前置：node-a/node-b 均运行默认 `sailer run`，`./kubectl get nodes` 能看到两个节点为 `Ready`。

```fish
./kubectl delete service readiness-demo; or true
./kubectl delete pod readiness-demo; or true
```

创建临时 Pod 和 Service YAML：

```fish
begin
  echo 'kind: Pod'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: readiness-demo'
  echo '  namespace: default'
  echo '  labels:'
  echo '    app: readiness-demo'
  echo 'spec:'
  echo '  containers:'
  echo '  - name: app'
  echo '    image: python'
  echo '    imageTag: "3.12-alpine"'
  echo '    command: ["sh", "-c"]'
  echo '    args: ["python -m http.server 8080 -d /tmp"]'
  echo '    ports:'
  echo '    - containerPort: 8080'
  echo '    readinessProbe:'
  echo '      exec:'
  echo '        command: ["cat", "/tmp/ready"]'
  echo '      initialDelaySeconds: 1'
  echo '      timeoutSeconds: 1'
  echo '      failureThreshold: 1'
end >/tmp/minik8s-pod-readiness.yaml

begin
  echo 'kind: Service'
  echo 'apiVersion: v1'
  echo 'metadata:'
  echo '  name: readiness-demo'
  echo '  namespace: default'
  echo 'spec:'
  echo '  type: ClusterIP'
  echo '  selector:'
  echo '    matchLabels:'
  echo '      app: readiness-demo'
  echo '  ports:'
  echo '  - port: 80'
  echo '    targetPort: 8080'
end >/tmp/minik8s-service-readiness.yaml
```

流程：

```fish
./kubectl apply -f /tmp/minik8s-pod-readiness.yaml
./kubectl apply -f /tmp/minik8s-service-readiness.yaml
sleep 12
./kubectl get pods
./kubectl describe pod readiness-demo
./kubectl describe service readiness-demo
```

根据 `describe pod` 中的 Node，到实际运行节点创建 readiness 标记：

```fish
set APP_CID (docker ps -q --filter label=minik8s.pod.name=readiness-demo --filter label=minik8s.container.name=app)
docker exec "$APP_CID" sh -c 'echo ready >/tmp/ready'
sleep 12
./kubectl get pods
./kubectl describe pod readiness-demo
./kubectl describe service readiness-demo
```

期望：

- 创建 `/tmp/ready` 前，Pod phase 可以是 `Running`，但 `READY` 为 `0/1`。
- 创建 `/tmp/ready` 前，`describe service readiness-demo` 的 `Endpoints` 为 `-`。
- 创建 `/tmp/ready` 后，`READY` 变为 `1/1`。
- 创建 `/tmp/ready` 后，Service endpoints 出现该 Pod 的 PodIP 和 targetPort `8080`。

恢复状态：

```fish
./kubectl delete service readiness-demo; or true
./kubectl delete pod readiness-demo; or true
rm -f /tmp/minik8s-pod-readiness.yaml
rm -f /tmp/minik8s-service-readiness.yaml
sleep 6
```

失败排查：

- Service 一直有 endpoint：确认运行的是包含 readiness 过滤的 bridge，并检查 Pod `Containers` 段是否已经 `ready=true`。
- Service 一直无 endpoint：确认 readiness 标记是在 Pod 实际运行节点的 workload 容器中创建。

## POD-04：双 worker 心跳与调度

目标：验证 node-a、node-b 都能注册为 Ready；未指定 `spec.nodeName` 的 Pod 会在节点心跳后被 Navigator 分配。

机器：node-a 执行 CLI；node-a/node-b 都需运行默认 `sailer run`。

前置：node-a/node-b 均运行默认 `sailer run`，且 node-a 已退出 `POD-01` 使用的
`MINIK8S_CNI_DISABLED=1` 临时模式。

```fish
# 清理旧的第二个 nginx Pod，并确认两个 worker 仍可调度
./kubectl delete pod nginx-pod-2; or true
./kubectl get nodes
```

流程：

```fish
# 创建未指定 nodeName/nodeSelector 的 Pod，让 Navigator 分配节点
./kubectl apply -f manifest/pod/pod_nginx_2.yaml
sleep 6

# 查看实际写回的 spec.nodeName 和运行状态
./kubectl get pod nginx-pod-2 -o yaml
./kubectl describe pod nginx-pod-2
```

期望：

- `get nodes` 显示 `node-a` 和 `node-b`，状态为 `Ready`。
- `nginx-pod-2` 的 `spec.nodeName` 不为空，且为 `node-a` 或 `node-b`。
- Pod 最终进入 `Running` 并拥有 PodIP。

恢复状态：

```fish
./kubectl delete pod nginx-pod-2; or true
sleep 6
./kubectl get nodes
docker ps -a --filter label=minik8s.pod.name=nginx-pod-2 --format '{{.Names}} {{.Status}}'
```

如果 Pod 被调度到 node-b，也在 node-b 执行最后一条 Docker 检查。恢复完成后，两个节点应仍为 `Ready`。

失败排查：

- `spec.nodeName` 为空：确认至少一个 sailer 正在心跳，默认 TTL 为 30s；用户 YAML 中不应指定 `nodeName`。
- Pod 被分到 node-b 但没 Running：到 node-b 查看 sailer 日志和 Docker 状态。

## POD-05：NodeLost 状态级联

目标：验证 node-b heartbeat 超时后，控制面将该节点上的非终态 Pod 从 `Running` 标为 `Unknown`，写入 `reason: NodeLost`，并从匹配的 Service endpoints 中移除该 PodIP。

机器：node-a 执行 CLI；node-b 需要先运行 `sailer run`，再在流程中手动停止。

前置：node-a/node-b 均运行默认 `sailer run`，且 `./kubectl get nodes` 能看到两个节点为 `Ready`。

```fish
# 清理可能影响 endpoints 的旧对象
./kubectl delete service nginx-service; or true
./kubectl delete pod nginx-node-b; or true
./kubectl get nodes
```

流程：

```fish
# 创建固定到 node-b 的 nginx 后端
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8

# 创建 Service，记录停止 worker 前的 endpoints
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 6
./kubectl get pod nginx-node-b -o yaml
./kubectl describe service nginx-service
```

在 node-b 的 `sailer run` 终端按 `Ctrl-C` 停止 worker，等待超过默认 Node TTL：

```fish
# node-b worker 已停止；等待控制面把节点和 Pod 标为异常
sleep 35
./kubectl get nodes

# 检查 Pod 状态级联和 Service endpoints 更新
./kubectl get pod nginx-node-b -o yaml
./kubectl describe service nginx-service
```

期望：

- 停止 node-b worker 前，`nginx-node-b` 为 `Running`，有非空 `podIP`。
- 停止 node-b worker 前，`nginx-service` endpoints 包含 `nginx-node-b` 对应 PodIP。
- 如果用 `Ctrl-C` 或 SIGTERM 正常停止 `sailer run`，`node-b` 应很快变为 `Unknown`；如果用
  `kill -9` 强杀进程，则需要等待默认 30s Node TTL 后由 bridge liveness loop 标记为 `Unknown`。
- `nginx-node-b` 的 `status.phase` 为 `Unknown`，`status.reason` 为 `NodeLost`。
- `nginx-node-b` 的 `status.podIP` 仍保留，用于诊断。
- `nginx-service` endpoints 不再包含 `nginx-node-b` 的 PodIP；如果没有其他 Running 且 label `app=nginx` 的 Pod，endpoints 应为空。

恢复状态：先在 node-b 重新启动默认 `sailer run`，使节点重新心跳。

```fish
set -e MINIK8S_CNI_DISABLED
./minik8s sailer run
```

然后在 node-a 删除测试对象并确认节点恢复：

```fish
./kubectl delete service nginx-service; or true
./kubectl delete pod nginx-node-b; or true
sleep 6
./kubectl get nodes
./kubectl describe service nginx-service; or true
```

如果 node-b 上仍有残留容器，在 node-b 检查：

```fish
docker ps -a --filter label=minik8s.pod.name=nginx-node-b --format '{{.Names}} {{.Status}}'
```

失败排查：

- Pod 仍为 `Running`：确认等待时间超过 Node TTL，且执行过 `./kubectl get nodes` 或 node liveness loop 正在运行。
- Service endpoint 仍包含 node-b PodIP：确认使用的是包含 NodeLost 级联修复后的 `bridge`，并等待一次 service sync。
- node-b 重新出现 Ready：确认 node-b 的 `sailer run` 已停止，且没有 systemd/supervisor 自动拉起。

## POD-05B：Node 删除级联

目标：验证显式删除 Node 时，控制面删除该 Node、级联删除已调度到该 Node 的 Pod、刷新 Service endpoints，并撤销旧 node token；被删除 worker 需要重新 `join` 后才能再次心跳。

机器：node-a 执行 CLI；node-b 需要先运行 `sailer run`。

前置：node-a/node-b 均运行默认 `sailer run`，且 `./kubectl get nodes` 能看到两个节点为 `Ready`。

```fish
# 清理可能影响删除级联判断的旧对象
./kubectl delete service nginx-service; or true
./kubectl delete pod nginx-node-b; or true
./kubectl get nodes
```

流程：

```fish
# 创建固定到 node-b 的 Pod 和选择它的 Service
./kubectl apply -f manifest/pod/pod_nginx_node_b.yaml
sleep 8
./kubectl apply -f manifest/service/service_clusterip_nginx.yaml
sleep 6
./kubectl get pod nginx-node-b -o yaml
./kubectl describe service nginx-service

# 删除 Node，触发 assigned Pod 级联删除和 endpoint 刷新
./kubectl delete node node-b
sleep 6
./kubectl get nodes
./kubectl get pod nginx-node-b -o yaml; or true
./kubectl describe service nginx-service
```

期望：

- 删除前，`nginx-node-b` 为 `Running`，`spec.nodeName` 为 `node-b`。
- 删除后，`get nodes` 不再包含 `node-b`。
- 删除后，`nginx-node-b` 不再存在。
- `nginx-service` endpoints 不再包含 `nginx-node-b` 的 PodIP；如果没有其他 Running 且 label `app=nginx` 的 Pod，endpoints 应为空。
- node-b 上仍在运行的旧 `sailer run` 使用旧 node token 心跳会被拒绝；重新加入前不应把 `node-b` 注册回来。

恢复状态：在 node-b 停止旧 `sailer run`，重新执行 join，然后启动 run。

```fish
cd /opt/minik8s
set -gx HARBOR "http://<node-a 内网 IP>:18080"
set -gx BOOTSTRAP_TOKEN "<当前 bridge bootstrap token>"
./minik8s sailer join \
  --apiserver $HARBOR \
  --token $BOOTSTRAP_TOKEN \
  --node-name node-b
./minik8s sailer run
```

失败排查：

- Pod 未被删除：确认 Pod 的 `spec.nodeName` 是 `node-b`，且使用的是支持 Node 删除级联的 `bridge`。
- Service endpoint 仍包含旧 PodIP：等待一次 service sync，或执行 `./kubectl describe service nginx-service` 触发刷新。
- node-b 自动重新出现：确认旧 `sailer run` 已停止，且重新出现前已经完成新的 `sailer join`。

## POD-06：失败原因回写

目标：用 mock runtime 稳定验证 sandbox/container 创建失败时 Pod 进入 Failed，并写入 reason。

机器：任意开发机。

前置：不依赖双机集群；只需要本地 Go 测试环境可用。

流程：

```fish
# 只跑稳定覆盖失败状态回写的 sailer 单元测试
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

```fish
./kubectl delete service nginx-service; or true
./kubectl delete service readiness-demo; or true
./kubectl delete pod readiness-demo; or true
./kubectl delete pod liveness-demo; or true
./kubectl delete pod nginx-node-b; or true
./kubectl delete pod multicontainer-localhost -n demo; or true
./kubectl delete pod nginx-pod-2; or true
./kubectl delete pod busybox-client; or true
./kubectl delete pod volume-resource-pod -n demo; or true
./kubectl delete pod nginx-pod; or true
rm -f /tmp/minik8s-nginx.html
rm -f /tmp/minik8s-pod-liveness.yaml
rm -f /tmp/minik8s-pod-readiness.yaml
rm -f /tmp/minik8s-service-readiness.yaml
rm -f /tmp/minik8s-pod-multicontainer.yaml
rm -f /tmp/minik8s-multicontainer/result
rm -f /tmp/minik8s-case-data/marker
sleep 6
./kubectl get nodes
./kubectl get pods
```

在 node-a 和 node-b 分别检查残留容器：

```fish
docker ps -a --filter label=minik8s.pod.namespace=default
docker ps -a --filter label=minik8s.pod.namespace=demo
```

全量恢复完成后，node-a/node-b 的默认 `sailer run` 应继续运行，`./kubectl get nodes` 应显示两个节点为 `Ready`。
