# Metrics Server Testcase

## 目标

验证 `metrics` addon 启用后，bridge 暴露最小 `metrics.k8s.io/v1beta1` 接口，
并且 `kubectl top nodes`、`kubectl top pods` 能消费 sailer 上报的 CPU/Memory 数据。

## 步骤

1. 生成默认 addon manifests：

   ```bash
   ./minik8s init --force
   ```

2. 启动 bridge：

   ```bash
   ./minik8s bridge --listen :18080 --addons dns,metrics
   ```

3. 在另一个终端启动至少一个 sailer，并确保 Pod 已被调度到该节点：

   ```bash
   MINIK8S_HARBOR=http://127.0.0.1:18080 ./minik8s sailer manifest/node/node_a.yaml
   ```

4. 查看 Metrics API 资源发现：

   ```bash
   MINIK8S_HARBOR=http://127.0.0.1:18080 ./kubectl api-resources
   curl http://127.0.0.1:18080/apis/metrics.k8s.io/v1beta1/pods
   curl http://127.0.0.1:18080/apis/metrics.k8s.io/v1beta1/nodes
   ```

   预期：返回 `PodMetricsList` 和 `NodeMetricsList`，usage 中包含 `cpu` 与 `memory`。

5. 查看 top：

   ```bash
   MINIK8S_HARBOR=http://127.0.0.1:18080 ./kubectl top pods
   MINIK8S_HARBOR=http://127.0.0.1:18080 ./kubectl top nodes
   ```

   预期：输出 `NAME CPU MEMORY` 表格。没有 sailer metrics 样本时表格为空。

## 边界

- 当前 metrics-server 是 Minik8s adapter：bridge 直接提供 metrics API，底层复用
  sailer 通过 Docker stats 上报的 CPU/Memory 样本。
- 未实现 Kubernetes API aggregation、TLS、RBAC、完整 metrics-server scrape 逻辑。
- 未启用 `metrics` addon 时，HPA 仍可能因为缺少指标样本进入 `MetricsUnavailable` 类状态。
