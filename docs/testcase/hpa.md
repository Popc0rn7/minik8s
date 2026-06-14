# HPA 测试用例

本文档验证课程版 `HorizontalPodAutoscaler`：HPA 读取 `sailer` 上报的 Docker
CPU/Memory metrics，调整目标 ReplicaSet 的 `spec.replicas`，再由 ReplicaSet
controller 创建或删除 Pod。

当前实现覆盖 HPA YAML/API/CLI、metrics 读取、按上下限调整 ReplicaSet。它不是完整
Kubernetes HPA，不包含复杂 stabilization policy 或多 Workload 类型。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| HPA-00 | metrics/HPA 环境基线 | node-a + worker | 保持 metrics addon |
| HPA-01 | HPA 创建与展示 | node-a | 删除 HPA/RS |
| HPA-02 | metrics 缺失时 condition | node-a | 等待或记录缺失原因 |
| HPA-03 | CPU 压力触发扩容 | Pod 实际运行节点 | 停止压力进程 |
| HPA-04 | 压力停止后冷却缩容 | node-a | 删除 HPA 不删除 RS |
| HPA-05 | HPA 单元测试 | 任意开发机 | 不改变集群 |

## HPA-00：环境基线

前置：

- bridge 以 `--addons dns,metrics` 或至少 `--addons metrics` 启动。
- 至少一个 `sailer run` 在线。
- HPA 目标 ReplicaSet 的 Pod template 必须设置 CPU 和 Memory requests；没有 requests
  时 HPA 会把对应指标标记为缺失并跳过伸缩。
- 第一次 metrics 上报没有 CPU delta，CPU 指标通常要等第二轮 `sailer` 同步。

```fish
./kubectl get nodes
./kubectl api-resources | grep -E 'hpa|metrics'
./kubectl top nodes; or true
```

期望：

- Ready Node 至少 1 个。
- API resources 包含 HPA 和 metrics。

## HPA-01：创建 ReplicaSet 和 HPA

```fish
./kubectl delete hpa nginx-hpa; or true
./kubectl delete rs nginx-rs; or true
sleep 8

./kubectl apply -f manifest/replicaset/replicaset_nginx.yaml
sleep 10
./kubectl apply -f manifest/hpa/hpa_nginx.yaml
sleep 6
./kubectl get hpa
./kubectl describe hpa nginx-hpa
./kubectl get rs nginx-rs
```

期望：

- `get hpa` 显示 `nginx-hpa`，target 为 `ReplicaSet/nginx-rs`。
- min/max replicas 与 YAML 一致。
- 如果 Pod 尚未 Running 或 metrics 尚未上报，condition 可显示 `MetricsUnavailable`。

失败排查：

- `MissingRequest`：确认 ReplicaSet template 中 containers 设置了
  `resources.requests.cpu` 和 `resources.requests.memory`。
- HPA 不存在：确认 bridge 编译了 HPA routes/store/controller。

## HPA-02：metrics 缺失 condition

目标：把等待状态和失败状态分开记录。

```fish
./kubectl describe hpa nginx-hpa
./kubectl top pods; or true
curl --noproxy '*' -fsS $HARBOR/apis/metrics.k8s.io/v1beta1/pods
```

期望：

- Pod 未 Running 或 metrics 未就绪时，HPA condition 指向 `MetricsUnavailable` 或等价原因。
- `kubectl top pods` 和 metrics API 能解释是否已有样本。

## HPA-03：压力触发扩容

目标：制造业务容器 CPU 压力，验证 replicas 每轮最多增加 1，直到达到 maxReplicas 或压力缓解。

先找一个 `nginx-rs` Pod 的实际运行节点：

```fish
./kubectl get pods
./kubectl describe pod nginx-rs-1
```

到实际运行节点执行：

```fish
set NGINX_CID (docker ps -q --filter label=minik8s.pod.name=nginx-rs-1 --filter label=minik8s.container.name=nginx)
docker exec -d "$NGINX_CID" sh -c 'while true; do :; done'
```

node-a 观察两到三轮 HPA 同步：

```fish
./kubectl get hpa
./kubectl get rs nginx-rs
./kubectl get pods
sleep 20
./kubectl get hpa
./kubectl get rs nginx-rs
```

期望：

- HPA current metrics 中 CPU utilization 上升。
- `nginx-rs` desired replicas 每轮最多增加 1。
- replicas 不超过 `maxReplicas: 3`。

失败排查：

- 没有扩容：确认压力进程在业务容器中，不是 sandbox；确认 metrics API 有 CPU 样本。

## HPA-04：停止压力后缩容

到运行压力的节点停止进程：

```fish
docker exec "$NGINX_CID" sh -c "pkill -f 'while true' || true"
```

node-a 等待冷却窗口后检查：

```fish
sleep 30
./kubectl get hpa
./kubectl get rs nginx-rs
./kubectl delete hpa nginx-hpa
./kubectl get rs nginx-rs
```

期望：

- HPA 不会立即剧烈缩容；冷却窗口后每轮最多减少 1 个副本。
- 删除 HPA 不删除 ReplicaSet，也不回滚当前 replicas。

## HPA-05：单元测试

```fish
go test ./pkg/yaml ./internal/metrics ./internal/bridge/logbook ./internal/bridge/captain ./internal/bridge/harbor ./internal/cli -run 'HPA|Metrics|Utilization' -count=1
```

期望：

- YAML、metrics quantity/utilization、HPA store/controller/API/CLI 测试通过。

## 全量恢复

```fish
./kubectl delete hpa nginx-hpa; or true
./kubectl delete rs nginx-rs; or true
sleep 10
./kubectl get hpa; or true
./kubectl get rs; or true
./kubectl get pods
```

如压力进程残留，到实际运行节点检查并停止：

```fish
docker ps --filter label=minik8s.pod.name=nginx-rs-1
```
