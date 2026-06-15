# Metrics Server 测试用例

本文档验证 `metrics` addon 启用后，bridge 暴露最小
`metrics.k8s.io/v1beta1` API，并且 `kubectl top nodes`、`kubectl top pods` 能消费
`sailer` 上报的 Docker CPU/Memory 样本。当前实现是 Minik8s adapter，不是完整
Kubernetes API aggregation。

## 覆盖矩阵

| Case | 目标 | 机器 | 恢复要求 |
| --- | --- | --- | --- |
| METRICS-00 | metrics addon 启动 | node-a | 保持 bridge 运行 |
| METRICS-01 | metrics API resource discovery | node-a | 不改变集群 |
| METRICS-02 | Pod/Node metrics 样本 | node-a + worker | 删除测试 Pod |
| METRICS-03 | `kubectl top` 展示 | node-a | 不改变集群 |

## METRICS-00：addon 启动

```fish
make prod-deploy
./minik8s init --force
./minik8s bridge \
  --listen :18080 \
  --cluster-cidr $CLUSTER_CIDR \
  --node-cidr-mask-size 24 \
  --addons dns,metrics
```

另一个终端：

```fish
./kubectl version
./minik8s doctor addon metrics
./kubectl api-resources | grep metrics
```

期望：

- `doctor addon metrics` 最终显示 ready。
- `api-resources` 包含 metrics API 或 `top` 所需资源。

## METRICS-01：metrics API

```fish
curl --noproxy '*' -fsS $HARBOR/apis/metrics.k8s.io/v1beta1/pods
curl --noproxy '*' -fsS $HARBOR/apis/metrics.k8s.io/v1beta1/nodes
```

期望：

- 返回 `PodMetricsList` 和 `NodeMetricsList`。
- 有样本时 usage 中包含 `cpu` 与 `memory`。
- 没有 sailer metrics 样本时 items 可以为空，不记为业务失败。

## METRICS-02：Pod/Node metrics 样本

目标：创建一个 Pod，等待 sailer 至少上报两轮 Docker stats。

```fish
./kubectl delete pod nginx-node-a; or true
./kubectl apply -f manifest/pod/pod_nginx_node_a.yaml
sleep 20
curl --noproxy '*' -fsS $HARBOR/apis/metrics.k8s.io/v1beta1/pods
curl --noproxy '*' -fsS $HARBOR/apis/metrics.k8s.io/v1beta1/nodes
```

期望：

- Pod metrics items 中出现 `nginx-node-a` 或当前测试 Pod。
- Node metrics items 中出现对应 node。
- CPU 可能需要第二轮样本才从 delta 计算出非空值。

失败排查：

- items 为空：确认 `sailer run` 在线，Pod 已 Running，并等待第二轮 sync。
- memory 为空：确认 Docker stats 可用，业务容器不是 sandbox。

## METRICS-03：kubectl top

```fish
./kubectl top pods
./kubectl top nodes
```

期望：

- 输出 `NAME CPU MEMORY` 表格。
- 有样本时显示 Pod/Node 行；无样本时空表是允许状态。

## 全量恢复

```fish
./kubectl delete pod nginx-node-a; or true
sleep 8
./kubectl get pods
```

## 边界

- 当前 metrics-server 是 bridge 直接提供 metrics API。
- metrics addon 不是真实 scraper；sailer 上报 Docker stats 后，bridge 只在内存中保存
  最新样本，bridge 重启会丢失 metrics。
- CPU 需要相邻两轮样本计算 delta，首轮可能为空；API 没有 freshness gate，可能返回
  stale 样本。
- NodeMetrics 由当前 PodMetrics 汇总得到，不是节点原生指标。
- 未实现 Kubernetes API aggregation、TLS、RBAC、完整 metrics-server scrape 逻辑、
  cAdvisor 和 custom/external metrics。
- HPA 可以复用这些 metrics 样本；样本缺失时 HPA 会进入 `MetricsUnavailable`。
- 当前验收只证明 `sailer -> bridge -> metrics.k8s.io -> kubectl top` 最小链路；后续
  改造目标是真实 metrics-server/cAdvisor 或等价 scraper。
