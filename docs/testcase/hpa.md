# HPA 测试用例

本文档验证课程版 `HorizontalPodAutoscaler`：HPA 读取 `sailer` 上报的 Docker
CPU/Memory metrics，调整目标 ReplicaSet 的 `spec.replicas`，再由 ReplicaSet
controller 创建或删除 Pod。

## 前置条件

- 已完成 `make build`。
- `bridge` 和至少一个 `sailer` 正常运行。
- HPA 目标 ReplicaSet 的 Pod template 必须设置 CPU 和 Memory requests；没有
  requests 时 HPA 会把对应指标标记为缺失并跳过伸缩。
- 第一次 metrics 上报没有 CPU delta，CPU 指标可能要等第二轮 `sailer` 同步后才
  可用。

## 启动控制面

```bash
./minik8s bridge --listen :18080 --hpa-sync-interval 15s
```

在 worker 节点启动 `sailer`，按现有双机或单机流程传入 Node YAML。

## HPA-01：创建 ReplicaSet 和 HPA

```bash
export MINIK8S_HARBOR=http://127.0.0.1:18080
./minik8s apply -f manifest/replicaset/replicaset_nginx.yaml
./minik8s apply -f manifest/hpa/hpa_nginx.yaml
./minik8s get hpa
./minik8s describe hpa nginx-hpa
```

预期：

- `get hpa` 显示 `nginx-hpa`，target 为 `ReplicaSet/nginx-rs`。
- 如果 Pod 尚未 Running 或 metrics 尚未上报，condition 可显示
  `MetricsUnavailable`，这是正常的等待状态。

## HPA-02：压力触发扩容

找到一个 nginx Pod 容器后制造 CPU 压力：

```bash
CID=$(docker ps -q --filter label=minik8s.pod.name=nginx-rs-1 --filter label=minik8s.container.name=nginx)
docker exec "$CID" sh -c 'while true; do :; done' &
```

等待两到三轮 HPA 同步：

```bash
watch -n 5 './minik8s get hpa && ./minik8s get rs && ./minik8s get pods'
```

预期：

- HPA current metrics 中 CPU utilization 上升。
- `nginx-rs` desired replicas 每轮最多增加 1，直到压力缓解或达到 `maxReplicas: 3`。

## HPA-03：停止压力后缩容

停止压力进程，等待缩容冷却时间：

```bash
pkill -f 'while true; do :; done' || true
./minik8s get hpa
./minik8s get rs
```

预期：

- HPA 不会立即剧烈缩容；冷却窗口后每轮最多减少 1 个副本。
- 删除 HPA 不会删除 ReplicaSet，也不会回滚当前 replicas：

```bash
./minik8s delete hpa nginx-hpa
./minik8s get rs
```

## 排查

- `MetricsUnavailable`：确认 `sailer` 仍在运行，Pod 已经 Running，并等待第二轮
  metrics 上报。
- `MissingRequest` 或长期不伸缩：确认 ReplicaSet template 中 containers 设置了
  `resources.requests.cpu` 和 `resources.requests.memory`。
- 没有扩容：确认压力实际发生在业务容器中，而不是 sandbox 容器中。
